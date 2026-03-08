package audit

import (
	"testing"
)

func TestStrPtr(t *testing.T) {
	tests := []struct {
		input string
		isNil bool
	}{
		{"", true},
		{"value", false},
	}

	for _, tt := range tests {
		result := strPtr(tt.input)
		if tt.isNil && result != nil {
			t.Errorf("strPtr(%q) = %v, want nil", tt.input, result)
		}
		if !tt.isNil && (result == nil || *result != tt.input) {
			t.Errorf("strPtr(%q) = %v, want %q", tt.input, result, tt.input)
		}
	}
}

func TestNewService(t *testing.T) {
	// Service should handle nil DB gracefully
	svc := NewService(nil, testLogger())
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestServiceLogNilDB(t *testing.T) {
	svc := NewService(nil, testLogger())
	// Should not panic
	svc.Log(t.Context(), Entry{
		Action:   ActionLoginSuccess,
		Resource: "session",
	})
}

func TestServiceLogAction(t *testing.T) {
	svc := NewService(nil, testLogger())
	// Should not panic with nil DB
	svc.LogAction(t.Context(), "club-1", "user-1", "127.0.0.1", ActionLoginSuccess, "session", "", nil)
}

func TestActionConstants(t *testing.T) {
	// Verify key constants are non-empty
	constants := []string{
		ActionLoginSuccess,
		ActionLoginFailed,
		ActionTokenRevoked,
		ActionUserRoleUpdated,
		ActionUserDeleted,
		ActionSlipCreated,
		ActionBookingConfirmed,
		ActionGDPRExportRequested,
	}
	for _, c := range constants {
		if c == "" {
			t.Error("found empty action constant")
		}
	}
}
