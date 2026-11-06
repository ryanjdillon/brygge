package main

import (
	"net/http"
	"strings"
	"testing"

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
