package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A Vipps charge booked through the per-charge model is one leg of its
// settlement's clearing: DR 2900 for the charge, CR 2900 for the fee and the
// net payout once the settlement is cleared (ClearVippsPayout). Taking a
// posted charge booking back after that voids the charge's debit while the
// fee and payout credits stay, so 2900 stays off by the charge until the
// charge is booked again. The paths that unbook a charge therefore refuse
// once the charge's settlement is cleared, and say how to undo the clearing
// first. rebookVippsChargeTx is the sanctioned way to change what a posted
// charge books while its settlement stays cleared (vipps_rebook.go).
//
// The check and the clearing serialise on the settlement's payout rows: the
// unbook locks them FOR UPDATE before it looks, and ClearVippsPayout locks
// them before it counts the unposted charges and holds them to its commit.
// Whichever comes second sees the other's committed result.

// ErrVippsChargeInClearedSettlement matches the refusal to take back a charge
// booking whose settlement is already cleared against the bank deposit. The
// handlers answer 409 charge_in_cleared_settlement; the concrete error is a
// *ChargeInClearedSettlementError naming the settlement and its deposit.
var ErrVippsChargeInClearedSettlement = errors.New("charge belongs to a cleared Vipps settlement")

// ChargeInClearedSettlementError names the cleared settlement that holds the
// charge and the bank deposit that cleared it (BankRowID, empty when no bank
// row carries the clearing entry). Its text is a full Norwegian sentence
// shown to the treasurer as is.
type ChargeInClearedSettlementError struct {
	SettlementNumber string
	MSN              string
	BankRowID        string
}

func (e *ChargeInClearedSettlementError) Error() string {
	return fmt.Sprintf(
		"Vipps-betalingen tilhører oppgjør %[1]s (Vippsnr %[2]s), som allerede er avstemt mot bankinnbetalingen. "+
			"Angre først avstemmingen av «Utb. %[1]s Vippsnr %[2]s» (utbetalingsbilaget annulleres og innbetalingen legges tilbake i Tildel; "+
			"Vipps-avgiften forblir bokført), så kan du angre betalingen. "+
			"Oppgjøret avstemmes på nytt ved neste synkronisering når alle betalingene i det er bokført.",
		e.SettlementNumber, e.MSN)
}

// Is makes errors.Is(err, ErrVippsChargeInClearedSettlement) hold.
func (e *ChargeInClearedSettlementError) Is(target error) bool {
	return target == ErrVippsChargeInClearedSettlement
}

// settlementPayoutRow is an imported payout row of a settlement.
type settlementPayoutRow struct {
	id, number string
	entryID    *string
}

// lockSettlementPayoutsTx locks, in id order, the imported payout rows of the
// settlement that msn and booking date name (charges and fees carry no
// settlement number, only these two). It is the one lock the payout clearing
// and every unbook of a charge in that settlement serialise on, taken before
// any entry lock so the two orders cannot meet in a cycle.
func lockSettlementPayoutsTx(ctx context.Context, tx pgx.Tx, clubID, msn string, booking time.Time) ([]settlementPayoutRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, COALESCE(settlement_number, ''), journal_entry_id::text
		   FROM vipps_import_rows
		  WHERE club_id = $1 AND msn = $2 AND row_type = 'payout' AND booking_date = $3::date
		  ORDER BY id
		  FOR UPDATE`,
		clubID, msn, booking)
	if err != nil {
		return nil, fmt.Errorf("locking settlement payouts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (settlementPayoutRow, error) {
		var p settlementPayoutRow
		err := r.Scan(&p.id, &p.number, &p.entryID)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("locking settlement payouts: %w", err)
	}
	return out, nil
}

// postedSettlementClearing reports whether the settlement has a posted
// clearing in the ledger: its payout row linked to a posted entry, or a bank
// deposit naming it booked on a posted entry. bankRowID is the deposit that
// carries it, "" when none is found. Unlike settlementClearedElsewhere, which
// refuses a second clearing and so counts any non-voided entry, a draft is
// not a clearing here: a draft payout leaves the ledger without the credit
// that a posted charge would be unbooked against.
func postedSettlementClearing(ctx context.Context, q dbtx, clubID, settlement, msn string, payoutEntry *string) (bankRowID string, cleared bool, err error) {
	if payoutEntry != nil {
		if err := q.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM journal_entries WHERE id = $1 AND club_id = $2 AND status = 'posted')`,
			*payoutEntry, clubID,
		).Scan(&cleared); err != nil {
			return "", false, fmt.Errorf("checking payout entry: %w", err)
		}
		if cleared {
			err := q.QueryRow(ctx,
				`SELECT id::text FROM bank_import_rows WHERE club_id = $1 AND journal_entry_id = $2 ORDER BY id LIMIT 1`,
				clubID, *payoutEntry,
			).Scan(&bankRowID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", false, fmt.Errorf("finding the deposit: %w", err)
			}
			if bankRowID != "" {
				return bankRowID, true, nil
			}
		}
	}
	rows, err := q.Query(ctx,
		`SELECT b.id::text, b.description FROM bank_import_rows b
		   JOIN journal_entries je ON je.id = b.journal_entry_id AND je.club_id = b.club_id
		  WHERE b.club_id = $1 AND je.status = 'posted'
		    AND b.description ILIKE '%' || $2 || '%'
		  ORDER BY b.id`,
		clubID, settlement,
	)
	if err != nil {
		return "", false, fmt.Errorf("checking booked deposits: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, desc string
		if err := rows.Scan(&id, &desc); err != nil {
			return "", false, fmt.Errorf("scan booked deposit: %w", err)
		}
		if m := VippsSettlementPattern.FindStringSubmatch(desc); len(m) == 3 && m[1] == settlement && m[2] == msn {
			return id, true, nil
		}
	}
	return "", cleared, rows.Err()
}

// clearedSettlementOfChargeTx reports the settlement a charge was paid out in
// when that settlement has a posted clearing (postedSettlementClearing), or
// nil when the charge's settlement is not imported or not cleared. It locks
// the settlement's payout rows FOR UPDATE and holds them to the end of tx, so
// a payout clearing cannot slip in between this answer and the caller's
// change.
func clearedSettlementOfChargeTx(ctx context.Context, tx pgx.Tx, clubID, rowID string) (*ChargeInClearedSettlementError, error) {
	var msn string
	var booking *time.Time
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(msn, ''), booking_date FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2 AND row_type = 'belastning'`,
		rowID, clubID,
	).Scan(&msn, &booking)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading charge settlement: %w", err)
	}
	// ClearVippsPayout groups charges on msn + booking date, so a charge
	// without either is in no settlement.
	if msn == "" || booking == nil {
		return nil, nil
	}
	payouts, err := lockSettlementPayoutsTx(ctx, tx, clubID, msn, *booking)
	if err != nil {
		return nil, err
	}
	for _, p := range payouts {
		if p.number == "" {
			continue
		}
		bankRowID, cleared, err := postedSettlementClearing(ctx, tx, clubID, p.number, msn, p.entryID)
		if err != nil {
			return nil, err
		}
		if cleared {
			return &ChargeInClearedSettlementError{SettlementNumber: p.number, MSN: msn, BankRowID: bankRowID}, nil
		}
	}
	return nil, nil
}

// chargeFakturaIDsTx returns, in id order, the fakturas a charge booking may
// have settled: the one recorded on the charge (invoice_id) and the ones its
// checkout order pays through order_sources (an order charge records no
// invoice_id).
func chargeFakturaIDsTx(ctx context.Context, q dbtx, clubID, rowID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT id FROM (
		     SELECT r.invoice_id::text AS id FROM vipps_import_rows r
		      WHERE r.id = $1 AND r.club_id = $2 AND r.invoice_id IS NOT NULL
		     UNION
		     SELECT os.invoice_id::text FROM vipps_import_rows r
		       JOIN orders o ON o.club_id = r.club_id AND o.vipps_reference = r.order_id AND o.vipps_reference <> ''
		       JOIN order_sources os ON os.order_id = o.id
		      WHERE r.id = $1 AND r.club_id = $2) f
		  ORDER BY id`,
		rowID, clubID)
	if err != nil {
		return nil, fmt.Errorf("loading the charge's fakturas: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("loading the charge's fakturas: %w", err)
	}
	return ids, nil
}

// lockFakturasTx locks the fakturas (given in id order) for the rest of tx:
// fakturas come first in the lock order every settle path shares.
func lockFakturasTx(ctx context.Context, tx pgx.Tx, clubID string, ids []string) error {
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE`, id, clubID); err != nil {
			return fmt.Errorf("locking invoice: %w", err)
		}
	}
	return nil
}

// reopenUnbookedFakturasTx reopens each faktura that reads as paid while no
// booked row settles it any more (invoiceBookedRowSQL), after the charge that
// settled it was unlinked from its entry. A faktura another booked row still
// settles stays paid.
func reopenUnbookedFakturasTx(ctx context.Context, tx pgx.Tx, clubID string, ids []string) error {
	for _, id := range ids {
		var reopen bool
		if err := tx.QueryRow(ctx,
			`SELECT i.payment_id IS NOT NULL AND NOT `+invoiceBookedRowSQL("i")+`
			   FROM invoices i WHERE i.id = $1 AND i.club_id = $2`,
			id, clubID,
		).Scan(&reopen); err != nil {
			return fmt.Errorf("checking invoice: %w", err)
		}
		if !reopen {
			continue
		}
		if _, err := reopenInvoicePaymentTx(ctx, tx, clubID, id); err != nil {
			return err
		}
	}
	return nil
}

// VippsSettlementEntryError is the refusal to void a Vipps settlement's fee
// or payout entry from the ledger. The handlers answer 409
// vipps_settlement_entry. Its text is a full Norwegian sentence.
type VippsSettlementEntryError struct {
	// RowType is "fee" or "payout".
	RowType string
}

func (e *VippsSettlementEntryError) Error() string {
	if e.RowType == "payout" {
		return "Bilaget er utbetalingsbilaget for et Vipps-oppgjør og kan ikke annulleres her. " +
			"Angre i stedet en av betalingene i oppgjøret og velg «Angre innbetalingen»."
	}
	return "Bilaget bokfører Vipps-avgiften for et oppgjør og kan ikke annulleres alene: avgiften ville stå igjen uten bilag " +
		"og bokføres ikke på nytt. Det finnes ingen angre for den i appen; rett den med et nytt manuelt bilag."
}

// Is makes errors.Is(err, ErrVippsSettlementEntry) hold.
func (e *VippsSettlementEntryError) Is(target error) bool { return target == ErrVippsSettlementEntry }

// ErrVippsSettlementEntry matches a *VippsSettlementEntryError.
var ErrVippsSettlementEntry = errors.New("the entry books a Vipps settlement's fee or payout")

// lockBankRowSettlementsTx locks the payout rows of every Vipps settlement
// whose payout row is linked to the entry (the clearing entry of a Vipps
// deposit), without locking the entry first.
func lockBankRowSettlementsTx(ctx context.Context, tx pgx.Tx, clubID, entryID string) error {
	type key struct {
		msn     string
		booking time.Time
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT msn, booking_date FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'payout' AND journal_entry_id = $2::uuid
		    AND COALESCE(msn, '') <> '' AND booking_date IS NOT NULL
		  ORDER BY msn, booking_date`,
		clubID, entryID)
	if err != nil {
		return fmt.Errorf("finding the deposit's settlement: %w", err)
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (key, error) {
		var k key
		err := r.Scan(&k.msn, &k.booking)
		return k, err
	})
	if err != nil {
		return fmt.Errorf("finding the deposit's settlement: %w", err)
	}
	for _, k := range keys {
		if _, err := lockSettlementPayoutsTx(ctx, tx, clubID, k.msn, k.booking); err != nil {
			return err
		}
	}
	return nil
}

// refuseVippsSettlementEntryTx returns a *VippsSettlementEntryError when a
// Vipps fee or payout row points at the entry.
func refuseVippsSettlementEntryTx(ctx context.Context, tx pgx.Tx, clubID, entryID string) error {
	var rowType string
	err := tx.QueryRow(ctx,
		`SELECT row_type FROM vipps_import_rows
		  WHERE club_id = $1 AND journal_entry_id = $2 AND row_type IN ('fee', 'payout') ORDER BY row_type LIMIT 1`,
		clubID, entryID,
	).Scan(&rowType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking settlement rows: %w", err)
	}
	return &VippsSettlementEntryError{RowType: rowType}
}

// releaseVoidedChargeTx releases a Vipps charge whose entry the ledger just
// voided: unlinked, it is back in the Vipps queue, and the fakturas it settled
// are reopened. Left linked it would read as booked for ever (no queue lists
// it, its settlement could not clear) and keep those fakturas paid.
func releaseVoidedChargeTx(ctx context.Context, tx pgx.Tx, clubID, rowID, entryID string, fakturas []string) error {
	tag, err := tx.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = NULL, invoice_id = NULL, auto_assign_rule = NULL
		  WHERE id = $1 AND club_id = $2 AND journal_entry_id = $3`,
		rowID, clubID, entryID)
	if err != nil {
		return fmt.Errorf("releasing the vipps charge: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("the Vipps charge changed while its bilag was being voided — try again")
	}
	return reopenUnbookedFakturasTx(ctx, tx, clubID, fakturas)
}
