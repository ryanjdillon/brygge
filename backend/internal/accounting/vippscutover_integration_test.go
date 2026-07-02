package accounting

import (
	"context"
	"testing"
)

// reconcileLegacyBilag books the settlement under the OLD single-bilag model:
// one balanced entry (DR bank + DR fee / CR receivable + CR revenue) with the
// 450 charge matched to the faktura so invoices.payment_id gets set — exactly
// the state the cutover has to regenerate while preserving that link.
func reconcileLegacyBilag(t *testing.T, svc *Service, ctx context.Context, f settlementFixture) string {
	t.Helper()
	lines := []VippsReconcileLine{
		{Kind: "bank_in", AccountCode: bankAccountCode, Debit: 712.50, Description: "Vipps utbetaling"},
		{VippsRowID: f.chargeA, Kind: "receivable", AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: f.invoiceID, Description: "Vipps innbetaling: Ola Testperson"},
		{VippsRowID: f.chargeB, Kind: "revenue", AccountCode: "3200", Credit: 300, Description: "Vipps innbetaling: Kari Walkup"},
		{VippsRowID: f.feeRow, Kind: "fee", AccountCode: vippsFeeAccountCode, Debit: 37.50, Description: "Vipps gebyr"},
	}
	entryID, err := svc.ReconcileVippsConfirm(ctx, f.clubID, f.bankRowID, f.periodID, f.userID, lines)
	if err != nil {
		t.Fatalf("legacy reconcile: %v", err)
	}
	return entryID
}

func TestRegenerateOpenPeriodVippsReconciliations(t *testing.T) {
	svc, ctx, f := newSettlement(t)

	legacyEntry := reconcileLegacyBilag(t, svc, ctx, f)

	// The legacy reconcile marked the faktura paid — capture the link.
	var legacyPayment *string
	if err := svc.db.QueryRow(ctx,
		`SELECT payment_id FROM invoices WHERE id = $1`, f.invoiceID,
	).Scan(&legacyPayment); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if legacyPayment == nil {
		t.Fatal("legacy reconcile should have set invoices.payment_id")
	}

	// Dry run must not mutate anything.
	dry, err := svc.RegenerateOpenPeriodVippsReconciliations(ctx, f.clubID, f.userID, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.ScannedBilags != 1 || dry.Regenerated != 1 || dry.ChargesBooked != 2 || dry.ReceivableCharges != 1 {
		t.Errorf("dry run summary = %+v, want 1 bilag / 1 regen / 2 charges / 1 receivable", dry)
	}
	var stillDraft string
	if err := svc.db.QueryRow(ctx,
		`SELECT status FROM journal_entries WHERE id = $1`, legacyEntry,
	).Scan(&stillDraft); err != nil {
		t.Fatalf("legacy entry should still exist after dry run: %v", err)
	}

	// Real run.
	res, err := svc.RegenerateOpenPeriodVippsReconciliations(ctx, f.clubID, f.userID, false)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if res.Regenerated != 1 || res.ChargesBooked != 2 || res.ReceivableCharges != 1 {
		t.Errorf("regenerate summary = %+v, want 1 regen / 2 charges / 1 receivable", res)
	}
	if len(res.Failed) != 0 {
		t.Errorf("unexpected failures: %v", res.Failed)
	}

	// Legacy draft is gone.
	var n int
	if err := svc.db.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries WHERE id = $1`, legacyEntry,
	).Scan(&n); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if n != 0 {
		t.Error("legacy draft bilag should be deleted after regeneration")
	}

	// Faktura link preserved — same payment id, untouched.
	var newPayment *string
	if err := svc.db.QueryRow(ctx,
		`SELECT payment_id FROM invoices WHERE id = $1`, f.invoiceID,
	).Scan(&newPayment); err != nil {
		t.Fatalf("read invoice after: %v", err)
	}
	if newPayment == nil || *newPayment != *legacyPayment {
		t.Errorf("payment_id changed: was %v now %v", legacyPayment, newPayment)
	}

	// Charge A re-posted DR clearing / CR 1500; charge B DR clearing / CR 3200.
	assertChargeReposted(t, svc, ctx, f.chargeA, receivablesAccountCode, 450)
	assertChargeReposted(t, svc, ctx, f.chargeB, "3200", 300)

	// Settlement clearing nets to ~0 and the bank row is re-linked.
	_, _, booking, err := svc.settlementForBankRow(ctx, f.clubID, f.bankRowID)
	if err != nil {
		t.Fatalf("settlement booking: %v", err)
	}
	bal, err := svc.settlementClearingBalance(ctx, f.clubID, "2000167", "548005", booking, f.bankRowID)
	if err != nil {
		t.Fatalf("clearing balance: %v", err)
	}
	if !floatNear(bal, 0, 0.01) {
		t.Errorf("regenerated settlement clearing = %.4f, want ~0", bal)
	}
	var bankEntry *string
	if err := svc.db.QueryRow(ctx,
		`SELECT journal_entry_id FROM bank_import_rows WHERE id = $1`, f.bankRowID,
	).Scan(&bankEntry); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if bankEntry == nil {
		t.Error("bank row should be re-linked to the new payout entry")
	}

	// Idempotent: the per-charge bank-payout entry shares source='vipps' /
	// source_table='bank_import_rows' with a legacy bilag, so the scan must
	// exclude it (it credits the clearing account) — a second run finds nothing.
	rerun, err := svc.RegenerateOpenPeriodVippsReconciliations(ctx, f.clubID, f.userID, false)
	if err != nil {
		t.Fatalf("second regenerate: %v", err)
	}
	if rerun.ScannedBilags != 0 || rerun.Regenerated != 0 {
		t.Errorf("re-run should be a no-op, got %+v", rerun)
	}
	// The faktura link still survives the (no-op) re-run.
	var afterRerun *string
	if err := svc.db.QueryRow(ctx,
		`SELECT payment_id FROM invoices WHERE id = $1`, f.invoiceID,
	).Scan(&afterRerun); err != nil {
		t.Fatalf("read invoice after re-run: %v", err)
	}
	if afterRerun == nil || *afterRerun != *legacyPayment {
		t.Errorf("payment_id changed on re-run: was %v now %v", legacyPayment, afterRerun)
	}
}

// assertChargeReposted checks a belastning row is now booked to a NEW entry
// with DR clearing / CR <account> for its amount.
func assertChargeReposted(t *testing.T, svc *Service, ctx context.Context, chargeID, creditAccount string, amount float64) {
	t.Helper()
	var entryID *string
	if err := svc.db.QueryRow(ctx,
		`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1`, chargeID,
	).Scan(&entryID); err != nil {
		t.Fatalf("read charge %s: %v", chargeID, err)
	}
	if entryID == nil {
		t.Fatalf("charge %s not re-booked", chargeID)
	}
	var clDr, acCr float64
	if err := svc.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN a.code = $2 THEN jl.debit END),0),
		        COALESCE(SUM(CASE WHEN a.code = $3 THEN jl.credit END),0)
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`,
		*entryID, vippsClearingAccountCode, creditAccount,
	).Scan(&clDr, &acCr); err != nil {
		t.Fatalf("sum charge entry: %v", err)
	}
	if clDr != amount || acCr != amount {
		t.Errorf("charge %s: clearing DR %.2f / %s CR %.2f, want %.2f each", chargeID, clDr, creditAccount, acCr, amount)
	}
}
