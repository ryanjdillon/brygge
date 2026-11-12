package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A person's undo is final for the automatic passes (ACC-13(b), BRY-565).
//
// When a person takes a booking back (UnassignVippsCharge, the ledger void of
// a charge entry, UnassignBankRow and every row it releases with it), the row
// gets auto_declined_at = now() in the undo's own transaction. Every automatic
// pass leaves such a row alone, twice over:
//
//   - in its candidate handling: it reads the marker and skips the row, and
//     counts it as declined where the pass reports counts (DeclinedSkipped,
//     the "declined" reason of bank auto-assign, a skipped item of the
//     payout sweeps);
//   - in its guarded link: the UPDATE that books the row adds
//     auto_declined_at IS NULL, so a pass that read the row before a person
//     undid it cannot book it after. A guard lost that way is errAutoDeclined,
//     reported as declined, never as an error.
//
// The marker is lifted by a booking, never by an automatic release:
//
//   - every statement that links a row to an entry sets auto_declined_at back
//     to NULL. Automatic links can only reach unmarked rows, so in practice
//     this is a person's booking. A path that links, posts and unwinds on a
//     failed post outside one transaction (postVippsChargeSplit) lifts it
//     only after the post succeeded, so a failed booking leaves the marker as
//     it was; the bank-row bookings lift it on their own transaction
//     (linkAndPostBankRowsTx), which a failed post rolls back;
//   - a person booking a charge in a Vipps settlement lifts it from that
//     settlement's payout deposit (liftSettlementDepositDecline, ACC-11), so
//     the sweep clears the settlement again once its charges are booked.
//
// Automatic releases (healStrandedKIDDrafts, a failed post's unwind, the
// regenerate and resync detaches) never set it.

// errAutoDeclined is an automatic pass finding that a person undid the row's
// booking: it waits for a person, and the pass counts it as declined.
var errAutoDeclined = errors.New("a person undid this booking; the automatic passes leave the row for a person")

// bookingMode is who a booking is for. It is threaded explicitly through the
// Vipps posting helpers and the legacy confirm, so the automatic passes are
// told apart by auto, never by their period policy.
type bookingMode struct {
	// policy says whether the booking may open the fiscal year of its date.
	policy periodPolicy
	// auto is an automatic pass: it refuses a row a person's undo declined
	// (errAutoDeclined), in its guarded link as well as up front.
	auto bool
}

var (
	// personBooking is a person's booking in Tildel (or a supervised tool
	// a person runs): it may open the year and books a declined row.
	personBooking = bookingMode{policy: periodMayCreate}
	// automaticBooking is an automatic pass: an existing open period only,
	// and never a row a person declined.
	automaticBooking = bookingMode{policy: periodExistingOnly, auto: true}
)

// Reasons shown for a declined row (Norwegian, shown as is).
const (
	// declinedDepositReason is the Vipps payout sweep's reason for a deposit
	// whose clearing a person undid ("Angre innbetalingen").
	// The manual clearing is Tildel's «Utlign utbetaling» (ClearVippsPayout).
	declinedDepositReason = "innbetalingen ble angret av en person – bokfør en betaling i oppgjøret på nytt, eller avstem den selv med «Utlign utbetaling» i Tildel"
	// stripeReasonDeclined is the Stripe payout sweep's reason for a deposit
	// whose clearing a person undid.
	stripeReasonDeclined = "innbetalingen ble angret av en person – avstem den selv med «Avstem mot Stripe-utbetaling» i Tildel"
)

// liftBankRowsDeclineIn clears the marker on every bank row booked on entryID.
// It runs after the entry is posted (on the booking's transaction, or on the
// pool after a pool-backed post), so a failed booking never loses a person's
// decline. A Vipps charge's lift is chargePosting.liftSQL.
func liftBankRowsDeclineIn(ctx context.Context, q dbtx, clubID, entryID string) error {
	if _, err := q.Exec(ctx,
		`UPDATE bank_import_rows SET auto_declined_at = NULL
		  WHERE club_id = $1 AND journal_entry_id = $2 AND auto_declined_at IS NOT NULL`,
		clubID, entryID,
	); err != nil {
		return fmt.Errorf("lifting the rows' decline: %w", err)
	}
	return nil
}

// bankRowDeclinedIn reports whether any of the bank rows carries the marker.
// A guarded link that lost reads it to tell a person's undo from a booking.
func bankRowDeclinedIn(ctx context.Context, q dbtx, clubID string, rowIDs []string) (bool, error) {
	var declined bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bank_import_rows
		                 WHERE club_id = $1 AND id = ANY($2::uuid[]) AND auto_declined_at IS NOT NULL)`,
		clubID, rowIDs,
	).Scan(&declined); err != nil {
		return false, fmt.Errorf("reading the rows' decline: %w", err)
	}
	return declined, nil
}

// liftSettlementDepositDecline lifts a person's decline from the payout
// deposit of the settlement a charge was paid out in, after a person booked
// that charge (ACC-11: once its charges are booked, the next sweep clears the
// settlement again). It runs in its own short transaction after the booking
// committed, taking the settlement's payout-row lock (lockSettlementPayoutsTx,
// the lock the clearing takes) before it touches a bank row, and never while
// a faktura lock is held. Idempotent; a charge without MSN or booking date is
// in no settlement and lifts nothing. A failure is logged: the booking stands,
// and the deposit can still be cleared by hand (ClearVippsPayout, Tildel's
// «Utlign utbetaling»).
//
// Callers: a person's Tildel booking of a charge (CreateVippsChargeBilag,
// AssignVippsChargeToInvoice), the backlog apply's charge posts and rebooks,
// and the fallback reclass. Never an automatic pass, and never the supervised
// cutover's regenerate.
func (s *Service) liftSettlementDepositDecline(ctx context.Context, clubID, chargeRowID string) {
	if err := s.liftSettlementDepositDeclineErr(ctx, clubID, chargeRowID); err != nil {
		s.log.Warn().Err(err).Str("vipps_row_id", chargeRowID).
			Msg("lifting the settlement deposit's decline failed; clear the deposit by hand (Utlign utbetaling)")
	}
}

func (s *Service) liftSettlementDepositDeclineErr(ctx context.Context, clubID, chargeRowID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var msn string
	var booking *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(msn, ''), booking_date FROM vipps_import_rows
		  WHERE id = $1 AND club_id = $2 AND row_type = 'belastning'`,
		chargeRowID, clubID,
	).Scan(&msn, &booking); errors.Is(err, pgx.ErrNoRows) {
		// Not a charge (a backlog draft of another row type): nothing to lift.
		return nil
	} else if err != nil {
		return fmt.Errorf("loading the charge's settlement: %w", err)
	}
	if msn == "" || booking == nil {
		return nil
	}
	payouts, err := lockSettlementPayoutsTx(ctx, tx, clubID, msn, *booking)
	if err != nil {
		return err
	}
	var ids []string
	for _, p := range payouts {
		if p.number == "" {
			continue
		}
		rows, err := tx.Query(ctx,
			`SELECT id::text, description FROM bank_import_rows
			  WHERE club_id = $1 AND journal_entry_id IS NULL AND auto_declined_at IS NOT NULL
			    AND description ILIKE '%' || $2 || '%'`,
			clubID, p.number,
		)
		if err != nil {
			return fmt.Errorf("finding the declined deposit: %w", err)
		}
		for rows.Next() {
			var id, desc string
			if err := rows.Scan(&id, &desc); err != nil {
				rows.Close()
				return fmt.Errorf("scanning the declined deposit: %w", err)
			}
			// The same match postedSettlementClearing uses: the settlement
			// number and the MSN, parsed in Go.
			if m := VippsSettlementPattern.FindStringSubmatch(desc); len(m) == 3 && m[1] == p.number && m[2] == msn {
				ids = append(ids, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("finding the declined deposit: %w", err)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET auto_declined_at = NULL
		  WHERE club_id = $1 AND id = ANY($2::uuid[]) AND journal_entry_id IS NULL`,
		clubID, ids,
	); err != nil {
		return fmt.Errorf("lifting the deposit's decline: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
