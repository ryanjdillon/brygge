package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/brygge-klubb/brygge/internal/auth"
)

// POST /join sends mail to an address the visitor chose, so its per-IP limiter
// fails closed: with Redis unreachable it answers 503 before the handler. The
// other join routes keep the ordinary fail-open limiters and reach theirs.
func TestJoinRateLimitFailsClosed(t *testing.T) {
	r := inertRouter(t, auth.NewSessionService(nil), nil)

	rec := serveAs(r, http.MethodPost, "/api/v1/join", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "rate_limit_unavailable") {
		t.Fatalf("POST /join with Redis down: %d %s, want 503 rate_limit_unavailable", rec.Code, rec.Body.String())
	}

	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/join/status"},
		{http.MethodGet, "/api/v1/join/confirm/some-token"},
		{http.MethodPost, "/api/v1/join/confirm"},
	} {
		if rec := serveAs(r, rt.method, rt.path, ""); rec.Code == http.StatusServiceUnavailable {
			t.Errorf("%s %s answered 503; only POST /join fails closed", rt.method, rt.path)
		}
	}
}

// The join form's address search is public and proxies a third party, so it
// has its own per-IP budget of 30 a minute; the 31st request is refused
// before the handler.
func TestJoinAddressSearchRateLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := inertRouterWithRedis(t, auth.NewSessionService(nil), nil, rdb)

	for i := range 30 {
		if rec := serveAs(r, http.MethodGet, "/api/v1/join/address-search?q=fiktivvegen", ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s, want 200", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := serveAs(r, http.MethodGet, "/api/v1/join/address-search?q=fiktivvegen", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 31: %d, want 429", rec.Code)
	}
	// Its own key: the join status route still answers.
	if rec := serveAs(r, http.MethodGet, "/api/v1/join/status", ""); rec.Code == http.StatusTooManyRequests {
		t.Error("the address search used up the standard budget")
	}
}
