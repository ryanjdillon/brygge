package finance

import (
	"strings"
	"testing"
)

func TestSettledPredicates(t *testing.T) {
	member := SettledForMemberSQL("i")
	for _, part := range []string{
		"i.payment_id IS NOT NULL",
		"i.online_paid_at IS NOT NULL",
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

func TestOpenReceivablePredicate(t *testing.T) {
	got := OpenReceivableSQL("i")
	for _, part := range []string{
		"i.total_amount > 0",
		"i.status <> 'voided'",
		"i.payment_id IS NULL",
		"NOT (EXISTS (SELECT 1 FROM invoices cn WHERE cn.credits_invoice_id = i.id",
		"rd.invoice_id = i.id AND rd.kind = 'credit') = round(i.total_amount * 100)",
	} {
		if !strings.Contains(got, part) {
			t.Errorf("OpenReceivableSQL(i) = %s, missing %q", got, part)
		}
	}
	// The online charge that set online_paid_at is the payment that clears the
	// receivable, so this predicate must not exclude it.
	if strings.Contains(got, "online_paid_at") {
		t.Errorf("OpenReceivableSQL(i) = %s, must not depend on online_paid_at", got)
	}
}

func TestImportedReceivableOpenPredicate(t *testing.T) {
	got := ImportedReceivableOpenSQL("i", "$3::date")
	for _, part := range []string{
		"i.import_source IS NULL",
		"ob_je.club_id = i.club_id AND ob_je.source = 'opening_balance'",
		"ob_je.status = 'posted' AND ob_a.code = '1500' AND ob_jl.debit > 0",
		"ob_je.entry_date >= i.issue_date AND ob_je.entry_date <= ($3::date)",
		"is_je.source = 'invoice_sync'",
		"is_je.source_id = i.id::text AND is_je.status = 'posted'",
	} {
		if !strings.Contains(got, part) {
			t.Errorf("ImportedReceivableOpenSQL(i) = %s, missing %q", got, part)
		}
	}
	// An unaliased UPDATE invoices ... WHERE needs the outer table named
	// inside the correlated subqueries.
	if got := ImportedReceivableOpenSQL("", "CURRENT_DATE"); !strings.Contains(got, "ob_je.entry_date >= invoices.issue_date") {
		t.Errorf("ImportedReceivableOpenSQL() = %s, want the subquery to reference invoices.issue_date", got)
	}
}
