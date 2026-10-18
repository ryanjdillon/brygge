package accounting

import (
	"slices"
	"testing"
	"time"
)

func partnerDay(d int) time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d) }

func row(id string, day int, cents int64) partnerRow {
	return partnerRow{ID: id, Date: partnerDay(day), Cents: cents, BankAccountCode: "1920", Eligible: true}
}

func testScoring() partnerScoring {
	return partnerScoring{
		clubName:    normalizeName("Synthetic Harbour Club"),
		ownAccounts: map[string]bool{"11112222333": true},
		priceCents:  map[int64]bool{6200000: true, 45000: true},
	}
}

func TestScorePartnerSignals(t *testing.T) {
	sc := testScoring()
	out := row("out", 0, -6300000)
	out.ToAccount = "9999.11.22222"
	out.Counterpart = "Kari Testdotter"

	tests := []struct {
		name     string
		b        partnerRow
		strength string
		reasons  []string
	}{
		{"same account", func() partnerRow {
			r := row("in", 2, 100000)
			r.FromAccount = "99991122222"
			return r
		}(), PartnerStrong, []string{partnerSameAccount, partnerNetsToPrice}},
		{"same name in another order", func() partnerRow {
			r := row("in", 2, 100000)
			r.Counterpart = "TESTDOTTER KARI"
			return r
		}(), PartnerStrong, []string{partnerSameName, partnerNetsToPrice}},
		{"nets to a price only", row("in", 2, 100000), PartnerWeak, []string{partnerNetsToPrice}},
		{"exact reversal", row("in", 10, 6300000), PartnerStrong, []string{partnerReverses}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := scorePartner(out, tt.b, sc)
			if !ok {
				t.Fatalf("no suggestion")
			}
			if s.Strength != tt.strength || !slices.Equal(s.Reasons, tt.reasons) {
				t.Errorf("got %s %v, want %s %v", s.Strength, s.Reasons, tt.strength, tt.reasons)
			}
			if s.DaysApart != daysBetween(out.Date, tt.b.Date) {
				t.Errorf("days apart = %d", s.DaysApart)
			}
		})
	}
}

func TestScorePartnerNetIsInOre(t *testing.T) {
	a, b := row("a", 0, -6300000), row("b", 2, 100000)
	b.Counterpart, a.Counterpart = "Kari Testdotter", "Kari Testdotter"
	s, ok := scorePartner(a, b, testScoring())
	if !ok || s.Net != -62000 {
		t.Fatalf("net = %v (ok %v), want -62000", s.Net, ok)
	}
}

func TestScorePartnerRejects(t *testing.T) {
	sc := testScoring()
	base := row("a", 0, -50000)
	base.Counterpart = "Ola Testmann"
	named := func(id string, day int, cents int64) partnerRow {
		r := row(id, day, cents)
		r.Counterpart = "Ola Testmann"
		return r
	}
	tests := []struct {
		name string
		a, b partnerRow
	}{
		{"same sign", base, named("b", 1, -1000)},
		{"other bank account", base, func() partnerRow { r := named("b", 1, 1000); r.BankAccountCode = "1925"; return r }()},
		{"outside the 60-day window", base, named("b", 61, 1000)},
		{"reversal after 14 days with nothing else", row("a", 0, -39755), row("b", 15, 39755)},
		{"unrelated amounts and names", row("a", 0, -39755), func() partnerRow { r := row("b", 3, 12000); r.Counterpart = "Someone Else"; return r }()},
		{"ineligible partner", base, func() partnerRow { r := named("b", 1, 1000); r.Eligible = false; return r }()},
		{"the club's own name", base, func() partnerRow {
			r := named("b", 1, 50000)
			r.Counterpart = "SYNTHETIC HARBOUR CLUB - HØYRENTE"
			return r
		}()},
		{"the club's own account", row("a", 0, -50000), func() partnerRow {
			r := row("b", 0, 50000)
			r.FromAccount = "1111.22.22333"
			return r
		}()},
		{"same row", base, base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if s, ok := scorePartner(tt.a, tt.b, sc); ok {
				t.Errorf("unexpected suggestion %+v", s)
			}
		})
	}
}

func TestScorePartnerEmptyFieldsDoNotFire(t *testing.T) {
	a, b := row("a", 0, -12345), row("b", 1, 6789)
	if s, ok := scorePartner(a, b, testScoring()); ok {
		t.Errorf("empty names and accounts matched: %+v", s)
	}
}

func TestNamesMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"Ola Nordtest", "NORDTEST OLA", true},
		{"Ola Nordtest", "Ola Johan Nordtest", true},
		{"Ola Nordtest", "Ola Sørtest", false},
		{"Testfirma AS", "Testfirma", true},
		{"Norsk Testing AS", "Norsk Prøving AS", false},
		{"Testfirma", "Testfirma Renovasjon", false},
		{"", "Ola Nordtest", false},
		{"Øyvind Ærtest", "oyvind aertest", true},
	}
	for _, tt := range tests {
		if got := namesMatch(tt.a, tt.b); got != tt.want {
			t.Errorf("namesMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestRankPartnersOrdersAndCaps(t *testing.T) {
	sc := testScoring()
	a := row("a", 10, -6300000)
	a.Counterpart = "Kari Testdotter"
	strong := func(id string, day int) partnerRow {
		r := row(id, day, 100)
		r.Counterpart = "Kari Testdotter"
		return r
	}
	weak := row("w", 10, 100000) // nets to 62 000, same day
	pool := []partnerRow{weak, strong("s3", 20), strong("s1", 11), strong("s2", 8), strong("s4", 40)}
	got := rankPartners(a, pool, sc)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if want := []string{"s1", "s2", "s3"}; !slices.Equal(ids, want) {
		t.Errorf("ranked %v, want %v (strong first, closest first, capped at %d)", ids, want, maxPartners)
	}
}
