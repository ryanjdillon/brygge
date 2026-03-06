package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestJSONResponse(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"name": "test"}
	JSON(rr, 200, data)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var got map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got["name"] != "test" {
		t.Errorf("body name = %q, want %q", got["name"], "test")
	}
}

func TestErrorResponse(t *testing.T) {
	rr := httptest.NewRecorder()

	Error(rr, 400, "bad request")

	if rr.Code != 400 {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var got map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got["error"] != "bad request" {
		t.Errorf("body error = %q, want %q", got["error"], "bad request")
	}
}

func TestJSONNilData(t *testing.T) {
	rr := httptest.NewRecorder()

	JSON(rr, 200, nil)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	// json.Encoder writes "null\n" for nil
	if body != "null\n" {
		t.Errorf("body = %q, want %q", body, "null\n")
	}
}
