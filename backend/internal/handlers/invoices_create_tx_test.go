package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// refuseLines makes the database refuse any invoice line whose description
// or sub-description mentions "Refused", standing in for a constraint a
// line can violate.
func refuseLines(t *testing.T, f *exemptionFixture) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE FUNCTION refuse_marked_line() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		     IF NEW.description LIKE '%Refused%' OR NEW.sub_description LIKE '%Refused%' THEN
		         RAISE EXCEPTION 'refused line';
		     END IF;
		     RETURN NEW;
		 END $$`,
		`CREATE TRIGGER refuse_marked_line BEFORE INSERT ON invoice_lines
		 FOR EACH ROW EXECUTE FUNCTION refuse_marked_line()`,
	} {
		if _, err := f.club.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("refuse trigger: %v", err)
		}
	}
}

func (f *exemptionFixture) invoiceCount(t *testing.T, userID string) (invoices, lines int) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(DISTINCT i.id), count(il.id)
		   FROM invoices i LEFT JOIN invoice_lines il ON il.invoice_id = i.id
		  WHERE i.club_id = $1 AND i.user_id = $2`, f.club.ID, userID,
	).Scan(&invoices, &lines); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	return invoices, lines
}

// A single faktura whose line the database refuses is not created at all:
// no faktura is kept with a total its lines disagree with (BRY-389).
func TestCreateInvoiceRefusedLineFailsFaktura(t *testing.T) {
	f := newExemptionFixture(t)
	refuseLines(t, f)
	member := f.club.NewMember(t, "member")

	var acct string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id FROM accounts WHERE club_id = $1 AND code = '3900'`, f.club.ID).Scan(&acct); err != nil {
		t.Fatalf("account: %v", err)
	}
	rec := f.call(t, f.h.HandleCreateInvoice, http.MethodPost, "/invoices/full", createInvoiceFullRequest{
		UserID: member.ID, DueDate: "2026-06-30", FiscalPeriodID: &f.club.PeriodID,
		Lines: []invoiceLineInput{
			{Description: "Medlemskontingent", Quantity: 1, UnitPrice: 450, PriceItemID: &f.dues},
			{Description: "Refused custom line", Quantity: 1, UnitPrice: 100, AccountID: &acct},
		},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create = %d %s, want 500", rec.Code, rec.Body.String())
	}
	if invoices, lines := f.invoiceCount(t, member.ID); invoices != 0 || lines != 0 {
		t.Errorf("kept %d invoices and %d lines, want none", invoices, lines)
	}
}

// In a bulk run a refused line fails only that member's faktura, which is
// reported as skipped with nothing of it kept; the others are created.
func TestBulkInvoiceRefusedLineFailsOnlyThatFaktura(t *testing.T) {
	f := newExemptionFixture(t)
	refuseLines(t, f)
	refused := f.slipHolder(t, 2.2)
	ok := f.slipHolder(t, 2.2)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE boats SET name = 'Refused boat' WHERE user_id = $1`, refused.ID); err != nil {
		t.Fatalf("rename boat: %v", err)
	}

	resp := f.bulk(t, bulkInvoiceRequest{
		UserIDs: []string{refused.ID, ok.ID}, PriceItemIDs: []string{f.dues}, BeamCategories: []string{"slip_fee"},
	})
	if len(resp.Created) != 1 || resp.Created[0].UserID != ok.ID {
		t.Fatalf("created = %+v, want only the unaffected member", resp.Created)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].UserID != refused.ID ||
		!strings.HasPrefix(resp.Skipped[0].Reason, "create failed") {
		t.Errorf("skipped = %+v, want the refused member as create failed", resp.Skipped)
	}
	if invoices, lines := f.invoiceCount(t, refused.ID); invoices != 0 || lines != 0 {
		t.Errorf("refused member kept %d invoices and %d lines, want none", invoices, lines)
	}
	if total, lines := f.lines(t, resp.Created[0].InvoiceID); total != 3050 || len(lines) != 2 {
		t.Errorf("unaffected faktura total %.2f over %d lines, want 3050 over 2", total, len(lines))
	}
}
