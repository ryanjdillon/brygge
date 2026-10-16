package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func newOrderReconcileFixture(t *testing.T) (*Service, *fin.Club) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	return svc, club
}

// creditLines returns code → (debit, credit) for a journal entry.
func creditLines(t *testing.T, c *fin.Club, entryID string) map[string][2]float64 {
	t.Helper()
	rows, err := c.Pool.Query(context.Background(),
		`SELECT a.code, jl.debit, jl.credit FROM journal_lines jl
		   JOIN accounts a ON a.id = jl.account_id WHERE jl.journal_entry_id = $1`, entryID)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	defer rows.Close()
	out := map[string][2]float64{}
	for rows.Next() {
		var code string
		var dr, cr float64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		v := out[code]
		out[code] = [2]float64{v[0] + dr, v[1] + cr}
	}
	return out
}

func rowEntry(t *testing.T, c *fin.Club, rowID string) string {
	t.Helper()
	var entry *string
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&entry); err != nil {
		t.Fatalf("row: %v", err)
	}
	if entry == nil {
		return ""
	}
	return *entry
}

func TestAutoReconcileOrderChargeSettlesFaktura(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	ctx := context.Background()

	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := club.NewOrderForInvoice(t, inv)
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference})

	res, err := svc.AutoReconcileOrderCharges(ctx, club.ID, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("AutoReconcileOrderCharges: %v", err)
	}
	if res.Booked != 1 || len(res.SkippedDetails) != 0 {
		t.Fatalf("result = %+v, want 1 booked", res)
	}

	entry := rowEntry(t, club, charge)
	if entry == "" {
		t.Fatal("charge not linked to an entry")
	}
	lines := creditLines(t, club, entry)
	if lines["2900"] != [2]float64{450, 0} || lines["1500"] != [2]float64{0, 450} {
		t.Errorf("booking = %v, want DR 2900 / CR 1500 450", lines)
	}
	var paid bool
	if err := club.Pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil || !paid {
		t.Errorf("faktura not settled (paid=%v, err=%v)", paid, err)
	}

	again, err := svc.AutoReconcileOrderCharges(ctx, club.ID, club.Treasurer.ID)
	if err != nil || again.Booked != 0 {
		t.Errorf("rerun = %+v (err %v), want a no-op", again, err)
	}
}

func TestAutoReconcileOrderChargeCatalogLineAndMismatch(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	ctx := context.Background()

	// A QR checkout for a service: no faktura, one catalog line.
	item := club.PriceItem(t, "service")
	mkOrder := func(ref string, total float64) {
		var orderID string
		if err := club.Pool.QueryRow(ctx,
			`INSERT INTO orders (club_id, total_amount, token, vipps_reference, status)
			 VALUES ($1, $2, $3, $4, 'paid') RETURNING id`,
			club.ID, total, "tok-"+testutil.RandomHex(8), ref,
		).Scan(&orderID); err != nil {
			t.Fatalf("order: %v", err)
		}
		if _, err := club.Pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
			 VALUES ($1, $2, 'Synthetic service', 1, $3, $3)`, orderID, item, total,
		); err != nil {
			t.Fatalf("order line: %v", err)
		}
	}
	mkOrder("ref-catalog", 120)
	mkOrder("ref-mismatch", 300)
	catalog := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 12000, OrderID: "ref-catalog"})
	mismatch := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 25000, OrderID: "ref-mismatch"})

	res, err := svc.AutoReconcileOrderCharges(ctx, club.ID, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("AutoReconcileOrderCharges: %v", err)
	}
	if res.Booked != 1 {
		t.Errorf("booked = %d, want 1", res.Booked)
	}
	if lines := creditLines(t, club, rowEntry(t, club, catalog)); lines["3300"] != [2]float64{0, 120} {
		t.Errorf("catalog line booking = %v, want CR 3300 120", lines)
	}
	if rowEntry(t, club, mismatch) != "" {
		t.Error("a charge that doesn't match its order total was booked")
	}
	if len(res.SkippedDetails) != 1 || !strings.Contains(res.SkippedDetails[0].Reason, "does not match") {
		t.Errorf("skipped = %+v, want the mismatch reported", res.SkippedDetails)
	}
}

// Report API rows imported as "other" before the settlement entries were
// classified are repaired, after which the payout clears against its bank
// deposit and the settlement nets to zero on the clearing account.
func TestRepairReportRowsThenPayoutClears(t *testing.T) {
	svc, club := newOrderReconcileFixture(t)
	ctx := context.Background()
	booking := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	const msn = "111"

	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := club.NewOrderForInvoice(t, inv)

	var importID string
	if err := club.Pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn) VALUES ($1, 'Vipps Report API 2026-09-01', $2) RETURNING id`,
		club.ID, msn,
	).Scan(&importID); err != nil {
		t.Fatalf("report import: %v", err)
	}
	insert := func(rowType, message, orderID, psp string, amount, fee float64) string {
		var id string
		if err := club.Pool.QueryRow(ctx,
			`INSERT INTO vipps_import_rows (vipps_import_id, club_id, row_hash, row_type, tx_at, booking_date,
			                                amount, fee, message, psp_ref, order_id, msn)
			 VALUES ($1, $2, $3, $4, $5, $12, $6, $7, $8, $9, $10, $11) RETURNING id`,
			importID, club.ID, testutil.RandomHex(32), rowType, booking, amount, fee, message, psp, orderID, msn, booking,
		).Scan(&id); err != nil {
			t.Fatalf("report row: %v", err)
		}
		return id
	}
	insert("belastning", "capture", order.VippsReference, "p1", 450, 0)
	insert("fee", "capture-fee", order.VippsReference, "p1", 0, -14.46)
	payout := insert("other", "payout-scheduled", "Utb. 2000001 Vippsnr "+msn, "900-2000001", -435.51, 0)
	retained := insert("other", "fees-retained", "", "900-20260901", -14.49, 0)

	// A CSV "other" row whose payer message mentions a fee must never be reclassified.
	csv := club.NewVippsRow(t, fin.VippsRowOpts{RowType: "other", Cents: 10000, MSN: msn, Date: booking})
	if _, err := club.Pool.Exec(ctx, `UPDATE vipps_import_rows SET message = 'fee for slipping' WHERE id = $1`, csv); err != nil {
		t.Fatalf("csv message: %v", err)
	}

	res, err := svc.AutoReconcileOrderCharges(ctx, club.ID, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("AutoReconcileOrderCharges: %v", err)
	}
	if res.Repaired != 2 || res.Booked != 1 {
		t.Fatalf("result = %+v, want 2 repaired and 1 booked", res)
	}
	var typ, settlement string
	if err := club.Pool.QueryRow(ctx, `SELECT row_type, settlement_number FROM vipps_import_rows WHERE id = $1`, payout).Scan(&typ, &settlement); err != nil || typ != "payout" || settlement != "2000001" {
		t.Errorf("payout row = %s/%s (err %v), want payout/2000001", typ, settlement, err)
	}
	if err := club.Pool.QueryRow(ctx, `SELECT row_type FROM vipps_import_rows WHERE id = $1`, retained).Scan(&typ); err != nil || typ != "fee" {
		t.Errorf("retained fees row = %s (err %v), want fee", typ, err)
	}
	if err := club.Pool.QueryRow(ctx, `SELECT row_type FROM vipps_import_rows WHERE id = $1`, csv).Scan(&typ); err != nil || typ != "other" {
		t.Errorf("CSV row was reclassified to %s (err %v)", typ, err)
	}
	if n, err := svc.RepairVippsReportRows(ctx, club.ID); err != nil || n != 0 {
		t.Errorf("second repair = %d (err %v), want 0", n, err)
	}

	bankImport := club.NewBankImport(t)
	bankRow := club.NewBankRow(t, bankImport, fin.BankRowOpts{
		Date: booking.AddDate(0, 0, 2), Cents: 43551, Description: "Utb. 2000001 Vippsnr " + msn,
	})
	clear, err := svc.ClearVippsPayout(ctx, club.ID, bankRow, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("ClearVippsPayout: %v", err)
	}
	if !clear.Ready || clear.BankEntryID == "" || clear.FeeEntryID == "" {
		t.Fatalf("clear = %+v, want ready with fee and bank entries", clear)
	}
	if !floatNear(clear.ClearingBalance, 0, 0.01) {
		t.Errorf("settlement clearing balance = %v, want 0", clear.ClearingBalance)
	}
}
