package sesevents

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeSQS serves one batch of messages per ReceiveMessage call, then
// blocks like a long poll until the context ends.
type fakeSQS struct {
	mu          sync.Mutex
	urlFailures int
	batches     [][]types.Message
	receiveErrs []error
	deleted     []string
	dlqDepth    string
	dlqSampled  chan struct{}
	drained     chan struct{}
}

func (f *fakeSQS) GetQueueUrl(_ context.Context, in *sqs.GetQueueUrlInput, _ ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if aws.ToString(in.QueueName) == "events" && f.urlFailures > 0 {
		f.urlFailures--
		return nil, errors.New("network unreachable")
	}
	return &sqs.GetQueueUrlOutput{QueueUrl: aws.String("https://sqs.example/" + aws.ToString(in.QueueName))}, nil
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	if len(f.receiveErrs) > 0 {
		err := f.receiveErrs[0]
		f.receiveErrs = f.receiveErrs[1:]
		f.mu.Unlock()
		return nil, err
	}
	if len(f.batches) > 0 {
		b := f.batches[0]
		f.batches = f.batches[1:]
		f.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: b}, nil
	}
	f.mu.Unlock()
	select {
	case f.drained <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	defer func() {
		select {
		case f.dlqSampled <- struct{}{}:
		default:
		}
	}()
	if aws.ToString(in.QueueUrl) != "https://sqs.example/events-dlq" {
		return nil, errors.New("unexpected queue")
	}
	return &sqs.GetQueueAttributesOutput{Attributes: map[string]string{
		string(types.QueueAttributeNameApproximateNumberOfMessages): f.dlqDepth,
	}}, nil
}

type fakeStore struct {
	mu      sync.Mutex
	failFor map[string]bool // SES message ids whose Apply fails
	seen    map[string]bool // dedup key → stored
	applied [][]Record
	// pingFailures is how many Ping calls fail before the store is back.
	pingFailures int
	pings        int
}

func (s *fakeStore) Ping(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pings++
	if s.pingFailures > 0 {
		s.pingFailures--
		return errors.New("db down")
	}
	return nil
}

func (s *fakeStore) Apply(_ context.Context, recs []Record) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFor[recs[0].SESMessageID] {
		return nil, errors.New("db down")
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	var fresh []Record
	for _, r := range recs {
		k := r.SESMessageID + r.EventType + r.EventAt.String() + r.Recipient
		if !s.seen[k] {
			s.seen[k] = true
			fresh = append(fresh, r)
		}
	}
	s.applied = append(s.applied, recs)
	return fresh, nil
}

func msg(handle, body string) types.Message {
	return types.Message{MessageId: aws.String("m-" + handle), ReceiptHandle: aws.String(handle), Body: aws.String(body)}
}

func newTestWorker(t *testing.T, f *fakeSQS, st EventStore) (*Worker, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	w := NewWorker(f, st, "events", "events-dlq", zerolog.Nop())
	w.metrics = newInstruments(mp.Meter("test"))
	w.MinBackoff = time.Millisecond
	w.MaxBackoff = 4 * time.Millisecond
	w.DLQInterval = time.Hour
	return w, reader
}

// runUntilDrained runs the worker until it has consumed every batch and
// the DLQ has been sampled once, then stops it.
func runUntilDrained(t *testing.T, w *Worker, f *fakeSQS) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	for _, ch := range []chan struct{}{f.drained, f.dlqSampled} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not drain the queue")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop on cancel")
	}
}

func newFake() *fakeSQS {
	return &fakeSQS{drained: make(chan struct{}, 1), dlqSampled: make(chan struct{}, 1), dlqDepth: "0"}
}

func TestWorkerDeletesOnlyCommittedMessages(t *testing.T) {
	f := newFake()
	f.urlFailures = 2
	f.receiveErrs = []error{errors.New("throttled")}
	delivery := string(loadFixture(t, "delivery.json"))
	bounce := string(loadFixture(t, "bounce_permanent.json"))
	f.batches = [][]types.Message{
		{
			msg("ok-delivery", delivery),
			msg("poison", `{"eventType":`),
			msg("ignored-open", string(loadFixture(t, "open.json"))),
		},
		{
			msg("redelivered", delivery),
			msg("ok-bounce", bounce),
			msg("db-fails", `{"eventType":"Send","mail":{"messageId":"broken-db","timestamp":"2026-11-03T10:00:00Z","destination":["member@example.net"]},"send":{}}`),
		},
	}
	st := &fakeStore{failFor: map[string]bool{"broken-db": true}}
	w, reader := newTestWorker(t, f, st)

	runUntilDrained(t, w, f)

	want := []string{"ok-delivery", "ignored-open", "redelivered", "ok-bounce"}
	if len(f.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", f.deleted, want)
	}
	for i := range want {
		if f.deleted[i] != want[i] {
			t.Fatalf("deleted %v, want %v", f.deleted, want)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	// The redelivered Delivery is received twice but counted once.
	assertSum(t, rm, "brygge.email.delivery", 1,
		attribute.String("kind", "invoice"), attribute.String("result", StatusAccepted),
		attribute.String("recipient_domain_class", DomainOther))
	assertSum(t, rm, "brygge.email.delivery", 1,
		attribute.String("kind", "invoice"), attribute.String("result", StatusRejected),
		attribute.String("recipient_domain_class", DomainOther))
	assertSum(t, rm, "brygge.ses.events.received", 2, attribute.String("event_type", EventDelivery))
	assertSum(t, rm, "brygge.ses.events.received", 1, attribute.String("event_type", eventTypeInvalid))
	assertSum(t, rm, "brygge.ses.events.received", 1, attribute.String("event_type", eventTypeIgnored))
	assertSum(t, rm, "brygge.ses.events.received", 1, attribute.String("event_type", EventSend))
	assertSum(t, rm, "brygge.ses.events.received", 0, attribute.String("event_type", EventComplaint))
}

// A store failure abandons the rest of the batch and holds off the next
// receive until the store answers a ping, so an outage does not burn
// through maxReceiveCount and push healthy events into the DLQ.
func TestWorkerPausesReceivesWhileStoreIsDown(t *testing.T) {
	f := newFake()
	send := `{"eventType":"Send","mail":{"messageId":"broken-db","timestamp":"2026-11-03T10:00:00Z","destination":["member@example.net"]},"send":{}}`
	f.batches = [][]types.Message{
		{msg("db-fails", send), msg("skipped", string(loadFixture(t, "delivery.json")))},
		{msg("after-recovery", string(loadFixture(t, "bounce_permanent.json")))},
	}
	st := &fakeStore{failFor: map[string]bool{"broken-db": true}, pingFailures: 3}
	w, _ := newTestWorker(t, f, st)

	runUntilDrained(t, w, f)

	if len(f.deleted) != 1 || f.deleted[0] != "after-recovery" {
		t.Fatalf("deleted %v, want only after-recovery", f.deleted)
	}
	if len(st.applied) != 1 || st.applied[0][0].SESMessageID != fixtureMessageID {
		t.Fatalf("applied %d batches; the message after the failure must not be applied", len(st.applied))
	}
	if st.pings != 4 {
		t.Fatalf("pings = %d, want 4 (3 failures, then success)", st.pings)
	}
}

func TestWorkerSamplesDLQDepth(t *testing.T) {
	f := newFake()
	f.dlqDepth = "3"
	w, reader := newTestWorker(t, f, &fakeStore{})

	runUntilDrained(t, w, f)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	m := findMetric(t, rm, "brygge.ses.dlq.messages")
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok || len(g.DataPoints) != 1 || g.DataPoints[0].Value != 3 {
		t.Fatalf("dlq gauge = %+v, want 3", m.Data)
	}
}

func TestWorkerStopsWhileResolvingQueue(t *testing.T) {
	f := newFake()
	f.urlFailures = 1 << 30
	w, _ := newTestWorker(t, f, &fakeStore{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker kept retrying after cancel")
	}
}

func TestNextBackoff(t *testing.T) {
	if got := nextBackoff(time.Second, time.Minute); got != 2*time.Second {
		t.Errorf("got %v", got)
	}
	if got := nextBackoff(40*time.Second, time.Minute); got != time.Minute {
		t.Errorf("got %v, want capped", got)
	}
}

func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("metric %s not recorded", name)
	return metricdata.Metrics{}
}

func assertSum(t *testing.T, rm metricdata.ResourceMetrics, name string, want int64, attrs ...attribute.KeyValue) {
	t.Helper()
	sum, ok := findMetric(t, rm, name).Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s is not an int64 sum", name)
	}
	set := attribute.NewSet(attrs...)
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Equals(&set) {
			if dp.Value != want {
				t.Errorf("%s%v = %d, want %d", name, attrs, dp.Value, want)
			}
			return
		}
	}
	t.Errorf("%s%v: series not pre-created", name, attrs)
}
