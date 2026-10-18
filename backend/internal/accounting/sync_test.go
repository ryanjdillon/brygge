package accounting

import "testing"

func TestDefaultAccountCodes(t *testing.T) {
	if bankAccountCode != "1920" {
		t.Errorf("bank account code = %q, want 1920", bankAccountCode)
	}
	if receivablesAccountCode != "1500" {
		t.Errorf("receivables account code = %q, want 1500", receivablesAccountCode)
	}
}
