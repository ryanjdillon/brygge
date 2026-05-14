package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

func TestReconcileVippsPreviewBalances(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	// Vipps side
	csv := `Salgssted,MSN/Vippsnummer,Land,Betalingsløsning,Tidspunkt,Bokføringsdato,Type,Beløp,Balanse,Gebyr,Nettobeløp,Valuta,Kundens navn,Kundens telefonnummer,Melding,Kategori,PSP-referanse,Ordre-ID/Referanse,Utbetalingsnummer,Bankkonto for utbetaling,Planlagt utbetalingsdato
Klokkarvik Båtlag,548005,Norge,Valgfritt beløp,2026-03-23 14:24:30,2026-03-23,Belastning,2700.00,2700.00,-47.25,2652.75,NOK,Geir Magne Ellingsen,+47 xxxx 5202,Sv 1234,,35859820875,13120637102,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Gebyrer fratrukket,-47.25,2652.75,,,NOK,,,,,460017-20260323,,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Utbetaling planlagt,-2652.75,0.00,,,NOK,,,,,460017-2000167,Utb. 2000167 Vippsnr 548005,2000167,NO34 7005 0279 3,2026-03-25
`
	vRows, err := ParseVippsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse vipps: %v", err)
	}

	var vippsImportID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by, row_count)
		 VALUES ($1, 'sample.csv', '548005', $2, $3) RETURNING id`,
		clubID, userID, len(vRows),
	).Scan(&vippsImportID); err != nil {
		t.Fatalf("insert vipps import: %v", err)
	}
	if _, err := svc.ImportVippsRows(ctx, clubID, vippsImportID, vRows); err != nil {
		t.Fatalf("import vipps: %v", err)
	}

	// Bank side
	var bankImportID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, imported_by, row_count)
		 VALUES ($1, 'bank.csv', 'sparebank-norge-v1', $2, 1) RETURNING id`,
		clubID, userID,
	).Scan(&bankImportID); err != nil {
		t.Fatalf("insert bank import: %v", err)
	}
	bankRows := []BankRow{{
		Date:        time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
		Description: "Utb. 2000167 Vippsnr 548005",
		Amount:      2652.75,
		Reference:   "BANK1",
	}}
	if _, err := svc.ImportBankRows(ctx, clubID, bankImportID, "", bankRows); err != nil {
		t.Fatalf("import bank: %v", err)
	}

	var bankRowID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM bank_import_rows WHERE club_id = $1 LIMIT 1`, clubID,
	).Scan(&bankRowID); err != nil {
		t.Fatalf("get bank row id: %v", err)
	}

	preview, err := svc.ReconcileVippsPreview(ctx, clubID, bankRowID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.Balanced {
		t.Errorf("expected balanced preview; reason=%q lines=%+v", preview.Reason, preview.Lines)
	}
	if preview.PeriodYear != 2026 {
		t.Errorf("period_year = %d, want 2026", preview.PeriodYear)
	}
	if preview.PeriodClosed {
		t.Errorf("period_closed should be false when period is open")
	}

	// Closing the period should flip the flag.
	if _, err := pool.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed', closed_at = now(), closed_by = $2 WHERE id = $1`,
		periodID, userID,
	); err != nil {
		t.Fatalf("close period: %v", err)
	}
	closedPreview, err := svc.ReconcileVippsPreview(ctx, clubID, bankRowID)
	if err != nil {
		t.Fatalf("preview after close: %v", err)
	}
	if !closedPreview.PeriodClosed {
		t.Errorf("period_closed should be true after closing the period")
	}
	// Re-open for the rest of the test.
	if _, err := pool.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'open', closed_at = NULL, closed_by = NULL WHERE id = $1`,
		periodID,
	); err != nil {
		t.Fatalf("reopen period: %v", err)
	}
	if preview.SettlementNumber != "2000167" || preview.MSN != "548005" {
		t.Errorf("settlement/msn = (%q, %q)", preview.SettlementNumber, preview.MSN)
	}
	if preview.UnresolvedCount != 1 {
		t.Errorf("expected 1 unresolved customer (no matching member), got %d", preview.UnresolvedCount)
	}
	if len(preview.Lines) != 3 {
		t.Errorf("expected 3 lines (bank, clearing, fee), got %d", len(preview.Lines))
	}

	// Confirm — should create a balanced draft entry under the 'vipps' source.
	entryID, err := svc.ReconcileVippsConfirm(ctx, clubID, bankRowID, periodID, userID, preview.Lines)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var (
		source string
		status string
		dr, cr float64
	)
	if err := pool.QueryRow(ctx,
		`SELECT je.source::text, je.status::text, COALESCE(SUM(jl.debit),0), COALESCE(SUM(jl.credit),0)
		 FROM journal_entries je LEFT JOIN journal_lines jl ON jl.journal_entry_id = je.id
		 WHERE je.id = $1 GROUP BY je.source, je.status`,
		entryID,
	).Scan(&source, &status, &dr, &cr); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if source != "vipps" {
		t.Errorf("source = %q, want vipps", source)
	}
	if status != "draft" {
		t.Errorf("status = %q, want draft", status)
	}
	if dr != cr || dr == 0 {
		t.Errorf("entry not balanced: dr=%.2f cr=%.2f", dr, cr)
	}
}
