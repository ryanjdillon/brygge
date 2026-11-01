package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// The Report API's per-settlement fee shape: a per-charge fee row, a
// settlement-level retained row, and the funds report's fees-retained row. All
// three must land as fee rows next to the charge and the payout.
func TestSyncVippsReportFeeShapeLandsAsThreeFeeRows(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	ctx := context.Background()

	const date = "2026-10-25"
	fake := newFakeVippsReport("123456")
	fake.funds[date] = []vipps.FundsEntry{
		{EntryType: "capture", Reference: "chk-synthetic", PspReference: "psp-3", Amount: 45000, LedgerDate: date, Time: date + "T08:00:00Z"},
		{EntryType: "fees-retained", PspReference: "900-20261025", Amount: -1446, LedgerDate: date},
		{EntryType: "payout-scheduled", Reference: "Utb. 7000001 Vippsnr 123456", PspReference: "900-7000001", Amount: -43554, LedgerDate: date},
	}
	fake.fees[date] = []vipps.FeeEntry{
		{EntryType: "fee", Reference: "chk-synthetic", PspReference: "psp-3", Amount: -1446, LedgerDate: date, Time: date + "T08:00:00Z"},
		{EntryType: "fees-retained", PspReference: "900-20261025", Amount: 1446, LedgerDate: date},
	}

	res, err := svc.SyncVippsReport(ctx, fake, club.ID, "", "", date)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Rows != 5 || res.Imported != 5 || res.SkippedDup != 0 || res.ImportID == "" {
		t.Fatalf("result = %+v, want 5 rows imported", res)
	}

	type shape struct {
		rowType, psp   string
		amount, fee    float64
		settlementName string
	}
	rows, err := pool.Query(ctx,
		`SELECT row_type, psp_ref, amount::float8, fee::float8, settlement_number
		   FROM vipps_import_rows WHERE club_id = $1 AND msn = '123456'
		  ORDER BY row_type, psp_ref, amount, fee`, club.ID)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()
	var got []shape
	for rows.Next() {
		var s shape
		if err := rows.Scan(&s.rowType, &s.psp, &s.amount, &s.fee, &s.settlementName); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	want := []shape{
		{"belastning", "psp-3", 450, 0, ""},
		{"fee", "900-20261025", -14.46, 0, ""},
		{"fee", "900-20261025", 0, 14.46, ""},
		{"fee", "psp-3", 0, -14.46, ""},
		{"payout", "900-7000001", -435.54, 0, "7000001"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Re-pulling the same day is idempotent.
	again, err := svc.SyncVippsReport(ctx, fake, club.ID, "", "", date)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if again.Imported != 0 || again.SkippedDup != 5 {
		t.Errorf("re-sync = %+v, want 5 duplicates skipped", again)
	}
}
