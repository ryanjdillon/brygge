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

func newTestBookingsHandler(t *testing.T) *BookingsHandler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := testConfig()
	log := zerolog.Nop()
	return NewBookingsHandler(nil, rdb, cfg, log)
}

func TestHandleCreateBookingMissingFields(t *testing.T) {
	h := newTestBookingsHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"missing resource_id", `{"start_date":"2025-07-01","end_date":"2025-07-05"}`},
		{"missing start_date", `{"resource_id":"res-1","end_date":"2025-07-05"}`},
		{"missing end_date", `{"resource_id":"res-1","start_date":"2025-07-01"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleCreateBooking(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Error == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

func TestHandleCreateBookingInvalidDates(t *testing.T) {
	h := newTestBookingsHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"bad start_date format", `{"resource_id":"r-1","start_date":"not-a-date","end_date":"2025-07-05"}`},
		{"bad end_date format", `{"resource_id":"r-1","start_date":"2025-07-01","end_date":"nope"}`},
		{"end before start", `{"resource_id":"r-1","start_date":"2025-07-10","end_date":"2025-07-01"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.HandleCreateBooking(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleCancelBookingUnauthorized(t *testing.T) {
	h := newTestBookingsHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/bookings/{bookingID}/cancel", h.HandleCancelBooking)
	})

	req := httptest.NewRequest(http.MethodPost, "/bookings/b-123/cancel", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleConfirmBookingRequiresStyre(t *testing.T) {
	h := newTestBookingsHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Use(middleware.RequireRole("board", "harbor_master"))
		r.Post("/bookings/{bookingID}/confirm", h.HandleConfirmBooking)
	})

	token := generateTestToken("user-1", "club-1", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/bookings/b-456/confirm", nil)
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

