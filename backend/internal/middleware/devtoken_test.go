package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/devtoken"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func serveDevToken(svc *devtoken.Service, authz string) (*httptest.ResponseRecorder, *http.Request) {
	var seen *http.Request
	h := RequireDevToken(svc, devtoken.ScopeDBQuery)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/query", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	// A cookie session must never substitute for the bearer token.
	req.AddCookie(&http.Cookie{Name: "brygge_session", Value: "irrelevant"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, seen
}

func TestRequireDevTokenRejects(t *testing.T) {
	svc := devtoken.NewService(nil) // malformed tokens are rejected before any DB lookup
	for name, authz := range map[string]string{
		"missing":      "",
		"not bearer":   "Basic abc",
		"empty bearer": "Bearer   ",
		"wrong prefix": "Bearer sk_live_whatever",
	} {
		rec, seen := serveDevToken(svc, authz)
		if rec.Code != http.StatusUnauthorized || seen != nil {
			t.Errorf("%s: status %d, handler reached=%v; want 401 and not reached", name, rec.Code, seen != nil)
		}
	}
}

func TestRequireDevTokenInjectsClaims(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := "22222222-2222-2222-2222-" + testutil.RandomHex(6)
	userID := "11111111-1111-1111-1111-" + testutil.RandomHex(6)
	if _, err := db.Exec(ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Synthetic Club')", clubID, "s-"+testutil.RandomHex(4)); err != nil {
		t.Fatalf("seed club: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO users (id, club_id, email) VALUES ($1, $2, $3)", userID, clubID, testutil.RandomHex(4)+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	svc := devtoken.NewService(db)
	plain, tok, err := svc.Mint(ctx, clubID, userID, "agent", 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	rec, seen := serveDevToken(svc, "Bearer "+plain)
	if rec.Code != http.StatusOK || seen == nil {
		t.Fatalf("valid token: status %d", rec.Code)
	}
	claims := GetClaims(seen.Context())
	if claims == nil || claims.ClubID != clubID || claims.UserID != userID || len(claims.Roles) != 0 {
		t.Errorf("claims = %+v; want club/creator and no roles", claims)
	}
	if got := GetDevTokenID(seen.Context()); got != tok.ID {
		t.Errorf("token id in ctx = %q, want %q", got, tok.ID)
	}
	if got := DevTokenKey(seen); got != "devtoken:"+tok.ID {
		t.Errorf("rate-limit key = %q", got)
	}

	if rec, _ := serveDevToken(svc, "Bearer "+plain+"x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered token: status %d, want 401", rec.Code)
	}
}
