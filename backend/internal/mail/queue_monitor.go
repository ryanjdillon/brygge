package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// QueueSummary is a point-in-time view of Stalwart's outbound queue.
type QueueSummary struct {
	Messages int64
	// Oldest is the creation time of the oldest queued message; zero when
	// the queue is empty.
	Oldest time.Time
}

// QueueSummary fetches the queue size and the oldest message in one
// request. Stalwart iterates the queue in ascending queue-id order and its
// ids are time-ordered snowflakes, so with limit=1 the single returned item
// is the oldest message while `total` still counts the whole queue.
// Without `values` the items are bare ids, which carry no creation time;
// it must be sent as `values=1` because Stalwart's query parser drops
// parameters with an empty value, so a bare `values` is ignored.
func (c *AdminClient) QueueSummary(ctx context.Context) (QueueSummary, error) {
	var env struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
			Total int64             `json:"total"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/queue/messages?values=1&limit=1", nil, &env); err != nil {
		return QueueSummary{}, err
	}
	sum := QueueSummary{Messages: env.Data.Total}
	if sum.Messages == 0 {
		return sum, nil
	}
	if len(env.Data.Items) == 0 {
		return QueueSummary{}, fmt.Errorf("stalwart queue: total %d but no items returned", sum.Messages)
	}
	var first struct {
		Created string `json:"created"`
	}
	if err := json.Unmarshal(env.Data.Items[0], &first); err != nil || first.Created == "" {
		return QueueSummary{}, errors.New("stalwart queue: first item has no created timestamp")
	}
	created, err := time.Parse(time.RFC3339, first.Created)
	if err != nil {
		return QueueSummary{}, fmt.Errorf("stalwart queue: parse created: %w", err)
	}
	sum.Oldest = created
	return sum, nil
}

type queueSummarizer interface {
	QueueSummary(ctx context.Context) (QueueSummary, error)
}

const (
	queuePollTimeout   = 5 * time.Second
	queueErrorLogEvery = 15 * time.Minute
)

// QueueMonitor polls Stalwart's outbound queue in the background and
// exposes the last successful result as observable gauges. The metric
// callbacks only read the cache, so a slow or unreachable Stalwart never
// stalls a metrics collection.
//
// Until the first successful poll only brygge.mail.queue.last_success is
// observed (as 0); afterwards a failed poll keeps the previous snapshot and
// the stale last_success is what tells the alert rules the numbers can no
// longer be trusted.
type QueueMonitor struct {
	client queueSummarizer
	log    zerolog.Logger
	now    func() time.Time

	mu          sync.Mutex
	have        bool
	messages    int64
	oldest      time.Time
	lastSuccess time.Time

	failing    bool
	lastErrLog time.Time
}

// NewQueueMonitor builds a monitor over client. Call RegisterMetrics once
// and Run in its own goroutine.
func NewQueueMonitor(client queueSummarizer, log zerolog.Logger) *QueueMonitor {
	return &QueueMonitor{
		client: client,
		log:    log.With().Str("component", "mail-queue-monitor").Logger(),
		now:    time.Now,
	}
}

// RegisterMetrics registers the queue gauges on the global MeterProvider.
func (m *QueueMonitor) RegisterMetrics() error {
	_, err := m.registerMetrics(otel.Meter("brygge-api"))
	return err
}

func (m *QueueMonitor) registerMetrics(meter metric.Meter) (metric.Registration, error) {
	messages, err := meter.Int64ObservableGauge("brygge.mail.queue.messages",
		metric.WithDescription("Messages in the mail server's outbound queue"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, err
	}
	oldestAge, err := meter.Float64ObservableGauge("brygge.mail.queue.oldest_age",
		metric.WithDescription("Age of the oldest message in the outbound queue; 0 when the queue is empty"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	lastSuccess, err := meter.Int64ObservableGauge("brygge.mail.queue.last_success",
		metric.WithDescription("Unix time of the last successful queue poll; 0 before the first"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	return meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !m.have {
			o.ObserveInt64(lastSuccess, 0)
			return nil
		}
		o.ObserveInt64(messages, m.messages)
		age := 0.0
		if m.messages > 0 && !m.oldest.IsZero() {
			age = max(m.now().Sub(m.oldest).Seconds(), 0)
		}
		o.ObserveFloat64(oldestAge, age)
		o.ObserveInt64(lastSuccess, m.lastSuccess.Unix())
		return nil
	}, messages, oldestAge, lastSuccess)
}

// Run polls immediately and then every interval until ctx is done.
func (m *QueueMonitor) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		m.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *QueueMonitor) poll(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, queuePollTimeout)
	defer cancel()
	sum, err := m.client.QueueSummary(pollCtx)
	if ctx.Err() != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if err != nil {
		// Warn on the transition into failure, then at most every
		// queueErrorLogEvery: the poll runs every minute and a
		// down mail server would otherwise flood the journal.
		if !m.failing || now.Sub(m.lastErrLog) >= queueErrorLogEvery {
			m.log.Warn().Err(err).Msg("mail queue poll failed")
			m.lastErrLog = now
		}
		m.failing = true
		return
	}
	if m.failing {
		m.log.Info().Msg("mail queue poll recovered")
		m.failing = false
	}
	m.have = true
	m.messages = sum.Messages
	m.oldest = sum.Oldest
	m.lastSuccess = now
}
