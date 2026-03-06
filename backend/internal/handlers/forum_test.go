package handlers

import (
	"testing"
)

func TestExtractLocalpart(t *testing.T) {
	tests := []struct {
		name     string
		matrixID string
		want     string
	}{
		{"standard matrix ID", "@alice:example.com", "alice"},
		{"matrix ID with subdomain", "@bob:matrix.example.org", "bob"},
		{"no colon", "@charlie", "charlie"},
		{"empty string", "", ""},
		{"single character", "@", "@"},
		{"just sigil no colon", "@x", "x"},
		{"colon immediately after sigil", "@:server.com", ""},
		{"dots in localpart", "@user.name:server.com", "user.name"},
		{"hyphens in localpart", "@my-user:server.com", "my-user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLocalpart(tt.matrixID)
			if got != tt.want {
				t.Errorf("extractLocalpart(%q) = %q, want %q", tt.matrixID, got, tt.want)
			}
		})
	}
}

func TestExtractDisplayName(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		want   string
	}{
		{"standard matrix ID", "@alice:example.com", "alice"},
		{"no colon", "@bob", "bob"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDisplayName(tt.userID)
			if got != tt.want {
				t.Errorf("extractDisplayName(%q) = %q, want %q", tt.userID, got, tt.want)
			}
		})
	}
}
