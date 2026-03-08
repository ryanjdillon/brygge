package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/config"
)

func TestHandleGetFeatures(t *testing.T) {
	cfg := &config.Config{
		Features: config.Features{
			Bookings:       true,
			Projects:       false,
			Calendar:       true,
			Commerce:       false,
			Communications: true,
		},
	}

	h := NewFeaturesHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/features", nil)
	rec := httptest.NewRecorder()

	h.HandleGetFeatures(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	expected := map[string]bool{
		"bookings":       true,
		"projects":       false,
		"calendar":       true,
		"commerce":       false,
		"communications": true,
	}

	for key, want := range expected {
		got, ok := result[key]
		if !ok {
			t.Errorf("missing key %q in response", key)
			continue
		}
		if got != want {
			t.Errorf("feature %q: got %v, want %v", key, got, want)
		}
	}
}

func TestHandleGetFeaturesAllEnabled(t *testing.T) {
	cfg := &config.Config{
		Features: config.Features{
			Bookings:       true,
			Projects:       true,
			Calendar:       true,
			Commerce:       true,
			Communications: true,
		},
	}

	h := NewFeaturesHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/features", nil)
	rec := httptest.NewRecorder()

	h.HandleGetFeatures(rec, req)

	var result map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for key, val := range result {
		if !val {
			t.Errorf("expected feature %q to be true", key)
		}
	}
}
