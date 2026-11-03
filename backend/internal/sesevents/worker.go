package sesevents

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/rs/zerolog"
)

// SQSAPI is the subset of the SQS client the worker uses; *sqs.Client
// satisfies it, and tests substitute a fake.
type SQSAPI interface {
	GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, opts ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, opts ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// EventStore persists parsed records; *Store is the production
// implementation. Apply returns the records that were new (not
// redeliveries) and must be atomic: on error nothing is stored. Ping
// reports whether the store is reachable at all, which separates an outage
// from a single event the store rejects.
type EventStore interface {
	Apply(ctx context.Context, recs []Record) ([]Record, error)
	Ping(ctx context.Context) error
}

// Long-poll settings. 20 s is the SQS maximum wait and makes an idle queue
// cost three requests a minute instead of a tight empty-receive loop.
// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-short-and-long-polling.html
const (
	receiveWaitSeconds = 20
	receiveMaxMessages = 10
)

// Worker drains the SES events queue into the store.
type Worker struct {
	sqs      SQSAPI
	store    EventStore
	queue    string
	dlq      string
	log      zerolog.Logger
	metrics  *instruments
	queueURL string

	// MinBackoff and MaxBackoff bound the exponential wait after an AWS
	// error; DLQInterval is how often the DLQ depth is sampled. Defaults
	// come from NewWorker; tests shorten them.
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
	DLQInterval time.Duration
}

// NewWorker builds a worker for the queue and dead-letter queue names
// (URLs are resolved at start, so config carries only names derived from
// the club slug). dlq may be empty to skip DLQ sampling.
func NewWorker(client SQSAPI, store EventStore, queue, dlq string, log zerolog.Logger) *Worker {
	return &Worker{
		sqs:         client,
		store:       store,
		queue:       queue,
		dlq:         dlq,
		log:         log.With().Str("component", "ses-events-worker").Logger(),
		metrics:     defaultInstruments(),
		MinBackoff:  time.Second,
		MaxBackoff:  time.Minute,
		DLQInterval: time.Minute,
	}
}

// Run polls until ctx is cancelled. It blocks; start it in a goroutine.
func (w *Worker) Run(ctx context.Context) {
	url, ok := w.resolveQueueURL(ctx, w.queue)
	if !ok {
		return
	}
	w.queueURL = url
	w.log.Info().Str("queue", w.queue).Msg("SES events worker started")

	if w.dlq != "" {
		go w.sampleDLQ(ctx)
	}

	backoff := w.MinBackoff
	for ctx.Err() == nil {
		out, err := w.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:                    aws.String(w.queueURL),
			MaxNumberOfMessages:         receiveMaxMessages,
			WaitTimeSeconds:             receiveWaitSeconds,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount},
		})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			w.log.Warn().Err(err).Dur("retry_in", backoff).Msg("SES events receive failed")
			if !sleep(ctx, backoff) {
				break
			}
			backoff = nextBackoff(backoff, w.MaxBackoff)
			continue
		}
		backoff = w.MinBackoff
		for _, m := range out.Messages {
			if !w.handle(ctx, m) {
				// The rest of the batch would fail the same way; leave it
				// to reappear after the visibility timeout.
				w.waitForStore(ctx)
				break
			}
		}
	}
	w.log.Info().Msg("SES events worker stopped")
}

// waitForStore blocks until the store answers a ping. Every receive
// counts towards the queue's maxReceiveCount, so polling on through a
// database outage would move healthy events to the DLQ within a few
// visibility timeouts; not receiving until the store is back keeps them on
// the main queue (4-day retention) instead.
func (w *Worker) waitForStore(ctx context.Context) {
	backoff := w.MinBackoff
	for ctx.Err() == nil {
		if err := w.store.Ping(ctx); err == nil {
			return
		} else if ctx.Err() == nil {
			w.log.Warn().Err(err).Dur("retry_in", backoff).Msg("SES events store unreachable; pausing receives")
		}
		if !sleep(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, w.MaxBackoff)
	}
}

// handle processes one message and deletes it only once its records are
// committed. A message left undeleted reappears after the visibility
// timeout; after maxReceiveCount receives SQS moves it to the DLQ, which
// is how a poison message stops blocking without being lost.
// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html
//
// It returns false only when the store failed, so the caller can check
// whether the store is down before receiving more.
func (w *Worker) handle(ctx context.Context, m types.Message) bool {
	log := w.log.With().Str("sqs_message_id", aws.ToString(m.MessageId)).
		Str("receive_count", m.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]).
		Logger()

	recs, err := Parse([]byte(aws.ToString(m.Body)))
	switch {
	case errors.Is(err, ErrIgnoredEventType):
		w.metrics.recordReceived(ctx, eventTypeIgnored)
		log.Debug().Err(err).Msg("SES event skipped")
		w.delete(ctx, m, log)
		return true
	case err != nil:
		w.metrics.recordReceived(ctx, eventTypeInvalid)
		log.Warn().Err(err).Msg("SES event unparseable; leaving it for the dead-letter queue")
		return true
	}
	w.metrics.recordReceived(ctx, recs[0].EventType)

	inserted, err := w.store.Apply(ctx, recs)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn().Err(err).Str("ses_message_id", recs[0].SESMessageID).
				Msg("SES event not stored; it will be redelivered")
		}
		return false
	}
	for _, r := range inserted {
		w.metrics.recordDelivery(ctx, r)
	}
	w.delete(ctx, m, log)
	return true
}

// delete removes a handled message. A failed delete is harmless: the
// message is redelivered and the store drops it as a duplicate.
func (w *Worker) delete(ctx context.Context, m types.Message, log zerolog.Logger) {
	if _, err := w.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.queueURL),
		ReceiptHandle: m.ReceiptHandle,
	}); err != nil && ctx.Err() == nil {
		log.Warn().Err(err).Msg("SES event delete failed; it will be redelivered")
	}
}

// resolveQueueURL looks the queue up by name, retrying with backoff so a
// boot during an AWS or network blip still starts the worker.
func (w *Worker) resolveQueueURL(ctx context.Context, name string) (string, bool) {
	backoff := w.MinBackoff
	for {
		out, err := w.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
		if err == nil {
			return aws.ToString(out.QueueUrl), true
		}
		if ctx.Err() != nil {
			return "", false
		}
		w.log.Warn().Err(err).Str("queue", name).Dur("retry_in", backoff).Msg("SQS queue URL lookup failed")
		if !sleep(ctx, backoff) {
			return "", false
		}
		backoff = nextBackoff(backoff, w.MaxBackoff)
	}
}

// sampleDLQ records the DLQ depth every DLQInterval. Anything in the DLQ
// is an event that failed maxReceiveCount times and needs a human.
func (w *Worker) sampleDLQ(ctx context.Context) {
	url, ok := w.resolveQueueURL(ctx, w.dlq)
	if !ok {
		return
	}
	ticker := time.NewTicker(w.DLQInterval)
	defer ticker.Stop()
	for {
		out, err := w.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(url),
			AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages},
		})
		switch {
		case err != nil:
			if ctx.Err() == nil {
				w.log.Warn().Err(err).Msg("SES events DLQ depth lookup failed")
			}
		default:
			raw := out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)]
			if n, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
				w.metrics.recordDLQ(ctx, n)
				if n > 0 {
					w.log.Warn().Int64("messages", n).Msg("SES events dead-letter queue is not empty")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(cur, limit time.Duration) time.Duration {
	if cur *= 2; cur > limit {
		return limit
	}
	return cur
}
