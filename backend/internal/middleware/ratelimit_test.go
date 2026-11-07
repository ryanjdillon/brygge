package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

func testRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return rdb, mr
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRateLimitAllowsWithinLimit(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 5, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}

// Distinct limiters (e.g. the strict auth limiter and the 30/min browse
// limiter) must keep independent per-IP budgets — browsing the public site
// must not burn the auth budget for the same IP.
func TestRateLimitLimitersAreIsolated(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	browse := NewRateLimiter(rdb, log, 30, time.Minute, IPKey).Handler(okHandler())
	strict := NewRateLimiter(rdb, log, 2, time.Minute, IPKey).Handler(okHandler())

	// Hammer the browse limiter well past the strict limit (same default IP).
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		browse.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/club", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("browse request %d unexpectedly blocked: %d", i+1, rec.Code)
		}
	}

	// The strict limiter must still allow its own full budget.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		strict.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/verify", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("strict request %d should be independent of browse traffic; got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimitBlocks429OnExceed(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 3, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// 4th request should be blocked
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimitRetryAfterHeader(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 1, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	// First request OK
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Second request blocked
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["error"] != "too many requests" {
		t.Errorf("expected error 'too many requests', got %v", body["error"])
	}
	if body["retry_after"] == nil {
		t.Error("expected retry_after in response body")
	}
}

func TestRateLimitHeaders(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 5, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("expected X-RateLimit-Limit 5, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Errorf("expected X-RateLimit-Remaining 4, got %s", rec.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestRateLimitResetsAfterWindow(t *testing.T) {
	rdb, mr := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 2, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	// Exhaust limit
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Should be blocked
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	// Fast-forward past window
	mr.FastForward(61 * time.Second)

	// Should be allowed again
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after window reset, got %d", rec.Code)
	}
}

func TestRateLimitDifferentIPsIndependent(t *testing.T) {
	rdb, _ := testRedis(t)
	log := zerolog.Nop()

	rl := NewRateLimiter(rdb, log, 1, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	// IP 1 — uses limit
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "1.1.1.1:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("IP1: expected 200, got %d", rec1.Code)
	}

	// IP 2 — should have its own limit
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "2.2.2.2:5678"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("IP2: expected 200, got %d", rec2.Code)
	}

	// IP 1 — should be blocked
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.RemoteAddr = "1.1.1.1:1234"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second: expected 429, got %d", rec3.Code)
	}
}

// A limiter is fail-open by default: with Redis gone the request goes
// through. FailClosed turns the same outage into a 503 that never reaches the
// handler, for routes where every request costs something real.
func TestRateLimitRedisErrorFailOpenAndClosed(t *testing.T) {
	rdb, mr := testRedis(t)
	log := zerolog.Nop()
	mr.Close()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	NewRateLimiter(rdb, log, 5, time.Minute, IPKey).Handler(next).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", nil))
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("fail-open limiter: got %d, reached=%v; want 200 and the handler reached", rec.Code, reached)
	}

	reached = false
	rec = httptest.NewRecorder()
	NewRateLimiter(rdb, log, 5, time.Minute, IPKey).FailClosed().Handler(next).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed limiter: got %d, want 503", rec.Code)
	}
	if reached {
		t.Fatal("fail-closed limiter let the request through on a Redis error")
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body["error"] != "rate_limit_unavailable" {
		t.Fatalf("fail-closed body = %v (err %v), want error rate_limit_unavailable", body, err)
	}
}

func TestRateLimitLogRedactsPathToken(t *testing.T) {
	rdb, _ := testRedis(t)
	var buf bytes.Buffer
	rl := NewRateLimiter(rdb, zerolog.New(&buf), 1, time.Minute, IPKey)
	handler := rl.Handler(okHandler())

	for i := 0; i < 2; i++ {
		handler.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/api/v1/betal/SECRET-TOKEN", nil))
	}

	out := buf.String()
	if strings.Contains(out, "SECRET-TOKEN") {
		t.Fatalf("rate limit log holds the path token: %s", out)
	}
	if !strings.Contains(out, "/api/v1/betal/REDACTED") {
		t.Fatalf("rate limit log missing the redacted path: %q", out)
	}
}
