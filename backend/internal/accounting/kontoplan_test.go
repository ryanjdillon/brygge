package accounting

import "testing"

func TestDefaultKontplanNotEmpty(t *testing.T) {
	accounts := DefaultKontoplan()
	if len(accounts) == 0 {
		t.Fatal("DefaultKontoplan() returned empty list")
	}
	if len(accounts) < 25 {
		t.Errorf("expected at least 25 accounts, got %d", len(accounts))
	}
}

func TestDefaultKontoplanUniqueCodes(t *testing.T) {
	accounts := DefaultKontoplan()
	seen := make(map[string]bool)
	for _, a := range accounts {
		if seen[a.Code] {
			t.Errorf("duplicate account code: %s", a.Code)
		}
		seen[a.Code] = true
	}
}

func TestDefaultKontoplanValidTypes(t *testing.T) {
	validTypes := map[AccountType]bool{
		AccountTypeAsset:     true,
		AccountTypeLiability: true,
		AccountTypeRevenue:   true,
		AccountTypeExpense:   true,
	}
	validMVA := map[MVAEligibility]bool{
		MVAEligible:      true,
		MVAIneligible:    true,
		MVAPartial:       true,
		MVANotApplicable: true,
	}

	for _, a := range DefaultKontoplan() {
		if !validTypes[a.Type] {
			t.Errorf("account %s (%s) has invalid type: %q", a.Code, a.Name, a.Type)
		}
		if !validMVA[a.MVAEligible] {
			t.Errorf("account %s (%s) has invalid MVA eligibility: %q", a.Code, a.Name, a.MVAEligible)
		}
		if a.Code == "" || a.Name == "" {
			t.Errorf("account has empty code or name: code=%q name=%q", a.Code, a.Name)
		}
	}
}

func TestDefaultKontoplanSortOrder(t *testing.T) {
	accounts := DefaultKontoplan()
	for i := 1; i < len(accounts); i++ {
		if accounts[i].SortOrder <= accounts[i-1].SortOrder {
			t.Errorf("sort order not strictly increasing: %s (%d) <= %s (%d)",
				accounts[i].Code, accounts[i].SortOrder,
				accounts[i-1].Code, accounts[i-1].SortOrder)
		}
	}
}

func TestDefaultKontoplanHasAllAccountTypes(t *testing.T) {
	accounts := DefaultKontoplan()
	types := make(map[AccountType]int)
	for _, a := range accounts {
		types[a.Type]++
	}

	for _, at := range []AccountType{AccountTypeAsset, AccountTypeLiability, AccountTypeRevenue, AccountTypeExpense} {
		if types[at] == 0 {
			t.Errorf("no accounts of type %q", at)
		}
	}
}

func TestDefaultKontoplanBoatCostsIneligible(t *testing.T) {
	accounts := DefaultKontoplan()
	boatCodes := map[string]bool{"6100": true, "6110": true}

	for _, a := range accounts {
		if boatCodes[a.Code] && a.MVAEligible != MVAIneligible {
			t.Errorf("boat-related account %s (%s) should be ineligible for momskompensasjon, got %q",
				a.Code, a.Name, a.MVAEligible)
		}
	}
}

// Every account code the posting paths can emit must exist in the default
// chart; a missing one makes CreateJournalEntry fail with "account not
// found", which sync counts as skipped and nobody sees.
func TestCategoryMapsResolveToDefaultKontoplan(t *testing.T) {
	chart := map[string]bool{}
	for _, def := range DefaultKontoplan() {
		chart[def.Code] = true
	}

	check := func(source, key, code string) {
		if !chart[code] {
			t.Errorf("%s[%q] = %s, which is not in DefaultKontoplan", source, key, code)
		}
	}
	for k, code := range paymentTypeAccountMap {
		check("paymentTypeAccountMap", k, code)
	}
	for k, code := range vippsCategoryAccountMap {
		check("vippsCategoryAccountMap", k, code)
	}
	for name, code := range map[string]string{
		"bankAccountCode":          bankAccountCode,
		"receivablesAccountCode":   receivablesAccountCode,
		"defaultRevenueCode":       defaultRevenueCode,
		"vippsFeeAccountCode":      vippsFeeAccountCode,
		"vippsFallbackRevenueCode": vippsFallbackRevenueCode,
		"vippsClearingAccountCode": vippsClearingAccountCode,
	} {
		check("const", name, code)
	}
}

// The invoice and Vipps category maps must send every key they share to
// the same account: the accounts list shows one default account per price
// item, and a drift would book the same item to two accounts by channel.
func TestCategoryMapsAgree(t *testing.T) {
	for k, vipps := range vippsCategoryAccountMap {
		if invoice, ok := paymentTypeAccountMap[k]; ok && invoice != vipps {
			t.Errorf("category %q: vippsCategoryAccountMap = %s, paymentTypeAccountMap = %s", k, vipps, invoice)
		}
	}
}

func TestPriceCategoryAccount(t *testing.T) {
	for _, c := range PriceItemCategories {
		if _, ok := PriceCategoryAccount(c); !ok {
			t.Errorf("price-item category %q has no default account", c)
		}
	}
	want := map[string]string{"service": "3300", "slip_fee": "3120", "electricity": "3130", "other": "3900"}
	for c, code := range want {
		if got, _ := PriceCategoryAccount(c); got != code {
			t.Errorf("PriceCategoryAccount(%q) = %s, want %s", c, got, code)
		}
	}
	// Payment-type keys share the maps but are not price-item categories.
	for _, c := range []string{"merchandise", "dues", "booking", "unknown", ""} {
		if code, ok := PriceCategoryAccount(c); ok {
			t.Errorf("PriceCategoryAccount(%q) = %s, want no account", c, code)
		}
	}
}

func TestDefaultKontoplanClubhouseEligible(t *testing.T) {
	accounts := DefaultKontoplan()
	for _, a := range accounts {
		if a.Code == "6200" && a.MVAEligible != MVAEligible {
			t.Errorf("clubhouse account %s (%s) should be eligible for momskompensasjon, got %q",
				a.Code, a.Name, a.MVAEligible)
		}
	}
}
