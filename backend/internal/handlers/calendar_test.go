package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

func newTestCalendarHandler(t *testing.T) *CalendarHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewCalendarHandler(nil, cfg, log)
}

func TestHandleCreateEventUnauthorized(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Use(middleware.RequireRole("styre"))
		r.Post("/calendar", h.HandleCreateEvent)
	})

	req := httptest.NewRequest(http.MethodPost, "/calendar", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleCreateEventForbiddenForMember(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Use(middleware.RequireRole("styre"))
		r.Post("/calendar", h.HandleCreateEvent)
	})

	token := generateTestToken("user-1", "club-1", []string{"member"})
	req := httptest.NewRequest(http.MethodPost, "/calendar", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestHandleListPublicEventsInvalidDateFormat(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	r.Get("/calendar", h.HandleListPublicEvents)

	tests := []struct {
		name  string
		query string
	}{
		{"bad start date", "?start=not-a-date"},
		{"bad end date", "?end=13-2025-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/calendar"+tt.query, nil)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}
		})
	}
}
