// backfill-bank-names re-decodes bank_import_rows.counterpart values that still
// carry the legacy 7-bit ISO-646-Norwegian substitution symbols (@ | { }) into
// proper Norwegian letters ("Lok@Y" → "Lokøy"), using the same case-aware
// decoder the importer now applies. It covers already-booked rows, which a
// re-import never re-parses; counterpart is a display-only field, so the rewrite
// touches no accounting.
//
// Environment:
//
//	DATABASE_URL   (required)
//	DRY_RUN=1      preview: count + sample, write nothing
//
// Idempotent — run it as often as you like. Run once with DRY_RUN=1 first to
// see the count and a few before/after samples, then again without it to write.
package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/accounting"
)

func main() {
	ctx := context.Background()
	dryRun := os.Getenv("DRY_RUN") == "1"

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if dryRun {
		log.Println("DRY RUN — no changes will be written")
	}

	res, err := accounting.BackfillCounterpartEncoding(ctx, db, dryRun)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}

	for _, s := range res.Samples {
		log.Printf("  %q → %q", s.Before, s.After)
	}
	verb := "updated"
	if dryRun {
		verb = "would update"
	}
	log.Printf("scanned %d rows with legacy encoding; %s %d", res.Scanned, verb, res.Changed)
}
