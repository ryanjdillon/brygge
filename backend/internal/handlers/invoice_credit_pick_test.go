package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Manual pick of the faktura a credit note settles (BRY-393): imported
// credit notes can have no payer, and the same-payer, same-amount
// candidates then come up empty.

type creditSearchResult struct {
	CreditNoteHasPayer bool `json:"credit_note_has_payer"`
	Items              []struct {
		ID         string `json:"id"`
		Number     int    `json:"invoice_number"`
		PayerName  string `json:"payer_name"`
		Settled    bool   `json:"settled"`
		SameAmount bool   `json:"same_amount"`
		SamePayer  bool   `json:"same_payer"`
	} `json:"items"`
}

func (f *creditFixture) search(t *testing.T, creditNote fin.Invoice, params url.Values) creditSearchResult {
	t.Helper()
	rec := f.call(t, http.MethodGet, "/invoices/"+creditNote.ID+"/credit-candidates?"+params.Encode(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d %s", rec.Code, rec.Body.String())
	}
	var res creditSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode search: %v (%s)", err, rec.Body.String())
	}
	return res
}

func (f *creditFixture) linkWith(t *testing.T, creditNote, invoice fin.Invoice, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body["invoice_id"] = invoice.ID
	return f.call(t, http.MethodPost, "/invoices/"+creditNote.ID+"/credit-link", body)
}

// dropPayer clears the invoice's member, as on imported credit notes that
// the previous system exported without one.
func (f *creditFixture) dropPayer(t *testing.T, inv fin.Invoice) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET user_id = NULL, recipient_id = NULL WHERE id = $1`, inv.ID,
	); err != nil {
		t.Fatalf("drop payer %d: %v", inv.Number, err)
	}
}

func ids(res creditSearchResult) map[string]bool {
	out := map[string]bool{}
	for _, it := range res.Items {
		out[it.ID] = true
	}
	return out
}

func TestCreditLinkMemberlessCreditNoteToPickedFaktura(t *testing.T) {
	f := newCreditFixture(t)
	f.h.audit = audit.NewService(f.club.Pool, zerolog.Nop())
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000, Imported: true})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: -30000, Imported: true})
	f.dropPayer(t, credit)

	if res := f.search(t, credit, nil); len(res.Items) != 0 {
		t.Fatalf("same-payer candidates = %+v, want none for a credit note without a payer", res.Items)
	}
	res := f.search(t, credit, url.Values{"any": {"1"}})
	if res.CreditNoteHasPayer {
		t.Errorf("credit_note_has_payer = true for a credit note without a payer")
	}
	var found bool
	for _, it := range res.Items {
		if it.ID == faktura.ID {
			found = true
			if !it.SameAmount || it.SamePayer || it.Settled {
				t.Errorf("search row = %+v, want same amount, other payer, open", it)
			}
		}
	}
	if !found {
		t.Fatalf("search = %+v, want the faktura listed", res.Items)
	}

	// No confirmation needed: the credit note has nobody to mismatch.
	rec := f.linkWith(t, credit, faktura, map[string]any{"manual_pick": true})
	wantCode(t, rec, http.StatusOK, "")
	if f.owed(t, faktura) {
		t.Errorf("faktura still owed after a member-less credit note was linked")
	}

	var details map[string]any
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 AND resource_id = $3`,
		f.club.ID, audit.ActionInvoiceCreditLinked, credit.ID,
	).Scan(&details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	want := map[string]any{
		"manual_pick": true, "member_mismatch": true,
		"confirm_other_member": false, "credit_note_has_payer": false,
		"outcome": "settled_pair",
	}
	for k, v := range want {
		if details[k] != v {
			t.Errorf("audit %s = %v, want %v (%v)", k, details[k], v, details)
		}
	}
}

func TestCreditLinkOtherMemberNeedsConfirmation(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 60000})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: -60000})

	wantCode(t, f.linkWith(t, credit, faktura, map[string]any{"manual_pick": true}), http.StatusConflict, "different_payer")
	if !f.owed(t, faktura) {
		t.Fatalf("an unconfirmed cross-payer link settled the faktura")
	}
	wantCode(t, f.linkWith(t, credit, faktura, map[string]any{"manual_pick": true, "confirm_other_member": true}),
		http.StatusOK, "")
	if f.owed(t, faktura) {
		t.Errorf("faktura still owed after a confirmed cross-payer link")
	}
}

// Neither a missing payer nor a confirmation relaxes the amount check.
func TestCreditLinkManualPickRefusesAmountMismatch(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000, Imported: true})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: -20000, Imported: true})
	f.dropPayer(t, credit)

	res := f.search(t, credit, url.Values{"any": {"1"}})
	for _, it := range res.Items {
		if it.ID == faktura.ID && it.SameAmount {
			t.Errorf("search row %+v marks a different amount as the same", it)
		}
	}
	wantCode(t, f.linkWith(t, credit, faktura, map[string]any{"manual_pick": true, "confirm_other_member": true}),
		http.StatusConflict, "partial_credit")
	if !f.owed(t, faktura) {
		t.Errorf("a refused partial credit settled the faktura")
	}
}

// The search only offers what the link would accept as a target, and never
// another club's fakturas.
func TestCreditSearchIsClubScopedAndEligibleOnly(t *testing.T) {
	f := newCreditFixture(t)
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: -30000, Imported: true})
	f.dropPayer(t, credit)

	eligible := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	paid := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: 30000, Imported: true, Paid: true})
	voided := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000, Status: "voided"})
	draft := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000, Unsent: true})
	otherCredit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -30000})
	credited := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: 12000})
	creditedBy := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: -12000})
	wantCode(t, f.link(t, creditedBy, credited), http.StatusOK, "")
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
	foreign := other.NewInvoice(t, fin.InvoiceOpts{Cents: 30000, Imported: true})

	got := ids(f.search(t, credit, url.Values{"any": {"1"}}))
	for name, inv := range map[string]fin.Invoice{"eligible": eligible, "paid": paid} {
		if !got[inv.ID] {
			t.Errorf("%s faktura missing from the search", name)
		}
	}
	for name, inv := range map[string]fin.Invoice{
		"voided": voided, "draft": draft, "credit note": otherCredit,
		"credited": credited, "foreign club": foreign,
	} {
		if got[inv.ID] {
			t.Errorf("%s faktura offered by the search", name)
		}
	}

	byNumber := f.search(t, credit, url.Values{"any": {"1"}, "q": {strconv.Itoa(paid.Number)}})
	if !ids(byNumber)[paid.ID] {
		t.Errorf("search by number %d = %+v, want the paid faktura", paid.Number, byNumber.Items)
	}
	for _, it := range byNumber.Items {
		if it.ID == paid.ID && !it.Settled {
			t.Errorf("paid faktura not reported as settled: %+v", it)
		}
	}

	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET member_name = 'Synthetic Harbour Payer' WHERE id = $1`, eligible.ID,
	); err != nil {
		t.Fatalf("name: %v", err)
	}
	byName := f.search(t, credit, url.Values{"any": {"1"}, "q": {"harbour payer"}})
	if len(byName.Items) != 1 || byName.Items[0].ID != eligible.ID || byName.Items[0].PayerName != "Synthetic Harbour Payer" {
		t.Errorf("search by name = %+v, want only the named faktura", byName.Items)
	}
}
