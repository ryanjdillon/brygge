package accounting

import (
	"context"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

func TestImportVippsRowsDeduplicates(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})

	csv := `Salgssted,MSN/Vippsnummer,Land,Betalingsløsning,Tidspunkt,Bokføringsdato,Type,Beløp,Balanse,Gebyr,Nettobeløp,Valuta,Kundens navn,Kundens telefonnummer,Melding,Kategori,PSP-referanse,Ordre-ID/Referanse,Utbetalingsnummer,Bankkonto for utbetaling,Planlagt utbetalingsdato
Klokkarvik Båtlag,548005,Norge,Valgfritt beløp,2026-03-23 14:24:30,2026-03-23,Belastning,2700.00,2700.00,-47.25,2652.75,NOK,Geir Magne Ellingsen,+47 xxxx 5202,Sv 1234,,35859820875,13120637102,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Gebyrer fratrukket,-47.25,2652.75,,,NOK,,,,,460017-20260323,,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Utbetaling planlagt,-2652.75,0.00,,,NOK,,,,,460017-2000167,Utb. 2000167 Vippsnr 548005,2000167,NO34 7005 0279 3,2026-03-25
`

	rows, err := ParseVippsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by, row_count)
		 VALUES ($1, 'sample.csv', '548005', $2, $3) RETURNING id`,
		clubID, userID, len(rows),
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	svc := NewService(pool, nil, zerolog.Nop())

	res1, err := svc.ImportVippsRows(ctx, clubID, importID, rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if res1.Imported != 3 || res1.SkippedDup != 0 {
		t.Fatalf("first import counts: imported=%d skipped=%d, want 3/0", res1.Imported, res1.SkippedDup)
	}

	res2, err := svc.ImportVippsRows(ctx, clubID, importID, rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res2.Imported != 0 || res2.SkippedDup != 3 {
		t.Fatalf("second import counts: imported=%d skipped=%d, want 0/3", res2.Imported, res2.SkippedDup)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM vipps_import_rows WHERE club_id = $1`, clubID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 stored rows, got %d", n)
	}
}
