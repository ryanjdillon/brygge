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
	if preview.SettlementNumber != "2000167" || preview.MSN != "548005" {
		t.Errorf("settlement/msn = (%q, %q)", preview.SettlementNumber, preview.MSN)
	}
	if preview.UnresolvedCount != 1 {
		t.Errorf("expected 1 unresolved customer (no matching member), got %d", preview.UnresolvedCount)
	}
	if len(preview.Lines) != 3 {
		t.Errorf("expected 3 lines (bank, clearing, fee), got %d", len(preview.Lines))
	}
}
