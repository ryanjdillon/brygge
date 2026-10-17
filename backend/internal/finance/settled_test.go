package finance

import (
	"strings"
	"testing"
)

func TestSettledPredicates(t *testing.T) {
	member := SettledForMemberSQL("i")
	for _, part := range []string{
		"i.payment_id IS NOT NULL",
		"i.vipps_paid_at IS NOT NULL",
		"i.status = 'voided'",
		"cn.credits_invoice_id = i.id AND cn.status <> 'voided'",
	} {
		if !strings.Contains(member, part) {
			t.Errorf("SettledForMemberSQL(i) = %s, missing %q", member, part)
		}
	}
	// Unaliased callers (UPDATE invoices ... WHERE) need the outer table named
	// inside the credit-note subquery.
	if got := SettledForMemberSQL(""); !strings.Contains(got, "cn.credits_invoice_id = invoices.id") {
		t.Errorf("SettledForMemberSQL() = %s, want the subquery to reference invoices.id", got)
	}
	if got := SettledForGLSQL(""); got != "(payment_id IS NOT NULL)" {
		t.Errorf("SettledForGLSQL() = %s", got)
	}
	if got := OwedByMemberSQL("i"); !strings.HasPrefix(got, "((i.total_amount > 0) AND NOT (") {
		t.Errorf("OwedByMemberSQL(i) = %s, want a debt check before the settled check", got)
	}
}
