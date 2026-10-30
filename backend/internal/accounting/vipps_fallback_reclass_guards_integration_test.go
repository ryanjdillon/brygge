package accounting

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The guards inside the charge transaction, each exercised on its own so
// removing one fails a test.

// plannedCharge parks a charge and returns it as the plan holds it.
func (f *fallbackFixture) plannedCharge(t *testing.T) (*fallbackCharge, string) {
	t.Helper()
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	row := f.parked(t, 80000, "Guard Payer")
	charges, err := f.svc.loadFallbackCharges(context.Background(), f.club.ID, "")
	if err != nil || len(charges) != 1 {
		t.Fatalf("charges = %v, %v", charges, err)
	}
	return charges[0], row
}

func (f *fallbackFixture) reclassify(c *fallbackCharge) error {
	_, err := f.svc.reclassifyFallbackCharge(context.Background(), f.club.ID, f.club.Treasurer.ID,
		vippsClearingAccountCode, c, "3200", "Gjesteplass synthetic")
	return err
}

// The apply takes the charge row's lock before anything else. A concurrent
// holder of that row (an unassign, which locks the charge row before the
// entry) makes the apply wait instead of racing it; taking the entry first
// would invert that order.
func TestFallbackReclassWaitsForTheChargeRowLock(t *testing.T) {
	f := newFallbackFixture(t)
	ctx := context.Background()
	c, row := f.plannedCharge(t)
	targets := map[string]string{c.entryID: "3200"}

	holder, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM vipps_import_rows WHERE id = $1 FOR UPDATE`, row); err != nil {
		t.Fatal(err)
	}

	done := make(chan *VippsFallbackReclassResult, 1)
	go func() {
		res, err := f.svc.ApplyVippsFallbackReclass(ctx, f.club.ID, f.club.Treasurer.ID, "", targets)
		if err != nil {
			t.Errorf("apply: %v", err)
		}
		done <- res
	}()
	select {
	case <-done:
		t.Fatal("the apply finished while another transaction held the charge row")
	case <-time.After(700 * time.Millisecond):
	}
	if f.rowEntry(t, row) != c.entryID {
		t.Fatal("the apply changed the charge while its row was locked")
	}
	// Row first, entry second: blocked on the row, the apply has not touched
	// the entry, so another transaction can still take it at once. An apply
	// that took the entry first would hold it here and fail this.
	probe, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(ctx, `SELECT 1 FROM journal_entries WHERE id = $1 FOR UPDATE NOWAIT`, c.entryID); err != nil {
		t.Errorf("the apply holds the entry while waiting for the charge row (lock order is row, then entry): %v", err)
	}
	_ = probe.Rollback(ctx)

	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res == nil || res.Reclassified != 1 {
			t.Errorf("apply after the lock was released = %+v, want 1 reclassified", res)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the apply did not resume after the row lock was released")
	}
}

// The entry's shape is read again under the lock: an entry that gained lines
// since the plan is no longer a single debit and credit, and is left alone.
func TestFallbackReclassRechecksTheEntryShapeInTheTransaction(t *testing.T) {
	f := newFallbackFixture(t)
	c, row := f.plannedCharge(t)
	f.exec(t,
		`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit)
		 SELECT $1::uuid, a.id, 10, 0 FROM accounts a WHERE a.club_id = $2 AND a.code = '1930'`, c.entryID, f.club.ID)
	f.exec(t,
		`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit)
		 SELECT $1::uuid, a.id, 0, 10 FROM accounts a WHERE a.club_id = $2 AND a.code = '3300'`, c.entryID, f.club.ID)
	entries := f.entryCount(t)

	// c still carries the lines the plan read, so only the transaction's own
	// re-read can see the change.
	if err := f.reclassify(c); !errors.Is(err, errFallbackMoved) {
		t.Fatalf("reclassify = %v, want errFallbackMoved", err)
	}
	if f.entryCount(t) != entries || f.entryStatus(t, c.entryID) != "posted" || f.rowEntry(t, row) != c.entryID {
		t.Errorf("a refused reclass changed the books")
	}
}

// A relink that affects no row (the helper's guarded mark, or the rule
// update after it) rolls the whole charge back: the original is not left
// voided, and no replacement is left unlinked.
func TestFallbackReclassRollsBackWhenTheRelinkAffectsNoRow(t *testing.T) {
	f := newFallbackFixture(t)
	c, row := f.plannedCharge(t)
	f.exec(t, `CREATE FUNCTION skip_vipps_row_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`)
	f.exec(t, `CREATE TRIGGER skip_vipps_row_update BEFORE UPDATE ON vipps_import_rows
	           FOR EACH ROW EXECUTE FUNCTION skip_vipps_row_update()`)
	entries := f.entryCount(t)

	if err := f.reclassify(c); err == nil {
		t.Fatal("reclassify succeeded although no row could be relinked")
	}
	if f.entryCount(t) != entries || f.entryStatus(t, c.entryID) != "posted" || f.rowEntry(t, row) != c.entryID {
		t.Errorf("a charge whose relink matched no row was left half moved")
	}
}
