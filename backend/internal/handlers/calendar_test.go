package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestIcsEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"no special chars", "Hello World", "Hello World"},
		{"comma", "one, two, three", `one\, two\, three`},
		{"semicolon", "a;b;c", `a\;b\;c`},
		{"backslash", `path\to\file`, `path\\to\\file`},
		{"newline", "line1\nline2", `line1\nline2`},
		{"all special chars", "a\\b;c,d\ne", `a\\b\;c\,d\ne`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := icsEscape(tt.input)
			if got != tt.want {
				t.Errorf("icsEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHandleCreateEventMissingFields(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/calendar", h.HandleCreateEvent)
	})

	token := generateTestToken("user-1", "club-1", []string{"styre"})

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"missing all required fields",
			`{}`,
			"title, start_time, and end_time are required",
		},
		{
			"missing start_time and end_time",
			`{"title":"Test Event"}`,
			"title, start_time, and end_time are required",
		},
		{
			"missing title",
			`{"start_time":"2025-06-01T10:00:00Z","end_time":"2025-06-01T12:00:00Z"}`,
			"title, start_time, and end_time are required",
		},
		{
			"missing end_time",
			`{"title":"Test","start_time":"2025-06-01T10:00:00Z"}`,
			"title, start_time, and end_time are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/calendar", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if !strings.Contains(resp.Error, tt.want) {
				t.Errorf("error = %q, want to contain %q", resp.Error, tt.want)
			}
		})
	}
}

func TestHandleCreateEventInvalidTimestamps(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/calendar", h.HandleCreateEvent)
	})

	token := generateTestToken("user-1", "club-1", []string{"styre"})

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"invalid start_time",
			`{"title":"Test","start_time":"not-a-date","end_time":"2025-06-01T12:00:00Z"}`,
			"invalid start_time format",
		},
		{
			"invalid end_time",
			`{"title":"Test","start_time":"2025-06-01T10:00:00Z","end_time":"not-a-date"}`,
			"invalid end_time format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/calendar", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if !strings.Contains(resp.Error, tt.want) {
				t.Errorf("error = %q, want to contain %q", resp.Error, tt.want)
			}
		})
	}
}

func TestHandleCreateEventEndBeforeStart(t *testing.T) {
	h := newTestCalendarHandler(t)

	r := chi.NewRouter()
	jwtSvc := testJWTService()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(jwtSvc))
		r.Post("/calendar", h.HandleCreateEvent)
	})

	token := generateTestToken("user-1", "club-1", []string{"styre"})

	body := `{"title":"Test","start_time":"2025-06-01T14:00:00Z","end_time":"2025-06-01T10:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/calendar", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "end_time must be after start_time") {
		t.Errorf("error = %q, want to contain 'end_time must be after start_time'", resp.Error)
	}
}
