package handlers

import (
	"strings"
	"testing"
)

func TestGenerateRecoveryCodesShape(t *testing.T) {
	codes, err := generateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("generateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}
	seen := make(map[string]bool, 10)
	for i, c := range codes {
		if len(c) != 9 {
			t.Errorf("code %d %q: length %d, want 9", i, c, len(c))
		}
		if c[4] != '-' {
			t.Errorf("code %d %q: missing dash at position 4", i, c)
		}
		// Each glyph must be in the safe alphabet (no 0/O/1/I/L).
		for _, r := range strings.ReplaceAll(c, "-", "") {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Errorf("code %d %q: char %q not in safe alphabet", i, c, r)
			}
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ABCD-EFGH", "ABCD-EFGH"},
		{"abcd-efgh", "ABCD-EFGH"},
		{"ABCDEFGH", "ABCD-EFGH"},
		{" ABCD-EFGH ", "ABCD-EFGH"},
		{"abcd efgh", "ABCD-EFGH"},
		{"ABCD-EFG", ""},  // too short
		{"ABCD-EFGHI", ""}, // too long
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeRecoveryCode(tt.in)
		if got != tt.want {
			t.Errorf("normalizeRecoveryCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
