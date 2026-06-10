package accounting

import "testing"

func TestVippsSettlementPattern(t *testing.T) {
	cases := []struct {
		desc       string
		wantNum    string
		wantMSN    string
		wantMatch  bool
	}{
		{"Utb. 2000591 Vippsnr 698382", "2000591", "698382", true},
		{"Utb. 2000167 Vippsnr 548005", "2000167", "548005", true},
		{"utb.2000167 vippsnr 548005", "2000167", "548005", true}, // case + spacing
		{"Overførsel", "", "", false},
		{"Strømregning", "", "", false},
	}
	for _, c := range cases {
		m := VippsSettlementPattern.FindStringSubmatch(c.desc)
		if !c.wantMatch {
			if len(m) > 0 {
				t.Errorf("expected no match for %q, got %v", c.desc, m)
			}
			continue
		}
		if len(m) != 3 {
			t.Errorf("expected 3 submatches for %q, got %d", c.desc, len(m))
			continue
		}
		if m[1] != c.wantNum || m[2] != c.wantMSN {
			t.Errorf("for %q got (%q, %q), want (%q, %q)", c.desc, m[1], m[2], c.wantNum, c.wantMSN)
		}
	}
}

func TestExtractKID(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"hei":                           "",
		"Faktura 2026000123":            "2026000123",
		"KID 123456789012":              "123456789012",
		"sommer sesong":                 "",
		"x 12345":                       "",     // too short (< 6 digits)
		"REF 1234567 Båt":               "1234567",
	}
	for in, want := range cases {
		if got := extractKID(in); got != want {
			t.Errorf("extractKID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Per Hansen", "per hansen"},
		{"PER HANSEN", "per hansen"},
		{"  per  hansen  ", "per hansen"},
		{"Sølveig Kallestad", "solveig kallestad"},
		{"Mette-Lise Larsen", "mette lise larsen"},
		{"Per E. Hansen", "per e hansen"},
		{"Ærlig Øystein Ågnes", "aerlig oystein agnes"},
		{"Käthe Müller", "kathe muller"},
		{"José", "jose"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := normalizeName(c.in); got != c.want {
			t.Errorf("normalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeNameIdempotent(t *testing.T) {
	for _, s := range []string{"Per Hansen", "Sølveig", "Mette-Lise"} {
		first := normalizeName(s)
		second := normalizeName(first)
		if first != second {
			t.Errorf("not idempotent: %q -> %q -> %q", s, first, second)
		}
	}
}

func TestFloatNear(t *testing.T) {
	if !floatNear(2652.75, 2652.75, 0.005) {
		t.Errorf("equal floats not near")
	}
	if !floatNear(100.001, 100.000, 0.005) {
		t.Errorf("within tolerance not near")
	}
	if floatNear(100.01, 100.00, 0.005) {
		t.Errorf("outside tolerance reported near")
	}
}
