package accounting

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

type syncFixture struct {
	pool     *pgxpool.Pool
	svc      *Service
	clubID   string
	userID   string
	periodID string
	slipItem string
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	f := &syncFixture{pool: pool, svc: NewService(pool, nil, zerolog.Nop())}
	f.clubID = testutil.SeedClub(t, pool)
	f.userID, _ = testutil.SeedUser(t, pool, f.clubID, []string{"treasurer"})
	if _, err := f.svc.SeedKontoplan(ctx, f.clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		f.clubID,
	).Scan(&f.periodID); err != nil {
		t.Fatalf("fiscal period: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active)
		 VALUES ($1, 'slip_fee', 'Plassleie syntetisk', 2500, 'season', true) RETURNING id`,
		f.clubID,
	).Scan(&f.slipItem); err != nil {
		t.Fatalf("slip item: %v", err)
	}
	return f
}

// invoice inserts a one-line invoice. accountCode, when set, ties the line
// to that account instead of the slip price item (a custom line).
func (f *syncFixture) invoice(t *testing.T, number int, total float64, status string, sent bool, accountCode string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	sentAt := "NULL"
	if sent {
		sentAt = "now()"
	}
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, issue_date, due_date, total_amount, status, kid_number, sent_at)
		 VALUES ($1, $2, $3, '2026-03-01', '2026-03-15', $4, $5, $6, `+sentAt+`) RETURNING id`,
		f.clubID, f.userID, number, total, status, "77"+testutil.RandomHex(4),
	).Scan(&id); err != nil {
		t.Fatalf("invoice %d: %v", number, err)
	}
	if accountCode != "" {
		if _, err := f.pool.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, account_id)
			 VALUES ($1, 'Custom line', 1, $2, $2, (SELECT id FROM accounts WHERE club_id = $3 AND code = $4))`,
			id, total, f.clubID, accountCode,
		); err != nil {
			t.Fatalf("custom line: %v", err)
		}
		return id
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
		 VALUES ($1, 'Plassleie', 1, $2, $2, $3)`,
		id, total, f.slipItem,
	); err != nil {
		t.Fatalf("slip line: %v", err)
	}
	return id
}

func (f *syncFixture) sync(t *testing.T) *SyncResult {
	t.Helper()
	res, err := f.svc.SyncInvoices(context.Background(), f.clubID, f.periodID, f.userID)
	if err != nil {
		t.Fatalf("SyncInvoices: %v", err)
	}
	return res
}

// net returns debit minus credit for an account in the trial balance.
func (f *syncFixture) net(t *testing.T, code string) float64 {
	t.Helper()
	tb, err := f.svc.TrialBalance(context.Background(), f.clubID, f.periodID, false)
	if err != nil {
		t.Fatalf("TrialBalance: %v", err)
	}
	for _, l := range tb.Lines {
		if l.AccountCode == code {
			return l.Debit - l.Credit
		}
	}
	return 0
}

func (f *syncFixture) entryLines(t *testing.T, invoiceID string) map[string][2]float64 {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT a.code, jl.debit, jl.credit
		 FROM journal_entries je
		 JOIN journal_lines jl ON jl.journal_entry_id = je.id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.source = 'invoice_sync' AND je.source_id = $1`,
		invoiceID,
	)
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
		out[code] = [2]float64{dr, cr}
	}
	return out
}

func TestSyncInvoicesSkipsVoidedAndUnsent(t *testing.T) {
	f := newSyncFixture(t)
	voided := f.invoice(t, 2001, 2500, "voided", true, "")
	draft := f.invoice(t, 2002, 2500, "open", false, "")
	sent := f.invoice(t, 2003, 2500, "open", true, "")

	res := f.sync(t)
	if res.Synced != 1 {
		t.Fatalf("synced = %d, want 1 (%+v)", res.Synced, res)
	}
	if len(f.entryLines(t, voided)) != 0 || len(f.entryLines(t, draft)) != 0 {
		t.Errorf("voided or unsent invoice was booked")
	}
	if len(f.entryLines(t, sent)) == 0 {
		t.Errorf("sent invoice was not booked")
	}
}

func TestSyncInvoicesReversesInvoiceVoidedAfterBooking(t *testing.T) {
	f := newSyncFixture(t)
	id := f.invoice(t, 2101, 2500, "open", true, "")
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("first sync: %+v", res)
	}
	if got := f.net(t, "1500"); got != 2500 {
		t.Fatalf("1500 after booking = %v, want 2500", got)
	}

	if _, err := f.pool.Exec(context.Background(), `UPDATE invoices SET status = 'voided' WHERE id = $1`, id); err != nil {
		t.Fatalf("void invoice: %v", err)
	}
	res := f.sync(t)
	if res.Reversed != 1 || res.Synced != 0 {
		t.Fatalf("second sync = %+v, want reversed=1 synced=0", res)
	}
	// The original stays (voided) and a posted reversal offsets it, so the
	// reports must net receivable and revenue to zero, not to -2500.
	if got := f.net(t, "1500"); got != 0 {
		t.Errorf("1500 after reversal = %v, want 0", got)
	}
	if got := f.net(t, "3120"); got != 0 {
		t.Errorf("3120 after reversal = %v, want 0", got)
	}
	if res := f.sync(t); res.Reversed != 0 || res.Synced != 0 {
		t.Errorf("third sync should be a no-op: %+v", res)
	}
}

func TestSyncInvoicesCustomLineUsesItsAccount(t *testing.T) {
	f := newSyncFixture(t)
	id := f.invoice(t, 2201, 400, "open", true, "3900")
	f.sync(t)
	lines := f.entryLines(t, id)
	if lines["1500"] != [2]float64{400, 0} || lines["3900"] != [2]float64{0, 400} {
		t.Errorf("custom line booking = %v, want DR 1500 / CR 3900 400", lines)
	}
	if _, ok := lines[defaultRevenueCode]; ok && defaultRevenueCode != "3900" {
		t.Errorf("custom line fell back to %s: %v", defaultRevenueCode, lines)
	}
}

func TestSyncInvoicesCreditNoteFlipsSides(t *testing.T) {
	f := newSyncFixture(t)
	id := f.invoice(t, 2301, -2500, "open", true, "")
	res := f.sync(t)
	if res.Synced != 1 {
		t.Fatalf("credit note not booked: %+v", res)
	}
	lines := f.entryLines(t, id)
	if lines["1500"] != [2]float64{0, 2500} || lines["3120"] != [2]float64{2500, 0} {
		t.Errorf("credit note booking = %v, want DR 3120 / CR 1500 2500", lines)
	}
}

func TestSyncInvoicesZeroTotalReportsReason(t *testing.T) {
	f := newSyncFixture(t)
	f.invoice(t, 2401, 0, "open", true, "")
	res := f.sync(t)
	if res.Skipped != 1 || len(res.SkippedDetails) != 1 {
		t.Fatalf("result = %+v, want one skipped with a reason", res)
	}
	d := res.SkippedDetails[0]
	if d.Ref != "faktura 2401" || !strings.Contains(d.Reason, "0") {
		t.Errorf("skipped detail = %+v", d)
	}
}

func TestSyncPaymentsSkipsInvoiceSettlements(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	invoiceID := f.invoice(t, 2501, 2500, "open", true, "")

	var linked, standalone string
	for _, p := range []*string{&linked, &standalone} {
		if err := f.pool.QueryRow(ctx,
			`INSERT INTO payments (club_id, user_id, type, amount, status, created_at)
			 VALUES ($1, $2, 'dues', 2500, 'completed', '2026-03-05') RETURNING id`,
			f.clubID, f.userID,
		).Scan(p); err != nil {
			t.Fatalf("payment: %v", err)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET payment_id = $1 WHERE id = $2`, linked, invoiceID); err != nil {
		t.Fatalf("link payment: %v", err)
	}

	res, err := f.svc.SyncPayments(ctx, f.clubID, f.periodID, f.userID)
	if err != nil {
		t.Fatalf("SyncPayments: %v", err)
	}
	if res.Synced != 1 {
		t.Fatalf("synced = %d, want 1 (only the standalone payment)", res.Synced)
	}
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries WHERE source = 'payment_sync' AND source_id = $1`, linked,
	).Scan(&n); err != nil || n != 0 {
		t.Errorf("invoice-settling payment was booked again (n=%d, err=%v)", n, err)
	}
}
