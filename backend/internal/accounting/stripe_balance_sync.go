package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// StripeBalanceSource is the part of the Stripe API the balance ingest
// reads. *stripe.Client implements it.
type StripeBalanceSource interface {
	ListBalanceTransactions(ctx context.Context, createdGTE time.Time, startingAfter string) (*stripe.BalanceTransactionPage, error)
	ListPayoutTransactions(ctx context.Context, payoutID, startingAfter string) (*stripe.BalanceTransactionPage, error)
	GetPayout(ctx context.Context, id string) (*stripe.Payout, error)
	GetPaymentIntent(ctx context.Context, id string) (*stripe.PaymentIntent, error)
}

var _ StripeBalanceSource = (*stripe.Client)(nil)

// Booking states of a stripe_balance_transactions row.
const (
	StripeTxnUnbooked    = "unbooked"
	StripeTxnBooked      = "booked"
	StripeTxnNeedsReview = "needs_review"
	StripeTxnDismissed   = "dismissed"
)

// Why a Stripe balance transaction waits for a person
// (stripe_balance_transactions.review_reason).
const (
	// StripeReviewNoAttempt: a charge Brygge cannot tie to a Stripe payment
	// attempt of the club, by metadata or through its PaymentIntent.
	StripeReviewNoAttempt = "no_attempt"
	// StripeReviewAmountMismatch: the charge differs from what its attempt
	// was created to take.
	StripeReviewAmountMismatch = "amount_mismatch"
	// StripeReviewCurrency: not NOK, or not the attempt's currency.
	StripeReviewCurrency = "currency"
	// StripeReviewRefundPending: refunds, disputes and their reversals, and
	// Stripe's other balance adjustments. None has a booking yet; it comes
	// with Stripe refunds.
	StripeReviewRefundPending = "refund_pending_bry446"
	// StripeReviewUnsupportedCategory: a reporting category Brygge has no
	// booking for. Stored as other_adjustment so the row is not lost.
	StripeReviewUnsupportedCategory = "unsupported_category"
)

// stripeCursorOverlap is how far before the recorded watermark a sync
// starts again. Re-reading is free (rows are upserted), and the overlap
// covers transactions Stripe makes visible after later ones.
const stripeCursorOverlap = 3 * 24 * time.Hour

// stripeFirstSyncLookback is where the first sync of a club with no Stripe
// payment attempts starts.
const stripeFirstSyncLookback = 30 * 24 * time.Hour

// ErrStripeSyncBusy is returned by TrySyncStripeBalance while another
// Stripe ingest for the club is running.
var ErrStripeSyncBusy = errors.New("a stripe balance sync is already running")

// StripeSyncResult summarises one Stripe balance ingest. Per-row failures
// are collected in Errors and do not stop the rest of the run.
type StripeSyncResult struct {
	// Fetched counts the balance transactions read from the balance list.
	Fetched int `json:"fetched"`
	// Inserted counts rows new to Brygge, payout members included.
	Inserted int `json:"inserted"`
	// NeedsReview counts, by review_reason, the rows this run inserted or
	// reclassified into needs_review.
	NeedsReview map[string]int `json:"needs_review"`
	// PayoutsSeen counts payouts refreshed from Stripe.
	PayoutsSeen int `json:"payouts_seen"`
	// PayoutsLinked counts automatic payouts whose membership was read in
	// full; MembersLinked counts the rows that gained their payout this run.
	PayoutsLinked int      `json:"payouts_linked"`
	MembersLinked int      `json:"members_linked"`
	Errors        []string `json:"errors,omitempty"`

	// incomplete is set when something was not stored (a row, a payout), so
	// the watermark must not move past it. A row stored for review after a
	// failed lookup does not count: it is in the table.
	incomplete bool
}

func newStripeSyncResult() StripeSyncResult {
	return StripeSyncResult{NeedsReview: map[string]int{}}
}

func (r *StripeSyncResult) fail(id string, err error) {
	r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", id, err))
}

// stripeSyncGuard lets one Stripe ingest run per club at a time within the
// process. A run calls Stripe for minutes at most and holds no database
// lock meanwhile, so the guard is in memory rather than in Postgres.
type stripeSyncGuard struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func (g *stripeSyncGuard) slot(clubID string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.slots == nil {
		g.slots = map[string]chan struct{}{}
	}
	ch, ok := g.slots[clubID]
	if !ok {
		ch = make(chan struct{}, 1)
		g.slots[clubID] = ch
	}
	return ch
}

// tryAcquire takes the club's slot if it is free.
func (g *stripeSyncGuard) tryAcquire(clubID string) (release func(), ok bool) {
	ch := g.slot(clubID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

// acquire waits for the club's slot until ctx ends.
func (g *stripeSyncGuard) acquire(ctx context.Context, clubID string) (release func(), err error) {
	ch := g.slot(clubID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SyncStripeBalance mirrors the club's Stripe balance transactions into
// stripe_balance_transactions and its payouts into stripe_payouts. It posts
// nothing to the journal: rows are classified for booking and payouts are
// linked to the transactions they paid out. It waits while another Stripe
// ingest for the club runs.
//
// The whole listing is read first and stored oldest first. The watermark
// it starts from next time moves only when the listing completed and every
// row and payout was stored, so a run that failed part-way is read again.
func (s *Service) SyncStripeBalance(ctx context.Context, src StripeBalanceSource, clubID string) (StripeSyncResult, error) {
	if src == nil {
		return newStripeSyncResult(), errors.New("stripe client not configured")
	}
	release, err := s.stripeSync.acquire(ctx, clubID)
	if err != nil {
		return newStripeSyncResult(), err
	}
	defer release()
	return s.syncStripeBalance(ctx, src, clubID)
}

// TrySyncStripeBalance is SyncStripeBalance, except that it returns
// ErrStripeSyncBusy at once while another Stripe ingest for the club runs.
func (s *Service) TrySyncStripeBalance(ctx context.Context, src StripeBalanceSource, clubID string) (StripeSyncResult, error) {
	if src == nil {
		return newStripeSyncResult(), errors.New("stripe client not configured")
	}
	release, ok := s.stripeSync.tryAcquire(clubID)
	if !ok {
		return newStripeSyncResult(), ErrStripeSyncBusy
	}
	defer release()
	return s.syncStripeBalance(ctx, src, clubID)
}

func (s *Service) syncStripeBalance(ctx context.Context, src StripeBalanceSource, clubID string) (StripeSyncResult, error) {
	res := newStripeSyncResult()
	since, err := s.stripeSyncCursor(ctx, clubID)
	if err != nil {
		return res, err
	}

	txns, listErr := listStripeBalance(ctx, src, since)
	res.Fetched = len(txns)
	sort.SliceStable(txns, func(i, j int) bool { return txns[i].Created < txns[j].Created })
	payouts := map[string]bool{}
	var newest int64
	for i := range txns {
		bt := &txns[i]
		s.ingestStripeTxnReported(ctx, src, clubID, bt, &res)
		if id := payoutOf(bt); id != "" {
			payouts[id] = true
		}
		newest = max(newest, bt.Created)
	}
	if listErr != nil {
		return res, fmt.Errorf("listing balance transactions: %w", listErr)
	}

	open, err := s.stripePayoutsToRefresh(ctx, clubID)
	if err != nil {
		return res, err
	}
	for _, id := range open {
		payouts[id] = true
	}
	for id := range payouts {
		if err := s.ingestStripePayout(ctx, src, clubID, id, &res); err != nil {
			res.fail(id, err)
			res.incomplete = true
		}
	}

	if !res.incomplete && newest > 0 {
		if err := s.advanceStripeWatermark(ctx, clubID, time.Unix(newest, 0).UTC()); err != nil {
			return res, err
		}
	}
	return res, nil
}

// listStripeBalance reads every page of the balance list from since. On an
// error it returns what it read so far with the error.
func listStripeBalance(ctx context.Context, src StripeBalanceSource, since time.Time) ([]stripe.BalanceTransaction, error) {
	var all []stripe.BalanceTransaction
	after := ""
	for {
		page, err := src.ListBalanceTransactions(ctx, since, after)
		if err != nil {
			return all, err
		}
		all = append(all, page.Data...)
		if !page.HasMore || len(page.Data) == 0 {
			return all, nil
		}
		after = page.Data[len(page.Data)-1].ID
	}
}

// IngestStripePayout refreshes one payout from Stripe and links the balance
// transactions it paid out, inserting any not yet seen. It posts nothing and
// does not move the sync watermark. It waits while another Stripe ingest
// for the club runs.
func (s *Service) IngestStripePayout(ctx context.Context, src StripeBalanceSource, clubID, payoutID string) (StripeSyncResult, error) {
	res := newStripeSyncResult()
	if src == nil {
		return res, errors.New("stripe client not configured")
	}
	release, err := s.stripeSync.acquire(ctx, clubID)
	if err != nil {
		return res, err
	}
	defer release()
	if err := s.ingestStripePayout(ctx, src, clubID, payoutID, &res); err != nil {
		return res, err
	}
	return res, nil
}

// stripeSyncCursor is where a sync starts listing: a little before the
// watermark of the last complete sync, else at the club's first Stripe
// attempt, else a fixed lookback. Stored rows do not move it: a payout's
// members can be older than anything listed so far.
func (s *Service) stripeSyncCursor(ctx context.Context, clubID string) (time.Time, error) {
	var watermark, firstAttempt *time.Time
	if err := s.db.QueryRow(ctx,
		`SELECT (SELECT listed_through FROM stripe_sync_state WHERE club_id = $1),
		        (SELECT min(created_at) FROM payment_attempts WHERE club_id = $1 AND provider = $2)`,
		clubID, stripe.ProviderName,
	).Scan(&watermark, &firstAttempt); err != nil {
		return time.Time{}, fmt.Errorf("stripe sync cursor: %w", err)
	}
	switch {
	case watermark != nil:
		return watermark.Add(-stripeCursorOverlap), nil
	case firstAttempt != nil:
		return *firstAttempt, nil
	default:
		return time.Now().Add(-stripeFirstSyncLookback), nil
	}
}

// advanceStripeWatermark records that the balance list was read in full up
// to through. It never moves back.
func (s *Service) advanceStripeWatermark(ctx context.Context, clubID string, through time.Time) error {
	if _, err := s.db.Exec(ctx,
		`INSERT INTO stripe_sync_state AS st (club_id, listed_through) VALUES ($1, $2)
		 ON CONFLICT (club_id) DO UPDATE
		    SET listed_through = GREATEST(st.listed_through, EXCLUDED.listed_through),
		        updated_at = now()`,
		clubID, through); err != nil {
		return fmt.Errorf("recording stripe sync watermark: %w", err)
	}
	return nil
}

// stripePayoutsToRefresh lists payouts whose state can still change or
// whose membership was never read: open payouts, and paid automatic payouts
// not listed yet.
func (s *Service) stripePayoutsToRefresh(ctx context.Context, clubID string) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT p.payout_id FROM stripe_payouts p
		  WHERE p.club_id = $1
		    AND (p.status IN ('pending', 'in_transit')
		         OR (p.status = 'paid' AND p.automatic AND p.members_listed_at IS NULL))`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing open stripe payouts: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("listing open stripe payouts: %w", err)
	}
	return ids, nil
}

// payoutOf returns the payout a payout or payout-reversal transaction moved.
func payoutOf(bt *stripe.BalanceTransaction) string {
	if bt.Source == nil {
		return ""
	}
	switch bt.ReportingCategory {
	case stripe.CategoryPayout, stripe.CategoryPayoutReversal:
		return bt.Source.ID
	}
	return ""
}

// stripeTxnClass is a balance transaction's classification at ingest.
type stripeTxnClass struct {
	category     string
	status       string
	reviewReason *string
	sourceID     string
	chargeID     string
	piID         string
	attemptID    *string
	orderID      *string
}

func (c *stripeTxnClass) review(reason string) {
	c.status = StripeTxnNeedsReview
	c.reviewReason = &reason
}

// ingestStripeTxnReported stores one balance transaction, recording a
// failure in res. It reports whether the row is stored.
func (s *Service) ingestStripeTxnReported(ctx context.Context, src StripeBalanceSource, clubID string, bt *stripe.BalanceTransaction, res *StripeSyncResult) bool {
	stored, err := s.ingestStripeTxn(ctx, src, clubID, bt, res)
	if err != nil {
		res.fail(bt.ID, err)
	}
	if !stored {
		res.incomplete = true
	}
	return stored
}

// ingestStripeTxn stores one balance transaction. A row already stored is
// reclassified only while it waits for review; booked, dismissed and
// ready rows are left exactly as they are. stored is false only when the
// row could not be written; err may also be a failed lookup of a row that
// was stored for review.
func (s *Service) ingestStripeTxn(ctx context.Context, src StripeBalanceSource, clubID string, bt *stripe.BalanceTransaction, res *StripeSyncResult) (stored bool, err error) {
	var status string
	err = s.db.QueryRow(ctx,
		`SELECT status FROM stripe_balance_transactions WHERE club_id = $1 AND balance_transaction_id = $2`,
		clubID, bt.ID).Scan(&status)
	switch {
	case err == nil && status != StripeTxnNeedsReview:
		return true, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("reading stored transaction: %w", err)
	}

	c, classifyErr := s.classifyStripeTxn(ctx, src, clubID, bt)
	if err := s.upsertStripeTxn(ctx, clubID, bt, c, res); err != nil {
		return false, err
	}
	return true, classifyErr
}

func (s *Service) upsertStripeTxn(ctx context.Context, clubID string, bt *stripe.BalanceTransaction, c stripeTxnClass, res *StripeSyncResult) error {
	var availableOn *time.Time
	if bt.AvailableOn > 0 {
		d := unixDate(bt.AvailableOn)
		availableOn = &d
	}
	var inserted bool
	err := s.db.QueryRow(ctx,
		`INSERT INTO stripe_balance_transactions AS t
		     (club_id, balance_transaction_id, reporting_category, amount_ore, fee_ore, net_ore,
		      currency, created_at, available_on, source_id, charge_id, payment_intent_id,
		      attempt_id, order_id, status, review_reason, stripe_type, fee_details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		 ON CONFLICT (club_id, balance_transaction_id) DO UPDATE
		    SET payment_intent_id = EXCLUDED.payment_intent_id,
		        attempt_id        = EXCLUDED.attempt_id,
		        order_id          = EXCLUDED.order_id,
		        status            = EXCLUDED.status,
		        review_reason     = EXCLUDED.review_reason,
		        updated_at        = now()
		  WHERE t.status = 'needs_review'
		    AND t.journal_entry_id IS NULL
		    AND (t.payment_intent_id, t.attempt_id, t.order_id, t.status, t.review_reason)
		        IS DISTINCT FROM
		        (EXCLUDED.payment_intent_id, EXCLUDED.attempt_id, EXCLUDED.order_id, EXCLUDED.status, EXCLUDED.review_reason)
		 RETURNING (xmax = 0)`,
		clubID, bt.ID, c.category, bt.Amount, bt.Fee, bt.Net,
		strings.ToLower(bt.Currency), time.Unix(bt.Created, 0).UTC(), availableOn,
		c.sourceID, c.chargeID, c.piID, c.attemptID, c.orderID, c.status, c.reviewReason,
		bt.Type, feeDetails(bt),
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// Stored already, and nothing about it changed.
		return nil
	}
	if err != nil {
		return fmt.Errorf("storing transaction: %w", err)
	}
	if inserted {
		res.Inserted++
	}
	if c.reviewReason != nil {
		res.NeedsReview[*c.reviewReason]++
	}
	return nil
}

// feeDetails is the fee breakdown as stored: never NULL, so the column's
// '[]' default is what an unbroken-down fee looks like too.
func feeDetails(bt *stripe.BalanceTransaction) []stripe.FeeDetail {
	if bt.FeeDetails == nil {
		return []stripe.FeeDetail{}
	}
	return bt.FeeDetails
}

// classifyStripeTxn decides a transaction's booking state. The error is a
// failed Stripe lookup; the classification is still usable (the charge is
// left for review) and a later sync classifies it again.
func (s *Service) classifyStripeTxn(ctx context.Context, src StripeBalanceSource, clubID string, bt *stripe.BalanceTransaction) (stripeTxnClass, error) {
	c := stripeTxnClass{category: bt.ReportingCategory, status: StripeTxnUnbooked}
	if bt.Source != nil {
		c.sourceID = bt.Source.ID
		c.piID = string(bt.Source.PaymentIntent)
		c.chargeID = string(bt.Source.Charge)
		if bt.Source.Object == "charge" {
			c.chargeID = bt.Source.ID
		}
	}

	var err error
	switch bt.ReportingCategory {
	case stripe.CategoryCharge:
		err = s.classifyStripeCharge(ctx, src, clubID, bt, &c)
	case stripe.CategoryFee, stripe.CategoryPayout, stripe.CategoryPayoutReversal:
	case stripe.CategoryRefund, stripe.CategoryDispute, stripe.CategoryDisputeReversal, stripe.CategoryOtherAdjustment:
		c.review(StripeReviewRefundPending)
	default:
		c.category = stripe.CategoryOtherAdjustment
		c.review(StripeReviewUnsupportedCategory)
		return c, nil
	}
	if !strings.EqualFold(bt.Currency, "nok") {
		c.review(StripeReviewCurrency)
	}
	return c, err
}

// classifyStripeCharge ties a charge to the payment attempt that created it
// and checks it took exactly what the attempt asked for.
func (s *Service) classifyStripeCharge(ctx context.Context, src StripeBalanceSource, clubID string, bt *stripe.BalanceTransaction, c *stripeTxnClass) error {
	attemptID, lookupErr := chargeAttemptID(ctx, src, bt.Source, c)
	a, ok, err := s.stripeAttempt(ctx, clubID, attemptID)
	if err != nil {
		return err
	}
	if !ok {
		c.review(StripeReviewNoAttempt)
		return lookupErr
	}
	c.attemptID, c.orderID = &a.id, &a.orderID
	switch {
	case !strings.EqualFold(bt.Currency, a.currency):
		c.review(StripeReviewCurrency)
	case bt.Amount != a.amountOre:
		c.review(StripeReviewAmountMismatch)
	}
	return nil
}

// chargeAttemptID reads the attempt id Brygge put in the payment's
// metadata: on the charge itself, else on its PaymentIntent.
func chargeAttemptID(ctx context.Context, src StripeBalanceSource, source *stripe.BalanceSource, c *stripeTxnClass) (string, error) {
	if source == nil {
		return "", nil
	}
	if id := source.Metadata["attempt_id"]; id != "" {
		return id, nil
	}
	if c.piID == "" {
		return "", nil
	}
	pi, err := src.GetPaymentIntent(ctx, c.piID)
	if err != nil {
		return "", fmt.Errorf("payment intent lookup: %w", err)
	}
	return pi.Metadata["attempt_id"], nil
}

type stripeAttemptRow struct {
	id, orderID, currency string
	amountOre             int64
}

// stripeAttempt loads a Stripe payment attempt of the club.
func (s *Service) stripeAttempt(ctx context.Context, clubID, attemptID string) (stripeAttemptRow, bool, error) {
	if _, err := uuid.Parse(attemptID); err != nil {
		return stripeAttemptRow{}, false, nil
	}
	a := stripeAttemptRow{id: attemptID}
	err := s.db.QueryRow(ctx,
		`SELECT order_id, currency, round(amount * 100)::bigint
		   FROM payment_attempts
		  WHERE id = $1 AND club_id = $2 AND provider = $3`,
		attemptID, clubID, stripe.ProviderName,
	).Scan(&a.orderID, &a.currency, &a.amountOre)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, fmt.Errorf("loading attempt: %w", err)
	}
	return a, true, nil
}

// stripePayoutStatuses is the stripe_payouts.status vocabulary.
var stripePayoutStatuses = map[string]bool{
	stripe.PayoutPending: true, stripe.PayoutInTransit: true, stripe.PayoutPaid: true,
	stripe.PayoutFailed: true, stripe.PayoutCanceled: true,
}

// ingestStripePayout refreshes a payout and its links. A failed or cancelled
// payout paid nothing out, so its members lose the link (unless the payout
// was already cleared in the books); Stripe pays them out again later. Only
// an automatic payout's members can be listed; a manual payout is recorded
// without them.
func (s *Service) ingestStripePayout(ctx context.Context, src StripeBalanceSource, clubID, payoutID string, res *StripeSyncResult) error {
	p, err := src.GetPayout(ctx, payoutID)
	if err != nil {
		return fmt.Errorf("payout lookup: %w", err)
	}
	if !stripePayoutStatuses[p.Status] {
		return fmt.Errorf("payout status %q not recognised", p.Status)
	}
	id, status, alreadyListed, err := s.upsertStripePayout(ctx, clubID, p)
	if err != nil {
		return err
	}
	res.PayoutsSeen++

	// An automatic payout's members are fixed when Stripe creates it, so one
	// complete listing is enough.
	var members []string
	listed := false
	if p.Automatic && !alreadyListed && status != stripe.PayoutFailed && status != stripe.PayoutCanceled {
		var complete bool
		members, complete, err = s.ingestPayoutMembers(ctx, src, clubID, payoutID, res)
		if err != nil {
			return err
		}
		if !complete {
			return errors.New("payout members not all stored")
		}
		listed = true
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE stripe_balance_transactions
		    SET payout_id = $3, updated_at = now()
		  WHERE club_id = $1 AND source_id = $2
		    AND reporting_category IN ('payout', 'payout_reversal')
		    AND payout_id IS DISTINCT FROM $3`,
		clubID, payoutID, id); err != nil {
		return fmt.Errorf("linking payout transactions: %w", err)
	}

	if status == stripe.PayoutFailed || status == stripe.PayoutCanceled {
		return s.unlinkFailedPayout(ctx, id)
	}
	if !listed {
		return nil
	}
	return s.linkPayoutMembers(ctx, clubID, id, members, res)
}

// upsertStripePayout stores a payout and returns its row id, the status now
// stored and whether its members were already listed. A status never moves
// back: Stripe answers can arrive out of order, and only paid may still turn
// into failed.
func (s *Service) upsertStripePayout(ctx context.Context, clubID string, p *stripe.Payout) (id, status string, listed bool, err error) {
	err = s.db.QueryRow(ctx,
		`INSERT INTO stripe_payouts AS p
		     (club_id, payout_id, amount_ore, currency, arrival_date, status, automatic, statement_descriptor)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (club_id, payout_id) DO UPDATE
		    SET status = CASE
		                   WHEN p.status IN ('failed', 'canceled') THEN p.status
		                   WHEN p.status = 'paid' AND EXCLUDED.status IN ('pending', 'in_transit') THEN p.status
		                   ELSE EXCLUDED.status END,
		        amount_ore           = EXCLUDED.amount_ore,
		        arrival_date         = EXCLUDED.arrival_date,
		        statement_descriptor = EXCLUDED.statement_descriptor,
		        updated_at           = now()
		 RETURNING id, status, members_listed_at IS NOT NULL`,
		clubID, p.ID, p.Amount, strings.ToLower(p.Currency), unixDate(p.ArrivalDate),
		p.Status, p.Automatic, p.StatementDescriptor,
	).Scan(&id, &status, &listed)
	if err != nil {
		return "", "", false, fmt.Errorf("storing payout: %w", err)
	}
	return id, status, listed, nil
}

// ingestPayoutMembers lists every balance transaction an automatic payout
// paid out, storing those not seen yet, and returns their ids and whether
// every one is stored.
func (s *Service) ingestPayoutMembers(ctx context.Context, src StripeBalanceSource, clubID, payoutID string, res *StripeSyncResult) (ids []string, complete bool, err error) {
	complete = true
	after := ""
	for {
		page, err := src.ListPayoutTransactions(ctx, payoutID, after)
		if err != nil {
			return nil, false, fmt.Errorf("listing payout transactions: %w", err)
		}
		for i := range page.Data {
			bt := &page.Data[i]
			ids = append(ids, bt.ID)
			if !s.ingestStripeTxnReported(ctx, src, clubID, bt, res) {
				complete = false
			}
		}
		if !page.HasMore || len(page.Data) == 0 {
			return ids, complete, nil
		}
		after = page.Data[len(page.Data)-1].ID
	}
}

// linkPayoutMembers points the listed transactions at their payout. A row
// still on a payout that was cleared in the books keeps that link, and a
// payout that failed meanwhile links nothing.
func (s *Service) linkPayoutMembers(ctx context.Context, clubID, payoutRowID string, ids []string, res *StripeSyncResult) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE stripe_balance_transactions t
		    SET payout_id = $2, updated_at = now()
		  WHERE t.club_id = $1
		    AND t.balance_transaction_id = ANY($3)
		    AND t.reporting_category NOT IN ('payout', 'payout_reversal')
		    AND t.payout_id IS DISTINCT FROM $2
		    AND NOT EXISTS (SELECT 1 FROM stripe_payouts old
		                     WHERE old.id = t.payout_id AND old.journal_entry_id IS NOT NULL)
		    AND EXISTS (SELECT 1 FROM stripe_payouts p
		                 WHERE p.id = $2 AND p.status NOT IN ('failed', 'canceled'))`,
		clubID, payoutRowID, ids)
	if err != nil {
		return fmt.Errorf("linking payout members: %w", err)
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE stripe_payouts SET members_listed_at = now(), updated_at = now()
		  WHERE id = $1 AND members_listed_at IS NULL`, payoutRowID); err != nil {
		return fmt.Errorf("recording payout membership: %w", err)
	}
	res.PayoutsLinked++
	res.MembersLinked += int(tag.RowsAffected())
	return nil
}

// unlinkFailedPayout releases the members of a payout that never arrived,
// unless the payout was already cleared in the books.
func (s *Service) unlinkFailedPayout(ctx context.Context, payoutRowID string) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE stripe_balance_transactions t
		    SET payout_id = NULL, updated_at = now()
		  WHERE t.payout_id = $1
		    AND t.reporting_category NOT IN ('payout', 'payout_reversal')
		    AND EXISTS (SELECT 1 FROM stripe_payouts p
		                 WHERE p.id = $1 AND p.journal_entry_id IS NULL)`,
		payoutRowID); err != nil {
		return fmt.Errorf("unlinking failed payout: %w", err)
	}
	return nil
}

// unixDate is the UTC calendar date of a Stripe timestamp; Stripe dates
// (available_on, arrival_date) are midnight UTC.
func unixDate(sec int64) time.Time {
	t := time.Unix(sec, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
