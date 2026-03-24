package accounting

import "testing"

func TestPaymentTypeAccountMap(t *testing.T) {
	expected := map[string]string{
		"dues":              "3100",
		"harbor_membership": "3110",
		"slip_fee":          "3120",
		"booking":           "3200",
		"merchandise":       "3300",
	}

	for pType, wantCode := range expected {
		code, ok := paymentTypeAccountMap[pType]
		if !ok {
			t.Errorf("payment type %q not in map", pType)
			continue
		}
		if code != wantCode {
			t.Errorf("payment type %q maps to %q, want %q", pType, code, wantCode)
		}
	}
}

func TestPaymentTypeAccountMapCoversAllTypes(t *testing.T) {
	// These are the payment_type enum values from the schema
	knownTypes := []string{"dues", "harbor_membership", "slip_fee", "booking", "merchandise"}
	for _, pt := range knownTypes {
		if _, ok := paymentTypeAccountMap[pt]; !ok {
			t.Errorf("payment type %q missing from account map", pt)
		}
	}
}

func TestDefaultAccountCodes(t *testing.T) {
	if bankAccountCode != "1920" {
		t.Errorf("bank account code = %q, want 1920", bankAccountCode)
	}
	if receivablesAccountCode != "1500" {
		t.Errorf("receivables account code = %q, want 1500", receivablesAccountCode)
	}
	if defaultRevenueCode != "3100" {
		t.Errorf("default revenue code = %q, want 3100", defaultRevenueCode)
	}
}
