package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
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

	r.Route("/waiting-list", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Post("/join", h.HandleJoinWaitingList)
		r.Get("/me", h.HandleGetMyPosition)
		r.Post("/withdraw", h.HandleWithdraw)
		r.Post("/{entryID}/accept", h.HandleAcceptOffer)
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
