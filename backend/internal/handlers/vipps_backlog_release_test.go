package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Releasing a 1500-parked backlog draft goes through the Vipps unassign with
// released_from_backlog: a charge that passes the settled-faktura test is
// released and audited with the flag; one that may have paid a faktura is
// refused and not audited. A bodyless unassign keeps working as before.
func TestUnassignVippsChargeReleasedFromBacklog(t *testing.T) {
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-charges/{rowID}/unassign", h.HandleUnassignVippsCharge)
	})
	club := env.club
	ctx := context.Background()

	parked := func(cents int64, payer string) (rowID, entryID string) {
		rowID = club.NewVippsRow(t, fin.VippsRowOpts{Cents: cents, CustomerName: payer})
		src := "vipps_import_rows"
		e, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
			FiscalPeriodID: club.PeriodID, EntryDate: "2026-06-01", Description: "Vipps innbetaling (legacy draft)",
			Source: "vipps", SourceID: &rowID, SourceTable: &src, CreatedBy: club.Treasurer.ID, ClubID: club.ID,
			Lines: []accounting.CreateJournalLineInput{
				{AccountCode: "2900", Debit: fin.Kr(cents)},
				{AccountCode: "1500", Credit: fin.Kr(cents)},
			},
		})
		if err != nil {
			t.Fatalf("draft: %v", err)
		}
		if _, err := club.Pool.Exec(ctx, `UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, e.ID, rowID); err != nil {
			t.Fatalf("link draft: %v", err)
		}
		return rowID, e.ID
	}
	release := map[string]bool{"released_from_backlog": true}

	row, entry := parked(77700, "Nobody Known")
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+row+"/unassign", release); rec.Code != http.StatusOK {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	logged := env.audit(audit.ActionVippsChargeUnassigned)
	if len(logged) != 1 || logged[0]["released_from_backlog"] != true || logged[0]["journal_entry_id"] != entry {
		t.Errorf("release audit = %v", logged)
	}

	// The payer's faktura is marked paid by a KID payment of the amount that
	// no booking explains: the charge may have paid it.
	m := env.member(t, "Legacypayer")
	inv := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000, Paid: true,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	if _, err := club.Pool.Exec(ctx,
		`UPDATE payments SET description = 'KID-avstemming' WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`,
		inv.ID); err != nil {
		t.Fatalf("mark payment: %v", err)
	}
	legacy, _ := parked(45000, "Synthetic Legacypayer")
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+legacy+"/unassign", release); rec.Code != http.StatusBadRequest {
		t.Errorf("release of a legacy-paid draft: %d %s, want 400", rec.Code, rec.Body)
	}
	if n := len(env.audit(audit.ActionVippsChargeUnassigned)); n != 1 {
		t.Errorf("a refused release was audited (%d rows)", n)
	}

	plain, _ := parked(33300, "Nobody Known")
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+plain+"/unassign", nil); rec.Code != http.StatusOK {
		t.Fatalf("bodyless unassign: %d %s", rec.Code, rec.Body)
	}
	logged = env.audit(audit.ActionVippsChargeUnassigned)
	if len(logged) != 2 {
		t.Fatalf("unassign audit rows = %d, want 2", len(logged))
	}
	if _, flagged := logged[1]["released_from_backlog"]; flagged {
		t.Errorf("a plain unassign carries released_from_backlog: %v", logged[1])
	}
}
