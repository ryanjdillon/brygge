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

func TestDefaultKontoplanClubhouseEligible(t *testing.T) {
	accounts := DefaultKontoplan()
	for _, a := range accounts {
		if a.Code == "6200" && a.MVAEligible != MVAEligible {
			t.Errorf("clubhouse account %s (%s) should be eligible for momskompensasjon, got %q",
				a.Code, a.Name, a.MVAEligible)
		}
	}
}
