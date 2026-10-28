package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A person clears a Stripe payout the sweep leaves alone: a manual payout,
// an ambiguous match, members that do not add up to the payout (the funds
// of a failed payout carried into a later one), a deposit outside the date
// window. The booking is the sweep's (clearStripePayout with
// manualStripeClear), so a manual clearing is undone by UnassignBankRow the
// same way and the payout can be cleared again.

// ErrStripePayoutNotFound is a payout id that is not the club's.
var ErrStripePayoutNotFound = errors.New("stripe payout not found")

// StripeClearRefusal is a manual clearing refused, nothing booked. Code is
// one of the exported StripeClear* codes; Reason is Norwegian and shown as
// is (for members_open it lists what is still open).
type StripeClearRefusal struct {
	Code   string
	Reason string
	// OpenMembers counts the payout's members not booked or dismissed.
	OpenMembers int
	// OpenBalance counts, for balance_open, the open balance rows that
	// could have funded a manual payout.
	OpenBalance int
}

func (e *StripeClearRefusal) Error() string {
	if e.Reason == "" {
		return "stripe payout not cleared: " + e.Code
	}
	return "stripe payout not cleared: " + e.Code + ": " + e.Reason
}

// StripePayoutClearResult is what ClearStripePayoutManually booked.
type StripePayoutClearResult struct {
	// PayoutRowID is the stripe_payouts row; PayoutID is Stripe's id.
	PayoutRowID    string `json:"id"`
	PayoutID       string `json:"payout_id"`
	BankRowID      string `json:"bank_row_id"`
	JournalEntryID string `json:"journal_entry_id"`
	// MembersCount is 0 for a manual payout: Stripe does not list what it
	// paid out, and ClearingBalanceOre is then left out.
	MembersCount int `json:"members_count"`
	// MembersNetOre is the net of the payout's members, which may differ
	// from the payout for a payout the sweep would not clear.
	MembersNetOre int64 `json:"members_net_ore"`
	// ClearingBalanceOre is the clearing account remainder over the
	// members and the new entry; non-zero when they do not net to the
	// payout. Nil (absent) for a payout without members, and when it could
	// not be read (the clearing stands).
	ClearingBalanceOre *int64 `json:"clearing_balance_ore,omitempty"`
}

// ClearStripePayoutManually books the deposit bankRowID as the payout
// payoutRowID (DR the import's bank account / CR the Stripe clearing
// account, the payout amount, on the bank date), as actorID. Compared with
// the sweep it does not require an automatic payout, members that add up
// to the payout, or a bank date in the arrival window: the person chose
// the pair. It still requires an eligible deposit that carries the club's
// Stripe descriptor and equals the payout to the øre, a paid, uncleared
// NOK payout, its members listed (automatic) and every one booked or
// dismissed, or for a manual payout every balance row that could have
// funded it booked or dismissed, and an open fiscal period for the bank
// date (never created). A refusal is a
// *StripeClearRefusal; a missing row or payout is ErrBankRowNotFound or
// ErrStripePayoutNotFound. It waits briefly for the club's Stripe slot
// (manualClearSlotWait), then gives up with ErrStripeSyncBusy.
func (s *Service) ClearStripePayoutManually(ctx context.Context, clubID, actorID, payoutRowID, bankRowID string) (*StripePayoutClearResult, error) {
	if actorID == "" {
		return nil, errors.New("clearing a stripe payout needs an acting user")
	}
	release, err := s.acquireStripeSlotBriefly(ctx, clubID)
	if err != nil {
		return nil, err
	}
	defer release()

	out, err := s.clearStripePayout(ctx, clubID, actorID, payoutRowID, bankRowID, manualStripeClear)
	if err != nil {
		return nil, err
	}
	switch {
	case out.code == stripeClearRowNotFound:
		return nil, ErrBankRowNotFound
	case out.code == stripeClearPayoutNotFound:
		return nil, ErrStripePayoutNotFound
	case out.state != stripePlanReady:
		code := out.code
		if code == stripeClearRaced || code == "" {
			// The row and payout are locked before the guarded links, so
			// this is a concurrent write the locks did not cover; the
			// payout is no longer what the person saw.
			code = StripeClearPayoutNotClearable
		}
		return nil, &StripeClearRefusal{Code: code, Reason: out.reason, OpenMembers: out.members.open(), OpenBalance: out.openBalance}
	}
	// Without members the remainder is only the payout itself, so it says
	// nothing; it is left out rather than reported as a discrepancy.
	if out.members.count == 0 {
		out.clearingBalanceOre = nil
	}
	return &StripePayoutClearResult{
		PayoutRowID:        payoutRowID,
		PayoutID:           out.payoutID,
		BankRowID:          bankRowID,
		JournalEntryID:     out.entryID,
		MembersCount:       out.members.count,
		MembersNetOre:      out.members.netOre,
		ClearingBalanceOre: out.clearingBalanceOre,
	}, nil
}

// manualClearSlotWait bounds how long a manual clearing waits for the
// club's Stripe slot: a booking pass or sweep usually lets go within
// seconds, a full sync may not, and a person should not wait on it.
var manualClearSlotWait = 5 * time.Second

// acquireStripeSlotBriefly takes the club's Stripe slot, waiting at most
// manualClearSlotWait; ErrStripeSyncBusy when it is still held.
func (s *Service) acquireStripeSlotBriefly(ctx context.Context, clubID string) (func(), error) {
	wait, cancel := context.WithTimeout(ctx, manualClearSlotWait)
	defer cancel()
	release, err := s.stripeSync.acquire(wait, clubID)
	if err == nil {
		return release, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, ErrStripeSyncBusy
}

// StripePayoutSummary is an uncleared payout as the clearing picker lists it.
type StripePayoutSummary struct {
	ID          string  `json:"id"`
	PayoutID    string  `json:"payout_id"`
	Amount      float64 `json:"amount"`
	AmountOre   int64   `json:"amount_ore"`
	ArrivalDate string  `json:"arrival_date"`
	Automatic   bool    `json:"automatic"`
	// MembersListed is false for a manual payout and an automatic one
	// whose members Stripe has not listed yet.
	MembersListed bool `json:"members_listed"`
	MembersCount  int  `json:"members_count"`
	// OpenMembers counts members neither booked nor dismissed; a payout
	// with any cannot be cleared.
	OpenMembers   int   `json:"open_members"`
	MembersNetOre int64 `json:"members_net_ore"`
	// OpenBalance counts, for a manual payout, the open balance rows that
	// could have funded it (linked to no payout, available by its
	// arrival); a manual payout with any cannot be cleared. 0 for an
	// automatic payout.
	OpenBalance int `json:"open_balance"`
}

// ListUnclearedStripePayouts lists the club's paid NOK payouts not yet
// cleared, newest arrival first, with their members' counts and net.
func (s *Service) ListUnclearedStripePayouts(ctx context.Context, clubID string) ([]StripePayoutSummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.payout_id, p.amount_ore, p.arrival_date, p.automatic, p.members_listed_at IS NOT NULL,
		        count(t.id),
		        count(t.id) FILTER (WHERE t.status NOT IN ('booked', 'dismissed')),
		        COALESCE(sum(t.net_ore), 0)::bigint,
		        CASE WHEN p.automatic THEN 0
		             ELSE (SELECT count(*) FROM (`+openUnlinkedStripeRowsSQL("p.club_id", "p.arrival_date")+`) o) END
		   FROM stripe_payouts p
		   LEFT JOIN stripe_balance_transactions t
		     ON t.club_id = p.club_id AND t.payout_id = p.id
		    AND t.reporting_category NOT IN ('payout', 'payout_reversal')
		  WHERE p.club_id = $1 AND p.status = 'paid' AND p.currency = 'nok' AND p.amount_ore > 0
		    AND p.journal_entry_id IS NULL AND p.bank_row_id IS NULL
		  GROUP BY p.id
		  ORDER BY p.arrival_date DESC, p.payout_id`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing stripe payouts: %w", err)
	}
	defer rows.Close()
	out := []StripePayoutSummary{}
	for rows.Next() {
		var p StripePayoutSummary
		var arrival time.Time
		if err := rows.Scan(&p.ID, &p.PayoutID, &p.AmountOre, &arrival, &p.Automatic, &p.MembersListed,
			&p.MembersCount, &p.OpenMembers, &p.MembersNetOre, &p.OpenBalance); err != nil {
			return nil, fmt.Errorf("scanning stripe payout: %w", err)
		}
		p.Amount = oreToKr(p.AmountOre)
		p.ArrivalDate = arrival.Format("2006-01-02")
		out = append(out, p)
	}
	return out, rows.Err()
}
