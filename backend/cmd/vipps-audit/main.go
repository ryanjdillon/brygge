// vipps-audit finds Vipps payments that likely paid a member's faktura but
// that were never marked paid (BRY-223) — the pre-purring safety net. Run it
// before sending purringer: any member listed here probably already paid via
// Vipps and should NOT receive a reminder.
//
// It is read-only — it writes nothing — and reuses the reconciler's own
// customer→member and open-invoice matching so the results match what the
// treasurer sees in Tildel.
//
// Environment:
//
//	DATABASE_URL   (required)
//	CLUB_ID        (required) — the club to audit
//
// Rows flagged "CONFIRMED MISS" were already booked to the wrong account and
// no longer appear in the Tildel queue — resolve those manually. Rows flagged
// "in Tildel" are still unreconciled and can be assigned from the Tildel tab.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	clubID := os.Getenv("CLUB_ID")
	if clubID == "" {
		log.Fatal("CLUB_ID is required")
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	svc := accounting.NewService(db, nil, zerolog.New(os.Stderr))

	candidates, err := svc.AuditUnlinkedVippsPayments(ctx, clubID)
	if err != nil {
		log.Fatalf("audit: %v", err)
	}

	if len(candidates) == 0 {
		fmt.Println("No unlinked Vipps payments found — safe to send purringer.")
		return
	}

	confirmed := 0
	for _, c := range candidates {
		if c.AlreadyReconciled {
			confirmed++
		}
	}
	fmt.Printf("%d candidate(s): %d CONFIRMED MISS (resolve manually), %d still in Tildel.\n\n",
		len(candidates), confirmed, len(candidates)-confirmed)

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tDATE\tAMOUNT\tVIPPS CUSTOMER\tMEMBER\tFAKTURA\tMESSAGE")
	for _, c := range candidates {
		status := "in Tildel"
		if c.AlreadyReconciled {
			status = "CONFIRMED MISS"
		}
		fmt.Fprintf(w, "%s\t%s\t%.2f\t%s\t%s\t#%d\t%s\n",
			status,
			c.BookingDate.Format("2006-01-02"),
			c.Amount,
			c.CustomerName,
			c.MemberName,
			c.InvoiceNumber,
			c.Message,
		)
	}
	w.Flush()
}
