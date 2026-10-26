// vipps-cutover regenerates a club's OPEN-period Vipps reconciliations under
// the per-charge model (BRY-228): it reverses each legacy single-bilag payout
// and re-posts the settlement through the clearing account, preserving faktura
// payment links. Closed-period bilags are left on the legacy model.
//
// Environment:
//
//	DATABASE_URL   (required)
//	CLUB_ID        (required) — the club to cut over
//	CREATED_BY     (required) — user id to attribute the new bilags to
//	DRY_RUN=1      preview: scan + count, write nothing
//
// Run once with DRY_RUN=1 first to see the counts, then again to write. It is
// safe to re-run — regenerated settlements no longer have a legacy bilag to
// pick up. Flip the club's vipps_per_charge_enabled flag (economy settings)
// after a clean run so new imports route onto the per-charge path.
package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
)

func main() {
	ctx := context.Background()
	dryRun := os.Getenv("DRY_RUN") == "1"

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	clubID := os.Getenv("CLUB_ID")
	if clubID == "" {
		log.Fatal("CLUB_ID is required")
	}
	createdBy := os.Getenv("CREATED_BY")
	if createdBy == "" {
		log.Fatal("CREATED_BY is required")
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	svc := accounting.NewService(db, nil, zerolog.New(os.Stderr))

	if dryRun {
		log.Println("DRY RUN — no changes will be written")
	}
	res, err := svc.RegenerateOpenPeriodVippsReconciliations(ctx, clubID, createdBy, dryRun)
	if err != nil {
		log.Fatalf("cutover: %v", err)
	}

	verb := "regenerated"
	if dryRun {
		verb = "would regenerate"
	}
	log.Printf("scanned %d open-period bilags; %s %d settlements (%d charges, %d faktura-linked)", // #nosec G706 -- integer counts and a fixed verb; nothing user-controlled
		res.ScannedBilags, verb, res.Regenerated, res.ChargesBooked, res.ReceivableCharges)
	log.Printf("left %d closed-period bilags on the legacy model", res.SkippedClosedPeriod) // #nosec G706 -- integer count only
	if len(res.Failed) > 0 {
		log.Printf("FAILED (bank row ids, left for manual review): %v", res.Failed) // #nosec G706 -- database-generated row ids, not operator or member input
	}
}
