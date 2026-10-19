package accounting

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A KID in the Vipps message names the faktura being paid, so it beats the
// payer's taught alias; an alias naming another member resolves to no one
// (BRY-383).
func TestVippsKIDBeatsPayerAlias(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	aliased := f.namedMember(t, "Holdingeier")
	kidOwner := f.namedMember(t, "Kidhavar")
	kidInv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &kidOwner, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.exec(t, `INSERT INTO vipps_customer_aliases (club_id, customer_name, user_id) VALUES ($1, 'SYN HOLDING AS', $2)`, f.club.ID, aliased.ID)

	if id, pass, err := f.svc.resolveCustomerToMemberPass(ctx, f.club.ID, "SYN HOLDING AS", "Kontingent "+kidInv.KID); err != nil || id != "" || pass != memberPassConflict {
		t.Fatalf("alias and KID for different members = (%q, %d, %v), want a conflict resolving to no one", id, pass, err)
	}
	if id, pass, err := f.svc.resolveCustomerToMemberPass(ctx, f.club.ID, "SYN HOLDING AS", "Kontingent"); err != nil || id != aliased.ID || pass != memberPassAlias {
		t.Fatalf("alias without a KID = (%q, %d, %v), want the alias member", id, pass, err)
	}
	if id, pass, err := f.svc.resolveCustomerToMemberPass(ctx, f.club.ID, "SYN SOMEONE ELSE", "Kontingent "+kidInv.KID); err != nil || id != kidOwner.ID || pass != memberPassKID {
		t.Fatalf("KID without an alias = (%q, %d, %v), want the KID's member", id, pass, err)
	}

	// The aliased member also owes the same amount, and a price item fits
	// it: neither may take a charge whose KID names someone else.
	aliasedInv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &aliased, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.priceItem(t, "service", "Synthetic slipping", 45000, "once", "non_member")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "SYN HOLDING AS"})
	f.exec(t, `UPDATE vipps_import_rows SET message = $2 WHERE id = $1`, row, "Kontingent "+kidInv.KID)

	res := f.run(t)
	if len(res.Assigned) != 0 || res.NeedsReview != 1 || rowEntry(t, f.club, row) != "" {
		t.Fatalf("result = %+v, want the conflicting charge left for review", res)
	}
	if f.invoicePaid(t, aliasedInv.ID) || f.invoicePaid(t, kidInv.ID) {
		t.Fatalf("a conflicting charge settled a faktura")
	}

	// Once the alias points at the KID's member too, the KID books it.
	f.exec(t, `UPDATE vipps_customer_aliases SET user_id = $2 WHERE club_id = $1`, f.club.ID, kidOwner.ID)
	if res := f.run(t); len(res.Assigned) != 1 || !f.invoicePaid(t, kidInv.ID) || f.invoicePaid(t, aliasedInv.ID) {
		t.Fatalf("result = %+v, want the charge booked to the KID's faktura", res)
	}
}
