package mail

import (
	"strings"
	"testing"
)

func TestPrincipalSlugIsStable(t *testing.T) {
	id := "ed5aa164-9977-41e9-b63f-f0a5c832e435"
	want := "buedy5aa1649977"
	got := principalSlug(id)
	if got != "bu"+"ed5aa1649977" {
		t.Errorf("principalSlug(%q) = %q, want %q", id, got, want)
	}
}

func TestPrincipalSlugIsAlphanumericOnly(t *testing.T) {
	got := principalSlug("ed5aa164-9977-41e9-b63f-f0a5c832e435")
	for _, c := range got {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			t.Errorf("slug contains non-alphanumeric: %q", c)
		}
	}
}

func TestPrincipalSlugHandlesShortInput(t *testing.T) {
	if got := principalSlug("abc"); got != "buabc" {
		t.Errorf("short input: got %q", got)
	}
}

func TestPrincipalSlugIsLowercase(t *testing.T) {
	got := principalSlug("ABCDEF12-3456")
	if got != strings.ToLower(got) {
		t.Errorf("slug not lowercased: %q", got)
	}
}

func TestGenerateServicePasswordIsNonEmptyAndURLSafe(t *testing.T) {
	a, err := generateServicePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 30 {
		t.Errorf("password too short: %d", len(a))
	}
	for _, c := range a {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			t.Errorf("non-URL-safe char in password: %q", c)
		}
	}
	b, _ := generateServicePassword()
	if a == b {
		t.Errorf("two consecutive passwords identical — RNG broken?")
	}
}
