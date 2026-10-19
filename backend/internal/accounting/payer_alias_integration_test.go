package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// aliasOf returns the member the club's alias for name points at, or "".
func (f *bankAutoFixture) aliasOf(name string) string {
	return aliasMember(context.Background(), f.club.Pool, f.club.ID, name)
}

func (f *bankAutoFixture) aliasCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM vipps_customer_aliases WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatalf("count aliases: %v", err)
	}
	return n
}

func (f *bankAutoFixture) payerState(t *testing.T, rowID string) *PayerAliasState {
	t.Helper()
	sug, err := f.svc.BankRowSuggestionsFor(context.Background(), f.club.ID, rowID)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if sug.PayerAlias == nil {
		t.Fatalf("suggestions for an incoming row carry no payer_alias")
	}
	return sug.PayerAlias
}

// candidates returns the row's suggested and amount-matched fakturas, failing
// when either response carries the id of a member who owns no candidate.
func (f *bankAutoFixture) candidates(t *testing.T, rowID string, hidden ...string) map[string]InvoiceSuggestion {
	t.Helper()
	ctx := context.Background()
	sug, err := f.svc.BankRowSuggestionsFor(ctx, f.club.ID, rowID)
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	pot, err := f.svc.PotentialInvoicesForRow(ctx, f.club.ID, rowID, "", 0, 0)
	if err != nil {
		t.Fatalf("potential invoices: %v", err)
	}
	for _, body := range []any{sug, pot} {
		raw, _ := json.Marshal(body)
		for _, id := range hidden {
			if strings.Contains(string(raw), id) {
				t.Fatalf("response names member %s: %s", id, raw)
			}
		}
	}
	out := map[string]InvoiceSuggestion{}
	for _, inv := range append(sug.Invoices, pot...) {
		if prev, ok := out[inv.InvoiceID]; ok && prev.PayerResolvesHere != inv.PayerResolvesHere {
			t.Fatalf("suggestions and potential invoices disagree on faktura %s", inv.InvoiceID)
		}
		out[inv.InvoiceID] = inv
	}
	return out
}

func (f *bankAutoFixture) assign(t *testing.T, rowID, invoiceID string, req PayerAliasRequest) (*TaughtAlias, error) {
	t.Helper()
	_, taught, err := f.svc.AssignBankRowToInvoiceRemembering(context.Background(), f.club.ID, f.club.Treasurer.ID, rowID, invoiceID, req)
	return taught, err
}

// A payer whose name is no member's is remembered for the faktura's member,
// and the next bank auto-assign books that payer's deposit through it.
func TestTildelAssignTeachesPayerAlias(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Fortoy")
	inv := f.faktura(t, m, 30000)
	row := f.deposit(t, "SYN FORTOY HOLDING", 30000)

	st := f.payerState(t, row)
	if st.Payer != "SYN FORTOY HOLDING" || st.Pass != "none" || st.Taken || st.OwnClub {
		t.Fatalf("payer state = %+v, want an unknown payer with nothing to conflict", st)
	}
	if c := f.candidates(t, row)[inv.ID]; c.UserID != m.ID || c.PayerResolvesHere {
		t.Fatalf("candidate = %+v, want the faktura with its member's user_id, not resolved", c)
	}

	taught, err := f.assign(t, row, inv.ID, PayerAliasRequest{Remember: true})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if taught == nil || taught.UserID != m.ID || taught.Override {
		t.Fatalf("taught = %+v, want an alias for the member without override", taught)
	}
	if got := f.aliasOf("syn fortoy holding"); got != m.ID {
		t.Fatalf("alias points at %q, want the member", got)
	}
	if f.entry(t, row) == "" || !f.paid(t, inv.ID) {
		t.Fatalf("the deposit was not booked")
	}
	next := f.faktura(t, m, 30000)
	nextRow := f.deposit(t, "SYN FORTOY HOLDING", 30000)
	if st := f.payerState(t, nextRow); !st.Taken || st.Pass != "alias" {
		t.Errorf("payer state after teaching = %+v, want the alias", st)
	}
	if c := f.candidates(t, nextRow)[next.ID]; !c.PayerResolvesHere {
		t.Errorf("candidate = %+v, want the payer resolving to the member's faktura", c)
	}
	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != nextRow || res.Assigned[0].MatchPass != "alias" || !f.paid(t, next.ID) {
		t.Fatalf("auto-assign = %+v, want the next deposit booked through the taught alias", res)
	}
}

func TestTildelAssignWithoutRememberSavesNoAlias(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Fortoy")
	inv := f.faktura(t, m, 30000)
	row := f.deposit(t, "SYN FORTOY HOLDING", 30000)

	taught, err := f.assign(t, row, inv.ID, PayerAliasRequest{})
	if err != nil || taught != nil {
		t.Fatalf("assign = (%+v, %v), want booked with no alias", taught, err)
	}
	if f.aliasCount(t) != 0 || f.entry(t, row) == "" {
		t.Errorf("aliases %d, entry %q; want no alias and the row booked", f.aliasCount(t), f.entry(t, row))
	}
}

// Remembering another member's name or alias is refused with nothing booked,
// and saved only when the treasurer confirms the override.
func TestTildelAssignPayerAliasConflicts(t *testing.T) {
	cases := []struct {
		name  string
		payer string
		setup func(t *testing.T, f *bankAutoFixture, other fin.Member)
	}{
		{"other member's name", "Synthetic Kaiplass", func(*testing.T, *bankAutoFixture, fin.Member) {}},
		{"other member's alias", "SYN KAIPLASS AS", func(t *testing.T, f *bankAutoFixture, other fin.Member) {
			f.exec(t, `INSERT INTO vipps_customer_aliases (club_id, customer_name, user_id) VALUES ($1, 'SYN KAIPLASS AS', $2)`, f.club.ID, other.ID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBankAutoFixture(t)
			m := f.member(t, "Fortoy")
			other := f.member(t, "Kaiplass")
			tc.setup(t, f, other)
			inv := f.faktura(t, m, 30000)
			row := f.deposit(t, tc.payer, 30000)

			if st := f.payerState(t, row); !st.Taken {
				t.Fatalf("payer state = %+v, want the name taken", st)
			}
			if c := f.candidates(t, row, other.ID)[inv.ID]; c.PayerResolvesHere {
				t.Fatalf("candidate = %+v, want the payer not resolving to this member", c)
			}

			taught, err := f.assign(t, row, inv.ID, PayerAliasRequest{Remember: true})
			if !errors.Is(err, ErrAliasConflict) || taught != nil {
				t.Fatalf("assign = (%+v, %v), want ErrAliasConflict", taught, err)
			}
			if f.entry(t, row) != "" || f.paid(t, inv.ID) {
				t.Fatalf("a refused alias booked the row")
			}
			var drafts int
			if err := f.club.Pool.QueryRow(context.Background(),
				`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id = $2`, f.club.ID, row).Scan(&drafts); err != nil || drafts != 0 {
				t.Fatalf("journal entries for the row = %d (err %v), want none", drafts, err)
			}
			if got := f.aliasOf(tc.payer); got == m.ID {
				t.Fatalf("a refused alias was saved")
			}

			taught, err = f.assign(t, row, inv.ID, PayerAliasRequest{Remember: true, ConfirmOverride: true})
			if err != nil || taught == nil || !taught.Override || taught.UserID != m.ID {
				t.Fatalf("override assign = (%+v, %v), want the alias saved with override", taught, err)
			}
			if got := f.aliasOf(tc.payer); got != m.ID || f.entry(t, row) == "" {
				t.Errorf("alias %q, entry %q; want the alias moved to the member and the row booked", got, f.entry(t, row))
			}
		})
	}
}

// A name shared by two members is a conflict for either, and resolves to
// neither.
func TestPayerAliasStateSharedName(t *testing.T) {
	f := newBankAutoFixture(t)
	a := f.member(t, "Kaiplass")
	f.member(t, "Kaiplass")
	inv := f.faktura(t, a, 30000)
	row := f.deposit(t, "Synthetic Kaiplass", 30000)

	if st := f.payerState(t, row); !st.Taken || st.Pass != "none" {
		t.Fatalf("payer state = %+v, want the name taken but resolving to no one", st)
	}
	if c := f.candidates(t, row)[inv.ID]; c.PayerResolvesHere {
		t.Fatalf("candidate = %+v, want a shared name to resolve nowhere", c)
	}
	if _, err := f.assign(t, row, inv.ID, PayerAliasRequest{Remember: true}); !errors.Is(err, ErrAliasConflict) {
		t.Fatalf("assign err = %v, want ErrAliasConflict for a namesake's faktura", err)
	}
}

// Nothing to learn: the payer already resolves to the member, or is the
// club itself. The assign books and saves no alias.
func TestTildelAssignPayerAliasNothingToLearn(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Fortoy")

	inv := f.faktura(t, m, 30000)
	row := f.deposit(t, "Synthetic Fortoy", 30000)
	if st := f.payerState(t, row); !st.Taken || st.Pass != "name" {
		t.Fatalf("payer state = %+v, want the member by name", st)
	}
	if c := f.candidates(t, row)[inv.ID]; !c.PayerResolvesHere {
		t.Fatalf("candidate = %+v, want the payer resolving to the member's faktura", c)
	}
	if taught, err := f.assign(t, row, inv.ID, PayerAliasRequest{Remember: true}); err != nil || taught != nil {
		t.Fatalf("assign = (%+v, %v), want booked with no alias", taught, err)
	}

	f.exec(t, `UPDATE bank_imports SET bank_account_number = '12345678903' WHERE id = $1`, f.imp)
	var clubName string
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT name FROM clubs WHERE id = $1`, f.club.ID).Scan(&clubName); err != nil {
		t.Fatalf("club name: %v", err)
	}
	own := f.faktura(t, m, 20000)
	ownRow := f.deposit(t, clubName, 20000, fin.BankRowOpts{FromAccount: "1234.56.78903"})
	if st := f.payerState(t, ownRow); !st.OwnClub {
		t.Fatalf("payer state = %+v, want own_club", st)
	}
	if taught, err := f.assign(t, ownRow, own.ID, PayerAliasRequest{Remember: true}); err != nil || taught != nil {
		t.Fatalf("assign = (%+v, %v), want booked with no alias", taught, err)
	}
	if f.aliasCount(t) != 0 {
		t.Errorf("aliases = %d, want none", f.aliasCount(t))
	}
}

func TestTildelMultiAssignPayerAlias(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Fortoy")

	inv := f.faktura(t, m, 30000)
	rows := []string{f.deposit(t, "SYN FORTOY HOLDING", 10000), f.deposit(t, "Syn Fortoy Holding", 20000)}
	_, taught, err := f.svc.AssignMultipleBankRowsToInvoiceRemembering(ctx, f.club.ID, f.club.Treasurer.ID, rows, inv.ID, PayerAliasRequest{Remember: true})
	if err != nil || taught == nil || taught.UserID != m.ID {
		t.Fatalf("same-payer multi assign = (%+v, %v), want the alias saved", taught, err)
	}
	if f.aliasOf("SYN FORTOY HOLDING") != m.ID {
		t.Errorf("alias not saved for the member")
	}

	mixed := f.faktura(t, m, 30000)
	rows = []string{f.deposit(t, "SYN FORTOY HOLDING", 10000), f.deposit(t, "SYN ANOTHER PAYER", 20000)}
	_, taught, err = f.svc.AssignMultipleBankRowsToInvoiceRemembering(ctx, f.club.ID, f.club.Treasurer.ID, rows, mixed.ID, PayerAliasRequest{Remember: true})
	if err != nil || taught != nil {
		t.Fatalf("mixed-payer multi assign = (%+v, %v), want booked with no alias", taught, err)
	}
	if f.aliasOf("SYN ANOTHER PAYER") != "" || f.entry(t, rows[1]) == "" {
		t.Errorf("mixed payers saved an alias or left a row unbooked")
	}

	f.member(t, "Kaiplass")
	conflict := f.faktura(t, m, 30000)
	rows = []string{f.deposit(t, "Synthetic Kaiplass", 10000), f.deposit(t, "Synthetic Kaiplass", 20000)}
	if _, _, err := f.svc.AssignMultipleBankRowsToInvoiceRemembering(ctx, f.club.ID, f.club.Treasurer.ID, rows, conflict.ID, PayerAliasRequest{Remember: true}); !errors.Is(err, ErrAliasConflict) {
		t.Fatalf("conflicting multi assign err = %v, want ErrAliasConflict", err)
	}
	if f.entry(t, rows[0]) != "" || f.entry(t, rows[1]) != "" || f.aliasOf("Synthetic Kaiplass") != "" {
		t.Fatalf("a refused multi assign booked a row or saved the alias")
	}
}

func TestUpsertCustomerAliasRefusesNonMember(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	for _, id := range []string{"00000000-0000-0000-0000-0000000000aa", "not-a-uuid"} {
		if _, err := f.svc.UpsertCustomerAlias(ctx, f.club.ID, "SYN PAYER", id); !errors.Is(err, ErrNotClubMember) {
			t.Errorf("UpsertCustomerAlias(%q) err = %v, want ErrNotClubMember", id, err)
		}
	}
	m := f.member(t, "Fortoy")
	a, err := f.svc.UpsertCustomerAlias(ctx, f.club.ID, "SYN PAYER", m.ID)
	if err != nil || a.UserID != m.ID {
		t.Fatalf("UpsertCustomerAlias(member) = (%+v, %v)", a, err)
	}
	if userID, err := f.svc.DeleteCustomerAlias(ctx, f.club.ID, a.ID); err != nil || userID != m.ID {
		t.Fatalf("DeleteCustomerAlias = (%q, %v), want the member", userID, err)
	}
	if _, err := f.svc.DeleteCustomerAlias(ctx, f.club.ID, a.ID); !errors.Is(err, ErrAliasNotFound) {
		t.Errorf("second delete err = %v, want ErrAliasNotFound", err)
	}
}

// A booking that fails after the alias is saved rolls the alias back with it:
// the alias is written in the faktura-lock transaction, never on its own.
func TestTildelAssignFailedBookingSavesNoAlias(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Fortoy")
	single := f.faktura(t, m, 30000)
	multi := f.faktura(t, m, 30000)
	row := f.deposit(t, "SYN FORTOY HOLDING", 30000)
	split := []string{f.deposit(t, "SYN FORTOY HOLDING", 10000), f.deposit(t, "SYN FORTOY HOLDING", 20000)}
	// No such account: creating the entry fails after the alias is saved.
	f.exec(t, `UPDATE bank_imports SET bank_account_code = '1999' WHERE id = $1`, f.imp)

	noTrace := func(rowIDs ...string) {
		t.Helper()
		if f.aliasCount(t) != 0 {
			t.Errorf("a failed booking saved the alias")
		}
		for _, id := range rowIDs {
			var entries int
			if err := f.club.Pool.QueryRow(ctx,
				`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id = $2`, f.club.ID, id).Scan(&entries); err != nil {
				t.Fatalf("entries: %v", err)
			}
			if f.entry(t, id) != "" || entries != 0 {
				t.Errorf("row %s booked (entry %q) or left %d journal entries", id, f.entry(t, id), entries)
			}
		}
	}

	if _, err := f.assign(t, row, single.ID, PayerAliasRequest{Remember: true}); err == nil {
		t.Fatalf("single assign to a missing bank account succeeded")
	}
	noTrace(row)

	if _, _, err := f.svc.AssignMultipleBankRowsToInvoiceRemembering(ctx, f.club.ID, f.club.Treasurer.ID, split, multi.ID,
		PayerAliasRequest{Remember: true}); err == nil {
		t.Fatalf("multi assign to a missing bank account succeeded")
	}
	noTrace(split...)
	if f.paid(t, single.ID) || f.paid(t, multi.ID) {
		t.Errorf("a failed booking marked a faktura paid")
	}
}
