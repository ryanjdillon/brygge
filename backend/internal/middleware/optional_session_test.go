package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// An applicant-only session on an optional-session route is served as an
// anonymous visitor: the handler sees no claims. Any other session keeps
// its claims.
func TestOptionalSessionAuthDropsApplicantClaims(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	sessions := auth.NewSessionService(pool)

	for _, tc := range []struct {
		roles      []string
		wantClaims bool
	}{
		{[]string{"applicant"}, false},
		{[]string{"member"}, true},
		{[]string{"applicant", "board"}, true},
	} {
		userID, _ := testutil.SeedUser(t, pool, clubID, tc.roles)
		sid, err := sessions.CreateSession(ctx, userID, clubID, "198.51.100.4:5555", "test-agent")
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		var sawClaims, called bool
		h := OptionalSessionAuth(sessions)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			called = true
			sawClaims = GetClaims(r.Context()) != nil
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/harbor/layout", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
		h.ServeHTTP(httptest.NewRecorder(), req)
		if !called {
			t.Fatalf("%v: handler not reached", tc.roles)
		}
		if sawClaims != tc.wantClaims {
			t.Errorf("%v: handler saw claims = %v, want %v", tc.roles, sawClaims, tc.wantClaims)
		}
	}
}
