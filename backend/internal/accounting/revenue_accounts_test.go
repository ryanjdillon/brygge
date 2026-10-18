package accounting

import "testing"

// Every key the revenue map knows is listed here, so changing where a
// category books is a visible edit to this table, not a silent drift.
func TestRevenueAccountResolver(t *testing.T) {
	cases := []struct {
		category string
		code     string
		known    bool
	}{
		{"dues", "3100", true},
		{"booking", "3200", true},
		{"merchandise", "3300", true},
		{"membership", "3100", true},
		{"harbor_membership", "3110", true},
		{"slip_fee", "3120", true},
		{"electricity", "3130", true},
		{"seasonal_rental", "3120", true},
		{"guest", "3200", true},
		{"motorhome", "3210", true},
		{"room_hire", "3220", true},
		{"service", "3300", true},
		{"other", "3900", true},
		{"unknown_category", "3900", false},
		{"", "3900", false},
	}
	listed := map[string]bool{}
	for _, c := range cases {
		listed[c.category] = true
		code, known := RevenueAccount(c.category)
		if known != c.known {
			t.Errorf("RevenueAccount(%q) known = %v, want %v", c.category, known, c.known)
		}
		if known && code != c.code {
			t.Errorf("RevenueAccount(%q) = %s, want %s", c.category, code, c.code)
		}
		if !known && code != "" {
			t.Errorf("RevenueAccount(%q) = %s for an unknown category, want empty", c.category, code)
		}
		if got := RevenueAccountOrFallback(c.category); got != c.code {
			t.Errorf("RevenueAccountOrFallback(%q) = %s, want %s", c.category, got, c.code)
		}
	}
	for k := range revenueAccountMap {
		if !listed[k] {
			t.Errorf("revenueAccountMap key %q is not listed in this test", k)
		}
	}
	if fallbackRevenueCode != "3900" {
		t.Errorf("fallbackRevenueCode = %s, want 3900", fallbackRevenueCode)
	}
}

// Every payment_type enum value and every price-item category must be mapped;
// an unmapped one would silently book to the fallback account.
func TestRevenueAccountCoversVocabularies(t *testing.T) {
	paymentTypes := []string{"dues", "harbor_membership", "slip_fee", "booking", "merchandise"}
	for _, k := range append(paymentTypes, PriceItemCategories...) {
		if _, ok := RevenueAccount(k); !ok {
			t.Errorf("%q has no revenue account", k)
		}
	}
}
