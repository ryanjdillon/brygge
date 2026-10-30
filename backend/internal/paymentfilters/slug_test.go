package paymentfilters

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Slipping", "slipping"},
		{"Gjesteplass", "gjesteplass"},
		{"Båt og bryggeøl", "bat-og-bryggeol"},
		{"Ærlig Åpen Ørret", "aerlig-apen-orret"},
		{"Müller & Söhne", "muller-sohne"},
		{"  --Kiosk / Café!!  ", "kiosk-caf"},
		{"2026 sesong", "2026-sesong"},
		{"", "group"},
		{"!!!", "group"},
		{"日本語", "group"},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)},
		{strings.Repeat("a", 39) + " b", strings.Repeat("a", 39)},
	}
	for _, tc := range tests {
		got := Slugify(tc.in)
		if got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if len(got) > slugMaxLen {
			t.Errorf("Slugify(%q) = %q is longer than %d", tc.in, got, slugMaxLen)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := map[string]bool{"slipping": true, "slipping-2": true}
	if got := uniqueSlug("slipping", taken); got != "slipping-3" {
		t.Errorf("uniqueSlug = %q, want slipping-3", got)
	}
	if got := uniqueSlug("kiosk", taken); got != "kiosk" {
		t.Errorf("uniqueSlug of a free slug = %q, want kiosk", got)
	}
	// The suffix still fits the limit when the base is at it.
	long := strings.Repeat("a", 40)
	got := uniqueSlug(long, map[string]bool{long: true})
	if len(got) > slugMaxLen || !strings.HasSuffix(got, "-2") {
		t.Errorf("uniqueSlug of a 40-character slug = %q", got)
	}
}

func TestReservedSlugs(t *testing.T) {
	for _, name := range []string{"All", "Alle", "Harbor", "Faktura", "Other", "faktura"} {
		if !reservedSlugs[Slugify(name)] {
			t.Errorf("%q should slugify to a reserved slug", name)
		}
	}
	if reservedSlugs[Slugify("Slipping")] {
		t.Error("slipping must not be reserved")
	}
}

func TestNormalizeName(t *testing.T) {
	if got, err := NormalizeName("  Slipping "); err != nil || got != "Slipping" {
		t.Errorf("NormalizeName = %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 41), "two\nlines", "tab\there"} {
		if _, err := NormalizeName(bad); CodeOf(err) != CodeInvalidName {
			t.Errorf("NormalizeName(%q) = %v, want %s", bad, err, CodeInvalidName)
		}
	}
	// 40 characters of multi-byte text is allowed: the limit counts characters.
	if _, err := NormalizeName(strings.Repeat("å", 40)); err != nil {
		t.Errorf("40 multi-byte characters refused: %v", err)
	}
}

// The name of the no-filter chip is taken in every locale.
func TestReservedLabels(t *testing.T) {
	for _, name := range []string{"all", "ALL", "Alle", "alle", "Tous", "Tutti", "Wszystkie"} {
		if !isReservedLabel(name) {
			t.Errorf("%q should be a reserved label", name)
		}
	}
	for _, name := range []string{"Allergi", "Slipping", ""} {
		if isReservedLabel(name) {
			t.Errorf("%q must not be reserved", name)
		}
	}
}

func TestNormalizeNameUnicode(t *testing.T) {
	// The same word typed with a combining accent is stored as one composed name.
	decomposed := "Cafe\u0301"
	if got, err := NormalizeName(decomposed); err != nil || got != "Caf\u00e9" {
		t.Errorf("NormalizeName(%q) = %q, %v, want the NFC form", decomposed, got, err)
	}
	// Invisible characters would make two chips look alike.
	for _, bad := range []string{"a\u200bb", "Slip\u200dping", "\ufeffKran", "a\u202eb", "x\u2060y"} {
		if _, err := NormalizeName(bad); CodeOf(err) != CodeInvalidName {
			t.Errorf("NormalizeName(%q) = %v, want %s", bad, err, CodeInvalidName)
		}
	}
	// A non-breaking space at the edge is trimmed, not kept.
	if got, err := NormalizeName("\u00a0Kran\u00a0"); err != nil || got != "Kran" {
		t.Errorf("NormalizeName with NBSP = %q, %v", got, err)
	}
	// The length counts the composed form: 40 decomposed accents fit.
	if _, err := NormalizeName(strings.Repeat("e\u0301", 40)); err != nil {
		t.Errorf("40 characters typed decomposed refused: %v", err)
	}
}

func TestIconAllowList(t *testing.T) {
	icons := IconAllowList()
	seen := map[string]bool{}
	for _, i := range icons {
		if seen[i] {
			t.Errorf("icon %q listed twice", i)
		}
		seen[i] = true
		if !ValidIcon(i) {
			t.Errorf("listed icon %q is not valid", i)
		}
	}
	// The built-ins' defaults and the seeded groups' icons must be allowed.
	for _, i := range []string{iconFaktura, iconOther, "sailboat", "van", "forklift"} {
		if !ValidIcon(i) {
			t.Errorf("%q must be in the allow-list", i)
		}
	}
	if ValidIcon("rocket") || ValidIcon("") {
		t.Error("unknown icons must be refused")
	}
	// A caller cannot change the list through the returned slice.
	icons[0] = "changed"
	if IconAllowList()[0] == "changed" {
		t.Error("IconAllowList returned the backing array")
	}
}
