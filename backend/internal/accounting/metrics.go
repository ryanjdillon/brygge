package accounting

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Operator metrics for the automatic booking run (BRY-571, ACC-13). They feed
// the brygge-<club>-accounting alert group in
// docs/developer/otel/alerts/brygge-alerts.rules.yaml and reach the operator,
// never the treasurer. Two families:
//
//   - BacklogGauges read the database at each export: how long the oldest
//     unbooked Vipps charge and the oldest clearable Vipps payout deposit
//     have waited. They span every club in the database, like the payment
//     attempt gauge.
//   - AutoBookingMetrics is told by the run (AutoBookingObserver) when the
//     Report API pull last succeeded and whether the last run failed.

const (
	backlogTimeout = 5 * time.Second
	// backlogLogEvery limits the warning for a failing read to one per
	// interval per gauge, as the payment attempt gauge does.
	backlogLogEvery = 15 * time.Minute
	// unknownMSN labels a charge imported without a merchant serial number,
	// so it still has a series of its own.
	unknownMSN = "unknown"
)

// bookableDateSQL holds when dateExpr falls in a fiscal period of the club
// that is open and not a reference period, and in no period that is closed,
// locked or reference. A row dated in a missing, closed or locked year waits
// for a person: BRY-575's notice tells the treasurer about the rows an
// automatic pass would book there, and the rest waits visibly in Tildel. A
// reference year's rows are never booked here. The backlog gauges leave all
// of them out, so the operator is not paged for what a person must decide.
// It is stricter than referenceDateSQL and implies it.
func bookableDateSQL(clubExpr, dateExpr string) string {
	return `(EXISTS (SELECT 1 FROM fiscal_periods bp
	                 WHERE bp.club_id = ` + clubExpr + `
	                   AND ` + dateExpr + ` BETWEEN bp.start_date AND bp.end_date)
	        AND NOT EXISTS (SELECT 1 FROM fiscal_periods bq
	                 WHERE bq.club_id = ` + clubExpr + `
	                   AND ` + dateExpr + ` BETWEEN bq.start_date AND bq.end_date
	                   AND (bq.status <> 'open' OR bq.reference_only)))`
}

// periodChangedSQL is when the fiscal period covering dateExpr last changed
// (created, closed, reopened, marked or unmarked as a reference year: every
// write to fiscal_periods sets updated_at), NULL when none covers it. Under
// bookableDateSQL only open, non-reference periods cover the date, so this is
// when the row became bookable as far as its year goes; the backlog clocks
// start no earlier, so creating or reopening a year does not page at once
// for rows that waited on it.
func periodChangedSQL(clubExpr, dateExpr string) string {
	return `(SELECT max(pc.updated_at) FROM fiscal_periods pc
	          WHERE pc.club_id = ` + clubExpr + `
	            AND ` + dateExpr + ` BETWEEN pc.start_date AND pc.end_date)`
}

// unbookedChargeAgeSQL is, per merchant, the age in seconds of the oldest
// Vipps charge that is not on a posted entry (in Tildel, on a draft or on a
// voided entry) and not dismissed, over the clubs with the per-charge model
// and automatic booking on (owner decision O2). Every amount counts, as in
// the payout sweep's gate: a zero or negative charge that holds a payout up
// must be visible. A charge a person declined for the automatic passes
// counts: it waits for that person. The clock starts at the latest of when
// the row reached Brygge (created_at), its own time, and when its year last
// changed (periodChangedSQL), so a late CSV import of old charges or a year
// opened late does not page at once; a charge dated ahead of now reads 0
// rather than a negative age. A dateless row (BRY-584) counts; a
// dated one only in a bookable period (bookableDateSQL). A row without a
// merchant number is labelled unknownMSN. A merchant with no such charge has
// no row, so no series.
var unbookedChargeAgeSQL = `SELECT COALESCE(NULLIF(r.msn, ''), '` + unknownMSN + `'),
	        GREATEST(EXTRACT(EPOCH FROM now() - min(GREATEST(r.created_at,
	            COALESCE(r.tx_at, r.booking_date::timestamptz),
	            ` + periodChangedSQL("r.club_id", "COALESCE(r.booking_date, r.tx_at::date)") + `))), 0)::float8
	   FROM vipps_import_rows r
	   JOIN clubs c ON c.id = r.club_id
	  WHERE c.vipps_per_charge_enabled AND c.automatic_booking
	    AND r.row_type = 'belastning' AND r.dismissed_at IS NULL
	    AND NOT ` + chargePostedSQL("r") + `
	    AND (COALESCE(r.booking_date, r.tx_at::date) IS NULL
	         OR ` + bookableDateSQL("r.club_id", "COALESCE(r.booking_date, r.tx_at::date)") + `)
	  GROUP BY 1`

// readyPayoutAgeSQL is the age in seconds of the oldest Vipps payout deposit
// the sweep could clear but has not, 0 when there is none, over the clubs
// with the per-charge model and automatic booking on (O2). A deposit counts
// when it is unbooked, not dismissed, not declined (a person took it out of
// the sweep on purpose), incoming, names a settlement ($1 is
// vippsSettlementPatternSQL) whose imported payout row has a booking date
// (one without drops out; the sweep reports it), and the settlement's charges
// on that date are all posted or dismissed (the sweep's gate). Both the
// deposit's date and the settlement's date must be bookable
// (bookableDateSQL). The clock starts when the sweep could first clear it:
// the latest of the deposit's upload, the last posting or dismissal of the
// settlement's charges, and the last change to the fiscal periods covering
// the two dates (periodChangedSQL). GREATEST ignores NULL. The payout row is
// picked as clearVippsPayout picks it.
var readyPayoutAgeSQL = `SELECT COALESCE(max(EXTRACT(EPOCH FROM now() - GREATEST(b.created_at,
	          (SELECT max(je.posted_at)
	             FROM vipps_import_rows cr
	             JOIN journal_entries je ON je.id = cr.journal_entry_id AND je.status = 'posted'
	            WHERE cr.club_id = b.club_id AND cr.msn = m.ref[2] AND cr.row_type = 'belastning'
	              AND cr.booking_date = p.booking_date AND cr.dismissed_at IS NULL),
	          (SELECT max(cd.dismissed_at)
	             FROM vipps_import_rows cd
	            WHERE cd.club_id = b.club_id AND cd.msn = m.ref[2] AND cd.row_type = 'belastning'
	              AND cd.booking_date = p.booking_date),
	          ` + periodChangedSQL("b.club_id", "b.row_date") + `,
	          ` + periodChangedSQL("b.club_id", "p.booking_date") + `))), 0)::float8
	   FROM bank_import_rows b
	   JOIN clubs c ON c.id = b.club_id
	   CROSS JOIN LATERAL regexp_match(b.description, $1, 'i') AS m(ref)
	   JOIN LATERAL (
	        SELECT pr.booking_date FROM vipps_import_rows pr
	         WHERE pr.club_id = b.club_id AND pr.row_type = 'payout'
	           AND pr.msn = m.ref[2] AND pr.settlement_number = m.ref[1]
	         ORDER BY pr.booking_date, pr.id
	         LIMIT 1) p ON true
	  WHERE c.vipps_per_charge_enabled AND c.automatic_booking
	    AND b.journal_entry_id IS NULL AND b.dismissed_at IS NULL AND b.auto_declined_at IS NULL
	    AND b.amount > 0
	    AND m.ref IS NOT NULL
	    AND p.booking_date IS NOT NULL
	    AND ` + bookableDateSQL("b.club_id", "b.row_date") + `
	    AND ` + bookableDateSQL("b.club_id", "p.booking_date") + `
	    AND NOT EXISTS (SELECT 1 FROM vipps_import_rows w
	                     WHERE w.club_id = b.club_id AND w.msn = m.ref[2] AND w.row_type = 'belastning'
	                       AND w.booking_date = p.booking_date AND w.dismissed_at IS NULL
	                       AND NOT ` + chargePostedSQL("w") + `)`

// backlogQuerier is the part of a pgx pool the backlog gauges read with.
type backlogQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// BacklogGauges reports how long Vipps work has waited for the automatic run:
// the oldest unbooked charge per merchant and the oldest clearable payout
// deposit. Each read has a 5 s timeout; a read that fails observes nothing
// for its gauge rather than a value that reads as "all booked".
type BacklogGauges struct {
	db  backlogQuerier
	log zerolog.Logger

	// One limiter per gauge, so a failing read of one does not hide the
	// other's warning.
	chargesErr, payoutsErr errLogLimiter
}

// errLogLimiter lets one warning through per backlogLogEvery.
type errLogLimiter struct {
	mu   sync.Mutex
	last time.Time
}

func (l *errLogLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.last) < backlogLogEvery {
		return false
	}
	l.last = time.Now()
	return true
}

// NewBacklogGauges builds the gauges over db. Call RegisterMetrics once.
func NewBacklogGauges(db backlogQuerier, log zerolog.Logger) *BacklogGauges {
	return &BacklogGauges{db: db, log: log.With().Str("component", "accounting-backlog-gauges").Logger()}
}

// RegisterMetrics registers the gauges on the global MeterProvider.
func (g *BacklogGauges) RegisterMetrics() error {
	_, err := g.registerMetrics(otel.Meter("brygge-api"))
	return err
}

func (g *BacklogGauges) registerMetrics(meter metric.Meter) (metric.Registration, error) {
	charges, err := meter.Float64ObservableGauge("brygge.vipps.unbooked_charge.oldest_age",
		metric.WithDescription("Age of the oldest Vipps charge not on a posted entry, per merchant, for clubs with automatic booking on"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	payouts, err := meter.Float64ObservableGauge("brygge.vipps.ready_payout.oldest_age",
		metric.WithDescription("Age of the oldest Vipps payout deposit the sweep could clear but has not, for clubs with automatic booking on; 0 when there is none"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	return meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, backlogTimeout)
		defer cancel()
		if ages, err := g.unbookedChargeAges(ctx); err != nil {
			g.warn(&g.chargesErr, err, "reading the oldest unbooked vipps charge failed")
		} else {
			for _, a := range ages {
				o.ObserveFloat64(charges, a.seconds, metric.WithAttributes(attribute.String("msn", a.msn)))
			}
		}
		var seconds float64
		if err := g.db.QueryRow(ctx, readyPayoutAgeSQL, vippsSettlementPatternSQL).Scan(&seconds); err != nil {
			g.warn(&g.payoutsErr, err, "reading the oldest ready vipps payout failed")
		} else {
			o.ObserveFloat64(payouts, seconds)
		}
		return nil
	}, charges, payouts)
}

type merchantAge struct {
	msn     string
	seconds float64
}

// unbookedChargeAges reads every merchant's row before any is observed, so a
// read that fails midway observes none.
func (g *BacklogGauges) unbookedChargeAges(ctx context.Context) ([]merchantAge, error) {
	rows, err := g.db.Query(ctx, unbookedChargeAgeSQL)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (merchantAge, error) {
		var a merchantAge
		err := r.Scan(&a.msn, &a.seconds)
		return a, err
	})
}

func (g *BacklogGauges) warn(l *errLogLimiter, err error, msg string) {
	if l.allow() {
		g.log.Warn().Err(err).Msg(msg)
	}
}

// autoBookingProviders are the providers AutoBookingMetrics reports; any
// other is ignored, so the label set stays closed.
var autoBookingProviders = map[string]bool{AutoBookingProviderVipps: true, AutoBookingProviderStripe: true}

// AutoBookingMetrics holds, in memory, what the automatic runs told it and
// exports it as gauges. It implements AutoBookingObserver; wire it with
// Service.SetAutoBookingObserver.
//
//   - brygge.vipps.report_sync.last_success (per msn): Unix time of the last
//     Report API pull where every date answered. Nothing is observed for a
//     merchant before its first success, so after a restart whose first
//     pull fails the series is absent, and the alert's absent branch covers
//     that.
//   - brygge.accounting.auto_booking.last_run_failed (per provider): 1 when
//     the provider's last run failed (AutoBookingResult.Failed), 0 when it
//     did not; absent before the first run. A busy slot or a failed ingest
//     is not a failure here: the next run retries the first, and the sync
//     gauge covers the second.
type AutoBookingMetrics struct {
	mu          sync.Mutex
	lastSuccess map[string]int64
	lastFailed  map[string]bool
}

var _ AutoBookingObserver = (*AutoBookingMetrics)(nil)

// NewAutoBookingMetrics returns empty metrics. Call RegisterMetrics once.
func NewAutoBookingMetrics() *AutoBookingMetrics {
	return &AutoBookingMetrics{lastSuccess: map[string]int64{}, lastFailed: map[string]bool{}}
}

// VippsReportSynced records a successful Report API pull for msn.
func (m *AutoBookingMetrics) VippsReportSynced(msn string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSuccess[msn] = at.Unix()
}

// AutoBookingRun records whether provider's last run failed.
func (m *AutoBookingMetrics) AutoBookingRun(provider string, failed bool) {
	if !autoBookingProviders[provider] {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastFailed[provider] = failed
}

// RegisterMetrics registers the gauges on the global MeterProvider.
func (m *AutoBookingMetrics) RegisterMetrics() error {
	_, err := m.registerMetrics(otel.Meter("brygge-api"))
	return err
}

func (m *AutoBookingMetrics) registerMetrics(meter metric.Meter) (metric.Registration, error) {
	lastSuccess, err := meter.Int64ObservableGauge("brygge.vipps.report_sync.last_success",
		metric.WithDescription("Unix time of the last Vipps Report API pull where every date answered, per merchant; absent before the first"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	lastRunFailed, err := meter.Int64ObservableGauge("brygge.accounting.auto_booking.last_run_failed",
		metric.WithDescription("1 when the provider's last automatic booking run failed, 0 when it did not; absent before the first run"),
		metric.WithUnit("{run}"),
	)
	if err != nil {
		return nil, err
	}
	return meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		for msn, at := range m.lastSuccess {
			o.ObserveInt64(lastSuccess, at, metric.WithAttributes(attribute.String("msn", msn)))
		}
		for provider, failed := range m.lastFailed {
			var v int64
			if failed {
				v = 1
			}
			o.ObserveInt64(lastRunFailed, v, metric.WithAttributes(attribute.String("provider", provider)))
		}
		return nil
	}, lastSuccess, lastRunFailed)
}
