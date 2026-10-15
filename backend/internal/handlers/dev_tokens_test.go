package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/devtoken"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestDevTokenHandlers(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := "22222222-2222-2222-2222-" + testutil.RandomHex(6)
	userID := "11111111-1111-1111-1111-" + testutil.RandomHex(6)
	mustExec(t, db, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Synthetic Club')", clubID, "s-"+testutil.RandomHex(4))
	mustExec(t, db, ctx, "INSERT INTO users (id, club_id, email) VALUES ($1, $2, $3)", userID, clubID, testutil.RandomHex(4)+"@example.test")
	claims := &auth.Claims{UserID: userID, ClubID: clubID, Roles: []string{"admin"}}

	do := func(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Method(method, "/dev/tokens", h)
		r.Method(method, "/dev/tokens/{tokenID}", h)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	svc := devtoken.NewService(db)

	disabled := NewDevTokenHandler(svc, nil, false, zerolog.Nop())
	if rec := do(disabled.HandleCreate, http.MethodPost, "/dev/tokens", `{"name":"x"}`); rec.Code != http.StatusConflict {
		t.Fatalf("mint while disabled: status %d, want 409", rec.Code)
	}

	h := NewDevTokenHandler(svc, nil, true, zerolog.Nop())
	if rec := do(h.HandleCreate, http.MethodPost, "/dev/tokens", `{"name":"x","ttl_hours":1000}`); rec.Code != http.StatusBadRequest {
		t.Errorf("over-max ttl: status %d, want 400", rec.Code)
	}

	rec := do(h.HandleCreate, http.MethodPost, "/dev/tokens", `{"name":"agent","ttl_hours":2}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: status %d (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || !strings.HasPrefix(created.Token, devtoken.Prefix) {
		t.Fatalf("mint response %s: %v", rec.Body.String(), err)
	}

	list := do(h.HandleList, http.MethodGet, "/dev/tokens", "")
	if list.Code != http.StatusOK || bytes.Contains(list.Body.Bytes(), []byte(created.Token)) {
		t.Fatalf("list status %d leaks plaintext=%v", list.Code, bytes.Contains(list.Body.Bytes(), []byte(created.Token)))
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Errorf("list should report enabled: %s", list.Body.String())
	}

	if rec := do(h.HandleRevoke, http.MethodDelete, "/dev/tokens/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Errorf("revoke: status %d", rec.Code)
	}
	if rec := do(h.HandleRevoke, http.MethodDelete, "/dev/tokens/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("double revoke: status %d, want 404", rec.Code)
	}
}
