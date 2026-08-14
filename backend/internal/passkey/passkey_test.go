package passkey

import (
	"testing"

	"github.com/brygge-klubb/brygge/internal/config"
)

func TestRPConfig(t *testing.T) {
	cases := []struct {
		name        string
		frontendURL string
		clubName    string
		wantRPID    string
		wantOrigin  string
		wantDisplay string
	}{
		{"prod", "https://klokkarvikbaatlag.no", "Klokkarvik Båtlag", "klokkarvikbaatlag.no", "https://klokkarvikbaatlag.no", "Klokkarvik Båtlag"},
		{"dev", "http://localhost:5173", "", "localhost", "http://localhost:5173", "Brygge"},
		{"trailing slash", "https://example.org/", "Club", "example.org", "https://example.org", "Club"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc, err := rpConfig(&config.Config{FrontendURL: tc.frontendURL, ClubName: tc.clubName})
			if err != nil {
				t.Fatalf("rpConfig: %v", err)
			}
			if wc.RPID != tc.wantRPID {
				t.Errorf("RPID = %q, want %q", wc.RPID, tc.wantRPID)
			}
			if len(wc.RPOrigins) != 1 || wc.RPOrigins[0] != tc.wantOrigin {
				t.Errorf("RPOrigins = %v, want [%q]", wc.RPOrigins, tc.wantOrigin)
			}
			if wc.RPDisplayName != tc.wantDisplay {
				t.Errorf("RPDisplayName = %q, want %q", wc.RPDisplayName, tc.wantDisplay)
			}
		})
	}
}

func TestRPConfigRejectsBadURL(t *testing.T) {
	if _, err := rpConfig(&config.Config{FrontendURL: "not a url"}); err == nil {
		t.Error("expected error for FRONTEND_URL without a host")
	}
}

func TestNewBuildsRelyingParty(t *testing.T) {
	w, err := New(&config.Config{FrontendURL: "https://klokkarvikbaatlag.no", ClubName: "Klokkarvik"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w == nil {
		t.Fatal("New returned nil relying party")
	}
}

func TestNewHandleIsRandomAndSized(t *testing.T) {
	a, err := newHandle()
	if err != nil {
		t.Fatalf("newHandle: %v", err)
	}
	b, err := newHandle()
	if err != nil {
		t.Fatalf("newHandle: %v", err)
	}
	if len(a) != handleBytes {
		t.Errorf("handle length = %d, want %d", len(a), handleBytes)
	}
	if string(a) == string(b) {
		t.Error("two handles collided — not random")
	}
}
