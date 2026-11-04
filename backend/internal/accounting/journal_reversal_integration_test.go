package accounting

import (
	"context"
	"errors"
	"testing"
)

// reversalOf returns the reversal bilag a void of entryID posted.
func (f *hygieneFixture) reversalOf(t *testing.T, entryID string) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id::text FROM journal_entries WHERE club_id = $1 AND reverses_entry_id = $2`, f.club.ID, entryID,
	).Scan(&id); err != nil {
		t.Fatalf("reversal of %s: %v", entryID, err)
	}
	return id
}

// A reversal bilag cannot be voided from the ledger: that would re-instate
// the original booking with no explicit link between the two voids. The
// original stays voided and the books do not move; a normal entry still voids.
func TestLedgerVoidRefusesAReversalBilag(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	orig := f.entry(t, f.club, "1920", "3900", 1250, true)
	other := f.entry(t, f.club, "1920", "3900", 300, true)

	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, orig, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void of a normal entry: %v", err)
	}
	reversal := f.reversalOf(t, orig)
	entries := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID)
	bank := f.net(t, f.club, "1920")

	_, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, reversal, f.club.Treasurer.ID)
	var rev *ReversalEntryError
	if !errors.Is(err, ErrReversalEntry) || !errors.As(err, &rev) || rev.OriginalID != orig {
		t.Fatalf("void of the reversal: err = %v, want ReversalEntryError naming %s", err, orig)
	}
	if got := f.status(t, orig); got != "voided" {
		t.Errorf("original is %s after the refused void, want voided", got)
	}
	if got := f.status(t, reversal); got != "posted" {
		t.Errorf("reversal is %s after the refused void, want posted", got)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != entries {
		t.Errorf("refused void wrote %d entries", n-entries)
	}
	if got := f.net(t, f.club, "1920"); got != bank {
		t.Errorf("1920 net moved from %v to %v", bank, got)
	}

	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, other, f.club.Treasurer.ID); err != nil {
		t.Errorf("a normal entry must still void: %v", err)
	}
}
