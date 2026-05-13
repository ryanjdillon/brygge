package accounting

import "testing"

func TestInferOwnAccountNumber(t *testing.T) {
	// Most rows have the drift account on either Fra konto or Til konto.
	rows := []BankRow{
		{FromAccount: "34700502793", ToAccount: ""},               // Til konto missing
		{FromAccount: "", ToAccount: "34700502793"},               // outflow
		{FromAccount: "34700502793", ToAccount: "36285182314"},    // internal transfer out
		{FromAccount: "12345", ToAccount: "34700502793"},          // inflow from third party
	}
	if got := inferOwnAccountNumber(rows); got != "34700502793" {
		t.Errorf("inferOwnAccountNumber = %q, want 34700502793", got)
	}
}

func TestInferOwnAccountNumberEmpty(t *testing.T) {
	if got := inferOwnAccountNumber(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := inferOwnAccountNumber([]BankRow{{Description: "x"}}); got != "" {
		t.Errorf("expected empty when no account columns, got %q", got)
	}
}
