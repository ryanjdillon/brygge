package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A fully exempt faktura (BRY-351) nets to zero and is never owed, so a bank
// payment quoting its KID is an overpayment left for review, not a
// settlement.
func TestImportKIDLeavesZeroFakturaUnmatched(t *testing.T) {
	f := newMatchingFixture(t)
	m := f.club.NewMember(t, "member")
	zero := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m,
		Lines: []fin.Line{{Category: "slip_fee", Cents: 260000}, {Category: "slip_fee", Cents: -260000}}})
	if zero.TotalCents != 0 {
		t.Fatalf("fixture total = %d, want 0", zero.TotalCents)
	}

	res, err := f.svc.ImportBankRows(context.Background(), f.club.ID, f.club.NewBankImport(t), "", []BankRow{{
		Date: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), Description: "Innbetaling",
		Amount: 2600, Reference: "SYN" + testutil.RandomHex(4), KID: zero.KID,
	}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Matched != 0 {
		t.Errorf("import matched %d rows, want the payment left unmatched", res.Matched)
	}
	if f.rowBooked(t, zero.KID) || f.paymentID(t, zero) {
		t.Errorf("a bank payment settled a zero faktura")
	}
}
