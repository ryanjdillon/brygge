package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

const repairTestMSN = "123456"

// newReportImport creates a Report API import to hang stale rows on.
func newReportImport(t *testing.T, club *fin.Club) string {
	t.Helper()
	var id string
	if err := club.Pool.QueryRow(context.Background(),
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 'Vipps Report API 2026-09-01', $2, $3) RETURNING id`,
		club.ID, repairTestMSN, club.Treasurer.ID,
	).Scan(&id); err != nil {
		t.Fatalf("report import: %v", err)
	}
	return id
}

// staleOtherRow imports a funds row the way the importer did before the funds
// classifier existed: row type "other", hashed as such, and no time when the
// Report API entry carried none (stored as NULL tx_at).
func staleOtherRow(t *testing.T, svc *Service, club *fin.Club, importID string, row VippsRow) string {
	t.Helper()
	row.RowType, row.SettlementNumber = VippsRowOther, ""
	res, err := svc.ImportVippsRows(context.Background(), club.ID, importID, []VippsRow{row})
	if err != nil || res.Imported != 1 {
		t.Fatalf("import stale row = %+v (err %v)", res, err)
	}
	var id string
	if err := club.Pool.QueryRow(context.Background(),
		`SELECT id FROM vipps_import_rows WHERE club_id = $1 AND psp_ref = $2`, club.ID, row.PSPRef).Scan(&id); err != nil {
		t.Fatalf("stale row id: %v", err)
	}
	return id
}

// The nullable tx_at of a Report API entry without a time must not break the
// repair: the row becomes a fee row carrying the hash a fresh import of the
// same entry computes, so a re-pull of the date dedups against it instead of
// inserting a second fee row.
func TestRepairReportRowWithoutTimeHashesLikeAReimport(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	ctx := context.Background()
	importID := newReportImport(t, club)

	entry := vipps.FundsEntry{
		PspReference: "900-20260901", EntryType: "fees-retained", Amount: -1449, LedgerDate: "2026-09-01",
	}
	stale := VippsReportRows(repairTestMSN, []vipps.FundsEntry{entry}, nil)[0]
	id := staleOtherRow(t, svc, club, importID, stale)

	var nullTime bool
	if err := club.Pool.QueryRow(ctx, `SELECT tx_at IS NULL FROM vipps_import_rows WHERE id = $1`, id).Scan(&nullTime); err != nil || !nullTime {
		t.Fatalf("fixture tx_at null = %v (err %v), want a row without a time", nullTime, err)
	}

	n, skipped, err := svc.RepairVippsReportRows(ctx, club.ID)
	if err != nil || n != 1 || len(skipped) != 0 {
		t.Fatalf("repair = %d, %+v (err %v), want 1 repaired and none skipped", n, skipped, err)
	}

	var typ, hash string
	if err := club.Pool.QueryRow(ctx, `SELECT row_type, row_hash FROM vipps_import_rows WHERE id = $1`, id).Scan(&typ, &hash); err != nil {
		t.Fatalf("repaired row: %v", err)
	}
	fresh := VippsReportRows(repairTestMSN, []vipps.FundsEntry{entry}, nil)[0]
	if typ != "fee" || fresh.RowType != VippsRowFee {
		t.Errorf("repaired row type = %s (fresh import %s), want fee", typ, fresh.RowType)
	}
	if want := VippsRowHash(club.ID, fresh); strings.TrimSpace(hash) != want {
		t.Errorf("repaired hash = %s, want the fresh import's %s", hash, want)
	}

	// Re-pull of the same date.
	res, err := svc.ImportVippsRows(ctx, club.ID, importID, []VippsRow{fresh})
	if err != nil || res.Imported != 0 || res.SkippedDup != 1 {
		t.Fatalf("re-pull = %+v (err %v), want it deduped against the repaired row", res, err)
	}
	var rows int
	if err := club.Pool.QueryRow(ctx, `SELECT count(*) FROM vipps_import_rows WHERE club_id = $1`, club.ID).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("rows after re-pull = %d (err %v), want 1", rows, err)
	}
}

// A row the repair cannot update is reported and skipped; it neither fails the
// pass nor stops the order pass that follows from booking a charge in the same
// run. The healthy row without a time is still repaired.
func TestAutoReconcileSkipsAnUnrepairableReportRowAndStillBooks(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	ctx := context.Background()
	importID := newReportImport(t, club)

	// An UPDATE that fails for one row stands in for any per-row refusal
	// (constraint, trigger, lock timeout).
	if _, err := club.Pool.Exec(ctx, `
		CREATE FUNCTION repair_test_refuse() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic refusal'; END $$;
		CREATE TRIGGER repair_test_refuse BEFORE UPDATE ON vipps_import_rows
		  FOR EACH ROW WHEN (OLD.psp_ref = 'psp-unrepairable') EXECUTE FUNCTION repair_test_refuse()`); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	bad := staleOtherRow(t, svc, club, importID, VippsRow{
		MSN: repairTestMSN, Amount: -10, Message: "fees-retained", PSPRef: "psp-unrepairable",
		BookingDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	good := staleOtherRow(t, svc, club, importID, VippsRow{
		MSN: repairTestMSN, Amount: -14.49, Message: "fees-retained", PSPRef: "psp-repairable",
		BookingDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})

	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := club.NewOrderForInvoice(t, inv)
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference})

	res, err := svc.AutoReconcileOrderCharges(ctx, club.ID, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("AutoReconcileOrderCharges: %v", err)
	}
	if res.Repaired != 1 || res.Booked != 1 {
		t.Fatalf("result = %+v, want 1 repaired and 1 booked", res)
	}
	if len(res.SkippedDetails) != 1 || !strings.Contains(res.SkippedDetails[0].Ref, bad) ||
		!strings.Contains(res.SkippedDetails[0].Reason, "synthetic refusal") {
		t.Errorf("skipped = %+v, want the unrepairable row %s reported", res.SkippedDetails, bad)
	}
	if rowEntry(t, club, charge) == "" {
		t.Error("the order charge was not booked")
	}
	var badType, goodType string
	if err := club.Pool.QueryRow(ctx, `SELECT row_type FROM vipps_import_rows WHERE id = $1`, bad).Scan(&badType); err != nil || badType != "other" {
		t.Errorf("unrepairable row = %s (err %v), want it left as other", badType, err)
	}
	if err := club.Pool.QueryRow(ctx, `SELECT row_type FROM vipps_import_rows WHERE id = $1`, good).Scan(&goodType); err != nil || goodType != "fee" {
		t.Errorf("healthy row = %s (err %v), want fee", goodType, err)
	}
}

// A cancelled context says nothing about the row: the repair returns it as an
// error instead of reporting the row as skipped.
func TestRepairReportRowsReturnsContextErrorsInsteadOfSkipping(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	importID := newReportImport(t, club)

	if _, err := club.Pool.Exec(context.Background(), `
		CREATE FUNCTION repair_test_slow() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_sleep(5); RETURN NEW; END $$;
		CREATE TRIGGER repair_test_slow BEFORE UPDATE ON vipps_import_rows
		  FOR EACH ROW WHEN (OLD.psp_ref = 'psp-slow') EXECUTE FUNCTION repair_test_slow()`); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	staleOtherRow(t, svc, club, importID, VippsRow{
		MSN: repairTestMSN, Amount: -10, Message: "fees-retained", PSPRef: "psp-slow",
		BookingDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	n, skipped, err := svc.RepairVippsReportRows(ctx, club.ID)
	if err == nil || len(skipped) != 0 || n != 0 {
		t.Fatalf("repair under a cancelled context = %d, %+v (err %v), want an error and no skips", n, skipped, err)
	}
}
