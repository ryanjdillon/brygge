package mail

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeQueue mimics Stalwart 0.15's GET /api/queue/messages: items are full
// message objects only when `values` has a non-empty value, bare ids
// otherwise, and `total` always counts the whole queue.
type fakeQueue struct {
	status  int
	created []string // oldest first
}

func (f *fakeQueue) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/queue/messages" {
			http.NotFound(w, r)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = fmt.Fprint(w, `{"type":"about:blank","status":401,"title":"Unauthorized"}`)
			return
		}
		if r.URL.Query().Get("limit") != "1" {
			t.Errorf("limit = %q, want 1", r.URL.Query().Get("limit"))
		}
		items := "[]"
		if len(f.created) > 0 {
			if r.URL.Query().Get("values") != "" {
				items = fmt.Sprintf(`[{"id":1,"return_path":"bounce@example.invalid","recipients":[],"created":%q,"size":1200,"blob_hash":"x"}]`, f.created[0])
			} else {
				items = "[1]"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"items":%s,"total":%d,"status":true}}`, items, len(f.created))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type queueGauges struct {
	messages, lastSuccess        int64
	oldestAge                    float64
	hasMessages, hasAge, hasLast bool
}

func collectQueue(t *testing.T, reader *sdkmetric.ManualReader) queueGauges {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var g queueGauges
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "brygge.mail.queue.messages":
				dps := m.Data.(metricdata.Gauge[int64]).DataPoints
				if len(dps) == 1 {
					g.messages, g.hasMessages = dps[0].Value, true
				}
			case "brygge.mail.queue.oldest_age":
				dps := m.Data.(metricdata.Gauge[float64]).DataPoints
				if len(dps) == 1 {
					g.oldestAge, g.hasAge = dps[0].Value, true
				}
			case "brygge.mail.queue.last_success":
				dps := m.Data.(metricdata.Gauge[int64]).DataPoints
				if len(dps) == 1 {
					g.lastSuccess, g.hasLast = dps[0].Value, true
				}
			}
		}
	}
	return g
}

func newTestMonitor(t *testing.T, url string, now time.Time) (*QueueMonitor, *sdkmetric.ManualReader) {
	t.Helper()
	m := NewQueueMonitor(NewAdminClient(url, "admin", "pw", "", zerolog.Nop()), zerolog.Nop())
	m.now = func() time.Time { return now }
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if _, err := m.registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	return m, reader
}

var queueTestNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func TestQueueMonitor_EmptyQueue(t *testing.T) {
	srv := (&fakeQueue{}).server(t)
	m, reader := newTestMonitor(t, srv.URL, queueTestNow)
	m.poll(context.Background())

	g := collectQueue(t, reader)
	if !g.hasMessages || g.messages != 0 {
		t.Errorf("messages = %d (present %v), want 0", g.messages, g.hasMessages)
	}
	if !g.hasAge || g.oldestAge != 0 {
		t.Errorf("oldest_age = %v (present %v), want 0", g.oldestAge, g.hasAge)
	}
	if g.lastSuccess != queueTestNow.Unix() {
		t.Errorf("last_success = %d, want %d", g.lastSuccess, queueTestNow.Unix())
	}
}

func TestQueueMonitor_PopulatedQueue(t *testing.T) {
	srv := (&fakeQueue{created: []string{
		"2026-10-25T09:00:00Z",
		"2026-10-25T11:30:00Z",
		"2026-10-25T11:59:00Z",
	}}).server(t)
	m, reader := newTestMonitor(t, srv.URL, queueTestNow)
	m.poll(context.Background())

	g := collectQueue(t, reader)
	if g.messages != 3 {
		t.Errorf("messages = %d, want 3", g.messages)
	}
	if want := (3 * time.Hour).Seconds(); g.oldestAge != want {
		t.Errorf("oldest_age = %v, want %v", g.oldestAge, want)
	}

	// The age is computed at observation time from the cached creation
	// time, so it keeps growing between polls.
	m.now = func() time.Time { return queueTestNow.Add(time.Minute) }
	if g := collectQueue(t, reader); g.oldestAge != (3*time.Hour + time.Minute).Seconds() {
		t.Errorf("oldest_age after a minute = %v", g.oldestAge)
	}
}

func TestQueueMonitor_Unauthorized(t *testing.T) {
	srv := (&fakeQueue{status: http.StatusUnauthorized}).server(t)
	m, reader := newTestMonitor(t, srv.URL, queueTestNow)
	m.poll(context.Background())

	g := collectQueue(t, reader)
	if g.hasMessages || g.hasAge {
		t.Errorf("queue gauges observed without a successful poll: %+v", g)
	}
	if !g.hasLast || g.lastSuccess != 0 {
		t.Errorf("last_success = %d (present %v), want 0", g.lastSuccess, g.hasLast)
	}
}

func TestQueueMonitor_ErrorKeepsLastSnapshot(t *testing.T) {
	fq := &fakeQueue{created: []string{"2026-10-25T11:00:00Z"}}
	srv := fq.server(t)
	m, reader := newTestMonitor(t, srv.URL, queueTestNow)
	m.poll(context.Background())

	fq.status = http.StatusUnauthorized
	m.now = func() time.Time { return queueTestNow.Add(5 * time.Minute) }
	m.poll(context.Background())

	g := collectQueue(t, reader)
	if g.messages != 1 {
		t.Errorf("messages = %d, want the cached 1", g.messages)
	}
	if g.lastSuccess != queueTestNow.Unix() {
		t.Errorf("last_success = %d, want the first poll's %d", g.lastSuccess, queueTestNow.Unix())
	}
}

func TestQueueMonitor_StaleSnapshotGoesAbsent(t *testing.T) {
	fq := &fakeQueue{created: []string{"2026-10-25T11:00:00Z"}}
	srv := fq.server(t)
	m, reader := newTestMonitor(t, srv.URL, queueTestNow)
	m.poll(context.Background())

	fq.status = http.StatusUnauthorized
	m.now = func() time.Time { return queueTestNow.Add(queueStaleAfter + time.Second) }
	m.poll(context.Background())

	g := collectQueue(t, reader)
	if g.hasMessages || g.hasAge {
		t.Errorf("stale snapshot still observed: %+v", g)
	}
	if !g.hasLast || g.lastSuccess != queueTestNow.Unix() {
		t.Errorf("last_success = %d (present %v), want %d", g.lastSuccess, g.hasLast, queueTestNow.Unix())
	}

	// A later successful poll brings the gauges back.
	fq.status = 0
	m.poll(context.Background())
	if g := collectQueue(t, reader); !g.hasMessages || g.messages != 1 {
		t.Errorf("messages after recovery = %d (present %v), want 1", g.messages, g.hasMessages)
	}
}

func TestQueueSummary_RejectsTotalWithoutItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"items":[],"total":2,"status":true}}`)
	}))
	t.Cleanup(srv.Close)
	c := NewAdminClient(srv.URL, "admin", "pw", "", zerolog.Nop())
	if _, err := c.QueueSummary(context.Background()); err == nil {
		t.Fatal("want an error when total is non-zero but no item is returned")
	}
}

func TestQueueSummary_RejectsIDOnlyItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"items":[42],"total":1,"status":true}}`)
	}))
	t.Cleanup(srv.Close)
	c := NewAdminClient(srv.URL, "admin", "pw", "", zerolog.Nop())
	if _, err := c.QueueSummary(context.Background()); err == nil {
		t.Fatal("want an error when items carry no creation time")
	}
}

func TestQueueMonitor_RunStopsOnCancel(t *testing.T) {
	srv := (&fakeQueue{}).server(t)
	m, _ := newTestMonitor(t, srv.URL, queueTestNow)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx, time.Hour)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
