package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
)

// A payment waits for a fiscal year when an automatic pass would book it but
// its date falls in a year the club has not opened, or in a period that is
// closed or locked (BRY-575, ACC-13 decision c). No automatic pass opens a
// year or books into a closed one, so such a payment waits until a person
// acts; the treasurer is told once per year and state.
//
// The condition is derived, never stored: WaitingFiscalYears reads it from
// the queues as they stand, the notice is queued with a ref that names the
// condition, and the sender reads it again before it mails, so a notice whose
// condition no longer holds is dropped instead of sent.

// Waiting states.
const (
	WaitingMissing = "missing"
	WaitingClosed  = "closed"
	WaitingLocked  = "locked"
)

// WaitingFiscalYear is one year (missing) or one period (closed, locked) that
// payments wait for.
type WaitingFiscalYear struct {
	Year  int    `json:"year"`
	State string `json:"state"`
	// PeriodID and ClosedAt name the closed or locked period; nil for a
	// missing year. ClosedAt is nil for a locked period nothing closed.
	PeriodID *string    `json:"period_id,omitempty"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	// Payments counts the payments that wait: Vipps charges, Stripe charges
	// and KID bank rows. A year held up only by a provider payout deposit or
	// a fee counts as one, so it is still reported.
	Payments int `json:"payments"`
	// Oldest is the earliest booking date among the waiting rows.
	Oldest time.Time `json:"oldest"`
}

// Ref is the delivery ref of the notice for w: once per club, year and
// state, and for a closed or locked period once per closing, so closing the
// year again after a reopen tells the treasurer again.
func (w WaitingFiscalYear) Ref(clubID string) string {
	ref := fmt.Sprintf("fiscal_year_waiting:%s:%d:%s", clubID, w.Year, w.State)
	if w.State == WaitingMissing || w.PeriodID == nil {
		return ref
	}
	var closed int64
	if w.ClosedAt != nil {
		closed = w.ClosedAt.Unix()
	}
	return ref + ":" + *w.PeriodID + ":" + strconv.FormatInt(closed, 10)
}

// waitingPeriod is a fiscal period as WaitingFiscalYears reads it.
type waitingPeriod struct {
	id         string
	year       int
	start, end time.Time
	status     string
	closedAt   *time.Time
	reference  bool
}

// waitingRow is one row an automatic pass would book, with its booking date.
type waitingRow struct {
	date time.Time
	// payment is true for a payment (Vipps charge, Stripe charge, KID row);
	// false for a deposit, fee, refund or dispute row.
	payment bool
	// cascade is set for a bank row that is not a payout deposit: it counts
	// only when the payment-match cascade finds the faktura it pays
	// (cascadeInvoice).
	cascade *BankRow
}

// cascadeGate decides, within one WaitingFiscalYears call, which bank rows
// the payment-match cascade would book.
type cascadeGate struct {
	s      *Service
	clubID string
	// nameMatch is as bank sync passes it: on while bank auto-assign is off.
	nameMatch bool
	// claimed holds the fakturas already counted: the cascade books a faktura
	// once, so a second row matching it would be left in Tildel.
	claimed map[string]bool
}

func (s *Service) newCascadeGate(ctx context.Context, clubID string) (*cascadeGate, error) {
	var bankAuto bool
	if err := s.db.QueryRow(ctx, `SELECT bank_auto_assign FROM clubs WHERE id = $1`, clubID).Scan(&bankAuto); err != nil {
		return nil, fmt.Errorf("loading bank auto-assign: %w", err)
	}
	return &cascadeGate{s: s, clubID: clubID, nameMatch: !bankAuto, claimed: map[string]bool{}}, nil
}

// books reports whether the cascade would book row, claiming its faktura.
func (g *cascadeGate) books(ctx context.Context, row BankRow) (bool, error) {
	invoiceID, err := g.s.cascadeInvoice(ctx, g.clubID, row, g.nameMatch)
	if err != nil {
		return false, fmt.Errorf("matching a waiting bank row: %w", err)
	}
	if invoiceID == "" || g.claimed[invoiceID] {
		return false, nil
	}
	g.claimed[invoiceID] = true
	return true, nil
}

// ErrFiscalYearResolved is the note on a fiscal year notice whose condition
// no longer held when it was sent (FiscalYearWaitingSender); nothing was
// mailed. It is the one failure the hourly sweep puts back in the queue when
// the condition comes back (queueWaitingNotices), so the two share its text.
var ErrFiscalYearResolved = fmt.Errorf("%w: no payments wait for this fiscal year any more", bcast.ErrUnsendable)

// cascadeInvoice is the faktura bank sync's payment-match cascade
// (syncKIDMatches) would book the row against, "" when none: not a
// rejected-KID notice, the KID recovered from the description as sync does
// (recoverKIDAndPayer), then findCascadeInvoice with nameMatch as sync passes
// it (on unless the club has bank auto-assign, whose own pass books KID-less
// deposits and is not counted here). A payment nothing would book (a stale
// KID, a faktura already paid) waits for a person in Tildel whatever the
// year, so reopening the year for it would book nothing. A failed lookup is
// an error, never "nothing to book".
func (s *Service) cascadeInvoice(ctx context.Context, clubID string, row BankRow, nameMatch bool) (string, error) {
	if IsRejectedKIDDescription(row.Description) {
		return "", nil
	}
	row.KID, row.Counterpart = recoverKIDAndPayer(row.KID, row.Counterpart, row.Description)
	return s.findCascadeInvoiceErr(ctx, clubID, row, nameMatch)
}

// WaitingFiscalYears lists the years and periods that payments wait for, by
// year and then state. It is read-only. A club with no non-reference period
// keeps no books in Brygge and gets nothing; a missing year is reported only
// after the club's first non-reference year, so history imported from before
// the books started is never reported; and a row dated in a reference year is
// never counted.
func (s *Service) WaitingFiscalYears(ctx context.Context, clubID string) ([]WaitingFiscalYear, error) {
	return s.waitingFiscalYears(ctx, clubID, time.Local)
}

// waitingFiscalYears is WaitingFiscalYears with the zone Stripe timestamps
// are read in (stripeBookingDate), so a test can pin it.
func (s *Service) waitingFiscalYears(ctx context.Context, clubID string, loc *time.Location) ([]WaitingFiscalYear, error) {
	periods, err := loadWaitingPeriods(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}
	firstYear, firstStart := booksStart(periods)
	if firstYear == 0 {
		return nil, nil
	}
	rows, err := s.loadWaitingRows(ctx, clubID, loc, firstStart)
	if err != nil {
		return nil, err
	}
	gate, err := s.newCascadeGate(ctx, clubID)
	if err != nil {
		return nil, err
	}
	var groups waitingGroups
	for _, r := range rows {
		w, ok := waitingFor(r.date, periods, firstYear)
		if !ok {
			continue
		}
		// A bank row counts only when the cascade would book it: the costly
		// faktura lookup runs for the rows that wait, not for every row.
		if r.cascade != nil {
			books, err := gate.books(ctx, *r.cascade)
			if err != nil {
				return nil, err
			}
			if !books {
				continue
			}
		}
		groups.add(w, r)
	}
	return groups.list(), nil
}

// booksStart is the club's first non-reference year and the earliest start
// of its non-reference periods; 0 when it keeps no books in Brygge.
func booksStart(periods []waitingPeriod) (firstYear int, firstStart time.Time) {
	for _, p := range periods {
		if p.reference {
			continue
		}
		if firstYear == 0 || p.year < firstYear {
			firstYear = p.year
		}
		if firstStart.IsZero() || p.start.Before(firstStart) {
			firstStart = p.start
		}
	}
	return firstYear, firstStart
}

// waitingGroups gathers counted rows by year, state and period.
type waitingGroups struct {
	byKey map[string]*waitingGroup
}

type waitingGroup struct {
	w        WaitingFiscalYear
	payments int
}

func (g *waitingGroups) add(w WaitingFiscalYear, r waitingRow) {
	if g.byKey == nil {
		g.byKey = map[string]*waitingGroup{}
	}
	key := fmt.Sprintf("%d:%s", w.Year, w.State)
	if w.PeriodID != nil {
		key += ":" + *w.PeriodID
	}
	grp := g.byKey[key]
	if grp == nil {
		w.Oldest = r.date
		grp = &waitingGroup{w: w}
		g.byKey[key] = grp
	}
	if r.date.Before(grp.w.Oldest) {
		grp.w.Oldest = r.date
	}
	if r.payment {
		grp.payments++
	}
}

// list returns the groups by year, then state; a group held up only by a
// deposit or fee counts as one payment.
func (g *waitingGroups) list() []WaitingFiscalYear {
	out := make([]WaitingFiscalYear, 0, len(g.byKey))
	for _, grp := range g.byKey {
		grp.w.Payments = max(grp.payments, 1)
		out = append(out, grp.w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return out[i].State < out[j].State
	})
	return out
}

// waitingFor decides what a row dated date waits for: the covering period
// when it is closed or locked (the latest-starting one, as existingPeriodIn
// picks), the date's calendar year when no period covers it. ok is false when
// it waits for nothing: an open period, a reference period, a year before the
// books started, or a year whose period exists with other dates (CreatePeriod
// would refuse it, so asking the treasurer to create it would mislead).
func waitingFor(date time.Time, periods []waitingPeriod, firstYear int) (WaitingFiscalYear, bool) {
	var cover *waitingPeriod
	for i := range periods {
		p := &periods[i]
		if date.Before(p.start) || date.After(p.end) {
			continue
		}
		if cover == nil || p.start.After(cover.start) {
			cover = p
		}
	}
	if cover != nil {
		if cover.reference || cover.status == "open" {
			return WaitingFiscalYear{}, false
		}
		id := cover.id
		state := WaitingClosed
		if cover.status == "locked" {
			state = WaitingLocked
		}
		return WaitingFiscalYear{Year: cover.year, State: state, PeriodID: &id, ClosedAt: cover.closedAt}, true
	}
	year := date.Year()
	if year <= firstYear {
		return WaitingFiscalYear{}, false
	}
	for _, p := range periods {
		if p.year == year {
			return WaitingFiscalYear{}, false
		}
	}
	return WaitingFiscalYear{Year: year, State: WaitingMissing}, true
}

func loadWaitingPeriods(ctx context.Context, q pgxQuerier, clubID string) ([]waitingPeriod, error) {
	rows, err := q.Query(ctx,
		`SELECT id::text, year, start_date, end_date, status::text, closed_at, reference_only
		   FROM fiscal_periods WHERE club_id = $1`,
		clubID)
	if err != nil {
		return nil, fmt.Errorf("listing fiscal periods: %w", err)
	}
	defer rows.Close()
	var out []waitingPeriod
	for rows.Next() {
		var p waitingPeriod
		if err := rows.Scan(&p.id, &p.year, &p.start, &p.end, &p.status, &p.closedAt, &p.reference); err != nil {
			return nil, fmt.Errorf("scanning fiscal period: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// loadWaitingRows reads every row an automatic pass would book, whatever its
// date: the Vipps charges auto-assign and the order reconcile take up, the
// incoming bank rows the KID cascade or a payout clearing books, and the
// Stripe rows the booking pass takes up.
func (s *Service) loadWaitingRows(ctx context.Context, clubID string, loc *time.Location, firstStart time.Time) ([]waitingRow, error) {
	var out []waitingRow
	q := s.db

	// Vipps charges, dated exactly as AutoAssignVippsCharges dates them, that
	// a pass books: an order-linked charge (AutoReconcileOrderCharges, every
	// club), any other only while the club has Vipps auto-assign on
	// (AutoAssignVippsCharges). On a club without it a charge waits for a
	// person whatever the year (on the legacy model, for the settlement
	// confirm, which is not counted). A row a person took out of the
	// automatic passes waits for nobody.
	vipps, err := q.Query(ctx,
		`SELECT COALESCE(r.booking_date, r.tx_at::date)
		   FROM vipps_import_rows r
		  WHERE r.club_id = $1 AND r.row_type = 'belastning' AND r.amount > 0
		    AND r.journal_entry_id IS NULL AND r.dismissed_at IS NULL
		    AND r.auto_declined_at IS NULL
		    AND COALESCE(r.booking_date, r.tx_at::date) >= $2
		    AND ((r.order_id <> '' AND EXISTS (
		             SELECT 1 FROM orders o WHERE o.club_id = r.club_id AND o.vipps_reference = r.order_id))
		      OR (SELECT c.vipps_auto_assign FROM clubs c WHERE c.id = r.club_id))
		    AND NOT `+referenceDateSQL("r.club_id", "COALESCE(r.booking_date, r.tx_at::date)"),
		clubID, firstStart)
	if err != nil {
		return nil, fmt.Errorf("listing waiting vipps charges: %w", err)
	}
	dates, err := pgx.CollectRows(vipps, pgx.RowTo[time.Time])
	if err != nil {
		return nil, fmt.Errorf("listing waiting vipps charges: %w", err)
	}
	for _, d := range dates {
		out = append(out, waitingRow{date: d, payment: true})
	}

	bank, err := s.loadWaitingBankRows(ctx, clubID, firstStart)
	if err != nil {
		return nil, err
	}
	out = append(out, bank...)

	// A day's margin for the zone stripeBookingDate reads the timestamp in.
	stripe, err := q.Query(ctx,
		`SELECT created_at, reporting_category = 'charge'
		   FROM stripe_balance_transactions
		  WHERE club_id = $1 AND created_at >= $2 AND `+stripeBookableSQL(false),
		clubID, firstStart.AddDate(0, 0, -1))
	if err != nil {
		return nil, fmt.Errorf("listing waiting stripe rows: %w", err)
	}
	defer stripe.Close()
	for stripe.Next() {
		var created time.Time
		var charge bool
		if err := stripe.Scan(&created, &charge); err != nil {
			return nil, fmt.Errorf("scanning waiting stripe row: %w", err)
		}
		// Reference years are left out by waitingFor, which sees the
		// reference periods: the date is only known here, in Go.
		out = append(out, waitingRow{date: stripeBookingDate(created, loc), payment: charge})
	}
	return out, stripe.Err()
}

// loadWaitingBankRows reads the incoming Tildel rows (tildelScope) that an
// automatic pass books: any row the payment-match cascade books (by KID,
// arkivref, faktura number or, while bank auto-assign is off, name and
// amount; counted only when it finds its faktura, cascadeInvoice), a Vipps
// settlement deposit on the per-charge model (the payout sweep) and a Stripe
// payout deposit (the Stripe payout clearing). A provisional row gives way to
// its settled row and is left out. Deliberately not counted: intra-bank
// transfer pairs, the legacy (non-per-charge) settlement confirm, whose
// bookings depend on a second row or a whole settlement balancing, and bank
// auto-assign's own matches.
//
// The cheap filters (incoming, a date the books cover) narrow the scan
// before the provisional subqueries. Payout deposits are recognised in Go
// (providerPayouts: the Stripe descriptor is a Go regular expression).
func (s *Service) loadWaitingBankRows(ctx context.Context, clubID string, firstStart time.Time) ([]waitingRow, error) {
	payouts, err := loadProviderPayouts(ctx, s.db, clubID)
	if err != nil {
		return nil, err
	}
	perCharge, err := s.VippsPerChargeEnabled(ctx, clubID)
	if err != nil {
		return nil, fmt.Errorf("checking vipps model: %w", err)
	}
	clauses, args, err := tildelScope(clubID, "", 0)
	if err != nil {
		return nil, err
	}
	args = append(args, firstStart)
	clauses = append([]string{
		"bir.amount > 0",
		fmt.Sprintf("bir.row_date >= $%d", len(args)),
	}, clauses...)
	clauses = append(clauses,
		"bir.refund_paired_with IS NULL",
		"bir.auto_declined_at IS NULL",
		"NOT COALESCE(("+provisionalRowSQL("bir")+"), false)",
	)
	rows, err := s.db.Query(ctx,
		`SELECT bir.row_date, btrim(COALESCE(bir.kid_number, '')),
		        COALESCE(bir.reference, ''),
		        COALESCE(bir.description, ''), COALESCE(bir.counterpart, ''),
		        round(bir.amount * 100)::bigint
		   FROM bank_import_rows bir
		  WHERE `+strings.Join(clauses, " AND ")+`
		  ORDER BY bir.row_date, bir.id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("listing waiting bank rows: %w", err)
	}
	type bankRow struct {
		date                        time.Time
		kid, ref, desc, counterpart string
		cents                       int64
	}
	var loaded []bankRow
	for rows.Next() {
		var r bankRow
		if err := rows.Scan(&r.date, &r.kid, &r.ref, &r.desc, &r.counterpart, &r.cents); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning waiting bank row: %w", err)
		}
		loaded = append(loaded, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing waiting bank rows: %w", err)
	}
	var out []waitingRow
	for _, r := range loaded {
		switch kind := payouts.kind(r.desc, r.counterpart, r.cents); {
		case kind == ProviderPayoutVipps && perCharge, kind == ProviderPayoutStripe:
			out = append(out, waitingRow{date: r.date})
		case kind == "":
			row := BankRow{KID: r.kid, Reference: r.ref, Description: r.desc, Counterpart: r.counterpart, Amount: float64(r.cents) / 100}
			out = append(out, waitingRow{date: r.date, payment: true, cascade: &row})
		}
	}
	return out, nil
}

// FiscalYearWaitingPayload is the payload of an
// accounting_fiscal_year_waiting delivery. It names the condition only; the
// sender reads the count and dates again when it mails.
type FiscalYearWaitingPayload struct {
	Ref      string `json:"ref"`
	Year     int    `json:"year"`
	State    string `json:"state"`
	PeriodID string `json:"period_id,omitempty"`
}

// fiscalYearWaitingRefPredicate is the predicate of the partial unique index
// idx_deliveries_fiscal_year_waiting_ref (migration 000144); an ON CONFLICT
// that names it must repeat it.
const fiscalYearWaitingRefPredicate = `kind = 'accounting_fiscal_year_waiting'`

// NotifyWaitingFiscalYears queues the treasurer's notice for every year and
// period payments wait for (WaitingFiscalYears) up to now's year, once per
// ref (WaitingFiscalYear.Ref). A notice already queued or sent for the same
// condition is left alone, and so is every failure but one: a notice that
// failed because its condition had cleared by the time it was sent
// (ErrFiscalYearResolved, matched exactly) is put back in the queue when the
// condition comes back. A failure for want of an address is requeued by the
// sender's RequeueUnaddressed once there is one; any other failure (no mail
// transport, retries used up, an unknown send outcome) stays failed for a
// person to see in the delivery log. It reports how many it queued or put
// back.
// The hourly sweep runs it for every club; RunAutomaticBooking may call it at
// the end of a run as well (BRY-567).
func (s *Service) NotifyWaitingFiscalYears(ctx context.Context, clubID string, now time.Time) (int, error) {
	waiting, err := s.WaitingFiscalYears(ctx, clubID)
	if err != nil {
		return 0, err
	}
	return queueWaitingNotices(ctx, s.db, clubID, waiting, now)
}

func queueWaitingNotices(ctx context.Context, q pgxQuerier, clubID string, waiting []WaitingFiscalYear, now time.Time) (int, error) {
	queued := 0
	for _, w := range waiting {
		if w.Year > now.Year() {
			continue
		}
		p := FiscalYearWaitingPayload{Ref: w.Ref(clubID), Year: w.Year, State: w.State}
		if w.PeriodID != nil {
			p.PeriodID = *w.PeriodID
		}
		payload, err := json.Marshal(p)
		if err != nil {
			return queued, fmt.Errorf("marshal fiscal year notice: %w", err)
		}
		var id string
		err = q.QueryRow(ctx,
			`INSERT INTO deliveries (kind, club_id, email, status, payload)
			 VALUES ($1, $2, '', $3, $4)
			 ON CONFLICT (kind, (payload->>'ref')) WHERE `+fiscalYearWaitingRefPredicate+`
			 DO UPDATE SET status = $3, attempts = 0, error = '', updated_at = now()
			   WHERE deliveries.status = $5 AND deliveries.error = $6
			 RETURNING id`,
			bcast.KindAccountingFiscalYearWaiting, clubID, bcast.DeliveryPending, payload,
			bcast.DeliveryFailed, ErrFiscalYearResolved.Error(),
		).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return queued, fmt.Errorf("queue fiscal year notice: %w", err)
		}
		queued++
	}
	return queued, nil
}

// NotifyAllWaitingFiscalYears runs NotifyWaitingFiscalYears for every club
// that keeps books in Brygge (a non-reference period exists). A club that
// fails is logged in the joined error and the rest still run.
func (s *Service) NotifyAllWaitingFiscalYears(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.id::text FROM clubs c
		  WHERE EXISTS (SELECT 1 FROM fiscal_periods fp WHERE fp.club_id = c.id AND NOT fp.reference_only)
		  ORDER BY c.id`)
	if err != nil {
		return 0, fmt.Errorf("listing clubs with books: %w", err)
	}
	clubs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("listing clubs with books: %w", err)
	}
	total := 0
	var errs []error
	for _, clubID := range clubs {
		n, err := s.NotifyWaitingFiscalYears(ctx, clubID, now)
		total += n
		if err != nil {
			errs = append(errs, fmt.Errorf("club %s: %w", clubID, err))
		}
	}
	return total, errors.Join(errs...)
}
