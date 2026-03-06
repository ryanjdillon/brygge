package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

func newTestWaitingListHandler(t *testing.T) *WaitingListHandler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := testConfig()
	log := zerolog.Nop()
	return NewWaitingListHandler(nil, rdb, cfg, log)
}

func waitingListRouter(t *testing.T, h *WaitingListHandler) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	jwtSvc := testJWTService()

	r.Route("/waiting-list", func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/join", h.HandleJoinWaitingList)
		r.Get("/me", h.HandleGetMyPosition)
		r.Post("/withdraw", h.HandleWithdraw)
		r.Post("/{entryID}/accept", h.HandleAcceptOffer)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole("styre"))
			r.Get("/", h.HandleListWaitingList)
			r.Post("/{entryID}/offer", h.HandleOfferSlip)
			r.Put("/{entryID}/position", h.HandleReorderEntry)
		})
	})

	return r
}

func TestHandleJoinWaitingListUnauthorized(t *testing.T) {
	h := newTestWaitingListHandler(t)
	r := waitingListRouter(t, h)

	req := httptest.NewRequest(http.MethodPost, "/waiting-list/join", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleGetMyPositionUnauthorized(t *testing.T) {
	h := newTestWaitingListHandler(t)
	r := waitingListRouter(t, h)

	req := httptest.NewRequest(http.MethodGet, "/waiting-list/me", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleListWaitingListRequiresStyre(t *testing.T) {
	h := newTestWaitingListHandler(t)
	r := waitingListRouter(t, h)

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/waiting-list/", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "insufficient") {
		t.Errorf("expected insufficient permissions error, got %q", resp.Error)
	}
}

func TestHandleOfferSlipRequiresStyre(t *testing.T) {
	h := newTestWaitingListHandler(t)
	r := waitingListRouter(t, h)

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/waiting-list/entry-123/offer", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleReorderEntryRequiresStyre(t *testing.T) {
	h := newTestWaitingListHandler(t)
	r := waitingListRouter(t, h)

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodPut, "/waiting-list/entry-123/position", strings.NewReader(`{"new_position":2}`))
	req.Header.Set("Authorization", authHeader(token))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}
