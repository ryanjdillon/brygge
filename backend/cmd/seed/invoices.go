package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// invoiceSeed describes one invoice to create across the Faktura tabs.
// state maps to how HandleListInvoices filters:
//   - "draft":  sent_at IS NULL  AND status = 'open'
//   - "sent":   sent_at IS NOT NULL AND status = 'open'
//   - "voided": status = 'voided'
type invoiceSeed struct {
	email        string  // recipient member (users.email); "" for none
	priceItem    string  // price_items.name — linked and used as the line description
	amount       float64 // total (single line)
	issueDaysAgo int     // issue_date = today - issueDaysAgo
	termDays     int     // due_date = issue_date + termDays
	state        string  // draft | sent | voided
}

// seedInvoices populates invoices across draft/sent/voided states so the
// tabbed Faktura page and its bulk actions (send reminder, copy emails,
// regenerate PDF, void) have data to exercise in dev. Idempotent: clears
// the club's invoices first (invoice_lines cascade).
func seedInvoices(ctx context.Context, db *pgxpool.Pool, clubID string, userIDs map[string]string) {
	// 2026 fiscal period drives the list's year column; created by an
	// earlier migration/seed. Fall back to NULL if absent.
	var fiscalPeriodID *string
	var fp string
	if err := db.QueryRow(ctx,
		`SELECT id FROM fiscal_periods WHERE club_id = $1 AND year = 2026 LIMIT 1`, clubID,
	).Scan(&fp); err == nil {
		fiscalPeriodID = &fp
	}

	// Resolve price-item ids by name for the Post column + line link.
	priceItemID := func(name string) *string {
		var id string
		if err := db.QueryRow(ctx,
			`SELECT id FROM price_items WHERE club_id = $1 AND name = $2 LIMIT 1`, clubID, name,
		).Scan(&id); err != nil {
			return nil
		}
		return &id
	}

	specs := []invoiceSeed{
		// Drafts (unsent) — the "Kladder" tab.
		{"slip-member@brygge.local", "Årlig plassleie", 8500, 2, 30, "draft"},
		{"member@brygge.local", "Årlig plassleie", 8500, 2, 30, "draft"},
		{"wl-member@brygge.local", "Harbor Membership", 50000, 1, 30, "draft"},
		{"admin@brygge.local", "Kran – opp/utsett", 1200, 0, 14, "draft"},

		// Sent, not yet due — "Sendt" tab, will show as awaiting payment.
		{"slip-member@brygge.local", "Sommersesong", 6000, 5, 30, "sent"},
		{"member@brygge.local", "Strøm vinter", 2000, 8, 30, "sent"},

		// Sent, overdue — issue far enough back that the due date has passed.
		{"wl-member@brygge.local", "Årlig plassleie", 8500, 45, 14, "sent"},
		{"admin@brygge.local", "Harbor Membership", 50000, 60, 14, "sent"},
		{"member@brygge.local", "Kran – opp/utsett", 1200, 30, 14, "sent"},

		// Voided — the "Annullerte" tab.
		{"slip-member@brygge.local", "Årlig plassleie", 8500, 20, 14, "voided"},
		{"admin@brygge.local", "Strøm vinter", 2000, 25, 14, "voided"},
	}

	// Idempotent re-seed: invoice_lines cascade on invoice delete.
	if _, err := db.Exec(ctx, `DELETE FROM invoices WHERE club_id = $1`, clubID); err != nil {
		fmt.Fprintf(os.Stderr, "  failed to clear invoices: %v\n", err)
		return
	}

	now := time.Now()
	created := 0
	for i, s := range specs {
		issue := now.AddDate(0, 0, -s.issueDaysAgo)
		due := issue.AddDate(0, 0, s.termDays)

		status := "open"
		var sentAt *time.Time
		if s.state == "voided" {
			status = "voided"
		}
		if s.state == "sent" || s.state == "voided" {
			t := issue.Add(24 * time.Hour)
			sentAt = &t
		}

		var userID *string
		if id := userIDs[s.email]; id != "" {
			userID = &id
		}
		// Valid mod-11 KID (matches production generation) so reissue and
		// reconcile flows are testable against realistic references.
		kid, kerr := finance.GenerateKIDMethod("000", 700+i, 1, finance.KIDMethodMod11)
		if kerr != nil {
			fmt.Fprintf(os.Stderr, "  failed to generate KID for %s: %v\n", s.email, kerr)
			continue
		}

		// Sent invoices carry a placeholder PDF blob so the reminder
		// ("purring") path — which requires a stored PDF — is testable in
		// dev without wiring the real PDF generator into the seed.
		var pdfData []byte
		if sentAt != nil {
			pdfData = []byte("%PDF-1.4\n% brygge seed placeholder\n")
		}

		var invoiceID string
		err := db.QueryRow(ctx, `
			INSERT INTO invoices (club_id, user_id, kid_number, issue_date, due_date,
			                      total_amount, status, sent_at, price_item_id, fiscal_period_id, pdf_data)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id
		`, clubID, userID, kid, issue, due, s.amount, status, sentAt,
			priceItemID(s.priceItem), fiscalPeriodID, pdfData).Scan(&invoiceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create invoice for %s: %v\n", s.email, err)
			continue
		}

		if _, err := db.Exec(ctx, `
			INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total,
			                           price_item_id, category)
			VALUES ($1, $2, 1, $3, $3, $4::uuid, (SELECT category FROM price_items WHERE id = $4::uuid))
		`, invoiceID, s.priceItem, s.amount, priceItemID(s.priceItem)); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create invoice line for %s: %v\n", s.email, err)
			continue
		}
		created++
	}
	fmt.Printf("  invoices: %d created (drafts + sent + voided)\n", created)
}
