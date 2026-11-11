package accounting

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// backlogFixture is a club on the per-charge model with automatic booking on,
// an open 2026 period (fin's default) and a bank import.
type backlogFixture struct {
	svc        *Service
	club       *fin.Club
	bankImport string
	entryNo    int
}

func newBacklogFixture(t *testing.T) *backlogFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	f := &backlogFixture{svc: NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())}
	f.club = newBacklogClub(t, pool, true, true)
	f.bankImport = f.club.NewBankImport(t)
	f.ageYears(t)
	return f
}

// ageYears backdates every fiscal period's last change, so the year's
// opening does not start the backlog clocks of the test's rows.
func (f *backlogFixture) ageYears(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE fiscal_periods SET updated_at = now() - interval '400 days', created_at = now() - interval '400 days'`)
}

func newBacklogClub(t *testing.T, pool *pgxpool.Pool, perCharge, auto bool) *fin.Club {
	t.Helper()
	c := fin.NewClub(t, pool)
	if _, err := pool.Exec(context.Background(),
		`UPDATE clubs SET vipps_per_charge_enabled = $2, automatic_booking = $3 WHERE id = $1`,
		c.ID, perCharge, auto); err != nil {
		t.Fatalf("club switches: %v", err)
	}
	return c
}

// extraClub is another club in the same database.
func (f *backlogFixture) extraClub(t *testing.T, perCharge, auto bool) *fin.Club {
	t.Helper()
	return newBacklogClub(t, f.club.Pool, perCharge, auto)
}

func (f *backlogFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func june(day int) time.Time { return time.Date(2026, 6, day, 0, 0, 0, 0, time.UTC) }

// charge seeds a Vipps charge on msn, booked on date, that reached Brygge
// age ago.
func (f *backlogFixture) charge(t *testing.T, c *fin.Club, msn string, date time.Time, age time.Duration) string {
	t.Helper()
	id := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: date, MSN: msn})
	f.exec(t, `UPDATE vipps_import_rows SET created_at = now() - make_interval(secs => $2) WHERE id = $1`, id, age.Seconds())
	return id
}

// entry links the Vipps row to a synthetic journal entry in status, posted
// postedAgo ago when posted.
func (f *backlogFixture) entry(t *testing.T, c *fin.Club, rowID, status string, postedAgo time.Duration) {
	t.Helper()
	f.entryNo++
	var postedAt *time.Time
	if status == "posted" {
		p := time.Now().Add(-postedAgo)
		postedAt = &p
	}
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, status, source, created_by, posted_at)
		 VALUES ($1, $2, $3, '2026-06-01', 'Synthetic Vipps charge', $4::journal_status, 'vipps', $5, $6) RETURNING id`,
		c.ID, c.PeriodID, f.entryNo, status, c.Treasurer.ID, postedAt,
	).Scan(&id); err != nil {
		t.Fatalf("journal entry: %v", err)
	}
	f.exec(t, `UPDATE vipps_import_rows SET journal_entry_id = $2 WHERE id = $1`, rowID, id)
}

func (f *backlogFixture) dismissCharge(t *testing.T, rowID string) {
	t.Helper()
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate' WHERE id = $1`,
		rowID, f.club.Treasurer.ID)
}

// settlement seeds a settlement's payout row on msn and date and a bank
// deposit naming it, uploaded uploadedAgo ago; it returns the deposit's id.
func (f *backlogFixture) settlement(t *testing.T, number, msn string, date time.Time, uploadedAgo time.Duration) string {
	t.Helper()
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -44000, Date: date, MSN: msn, Settlement: number})
	id := f.club.NewBankRow(t, f.bankImport, fin.BankRowOpts{
		Date: date.AddDate(0, 0, 1), Cents: 44000, Description: "Utb. " + number + " Vippsnr " + msn,
	})
	f.exec(t, `UPDATE bank_import_rows SET created_at = now() - make_interval(secs => $2) WHERE id = $1`, id, uploadedAgo.Seconds())
	return id
}

func (f *backlogFixture) unbookedAges(t *testing.T) map[string]float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(), unbookedChargeAgeSQL)
	if err != nil {
		t.Fatalf("unbooked charge ages: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var msn string
		var s float64
		if err := rows.Scan(&msn, &s); err != nil {
			t.Fatal(err)
		}
		out[msn] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *backlogFixture) readyPayoutAge(t *testing.T) float64 {
	t.Helper()
	var s float64
	if err := f.club.Pool.QueryRow(context.Background(), readyPayoutAgeSQL, vippsSettlementPatternSQL).Scan(&s); err != nil {
		t.Fatalf("ready payout age: %v", err)
	}
	return s
}

// near reports whether got is want give or take a minute (test runtime and
// the database clock).
func near(got float64, want time.Duration) bool {
	return math.Abs(got-want.Seconds()) < 60
}

func TestUnbookedChargeAgeOldestPerMSN(t *testing.T) {
	f := newBacklogFixture(t)
	c := f.club
	const day = 24 * time.Hour

	// Merchant A: the oldest unbooked charge is 10 days old. A posted
	// charge and a dismissed one, both older, don't count, nor does a fee.
	f.charge(t, c, "100001", june(2), 10*day)
	posted := f.charge(t, c, "100001", june(1), 30*day)
	f.entry(t, c, posted, "posted", 29*day)
	dismissed := f.charge(t, c, "100001", june(1), 40*day)
	f.dismissCharge(t, dismissed)
	fee := c.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -1000, Date: june(1), MSN: "100001"})
	f.exec(t, `UPDATE vipps_import_rows SET created_at = now() - interval '50 days' WHERE id = $1`, fee)

	// Merchant B: a charge on a draft and one on a voided entry are not in
	// the ledger, so they count; a declined one waits for a person and counts.
	draft := f.charge(t, c, "100002", june(3), 5*day)
	f.entry(t, c, draft, "draft", 0)
	voided := f.charge(t, c, "100002", june(3), 6*day)
	f.entry(t, c, voided, "voided", 0)
	declined := f.charge(t, c, "100002", june(3), 8*day)
	f.exec(t, `UPDATE vipps_import_rows SET auto_declined_at = now() WHERE id = $1`, declined)

	// Merchant C: everything posted, so no series.
	cPosted := f.charge(t, c, "100003", june(4), 20*day)
	f.entry(t, c, cPosted, "posted", day)

	// Merchant D: a January charge imported an hour ago is an hour old: a
	// late CSV import does not page at once.
	f.charge(t, c, "100004", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), time.Hour)

	// A charge dated ahead of now reads 0, never a negative age.
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, updated_at)
	           VALUES ($1, 2027, '2027-01-01', '2027-12-31', now() - interval '400 days')`, c.ID)
	f.charge(t, c, "100007", time.Date(2027, 12, 30, 0, 0, 0, 0, time.UTC), 3*day)

	// Merchant E: a dateless row (BRY-584) counts from its import.
	dateless := f.charge(t, c, "100005", june(4), 2*day)
	f.exec(t, `UPDATE vipps_import_rows SET booking_date = NULL, tx_at = NULL WHERE id = $1`, dateless)

	// A charge with no merchant number has a series of its own; a zero or
	// negative charge counts, as it holds its payout up like any other.
	noMSN := f.charge(t, c, "100006", june(4), 3*day)
	f.exec(t, `UPDATE vipps_import_rows SET msn = '' WHERE id = $1`, noMSN)
	negative := f.charge(t, c, "100006", june(4), 4*day)
	f.exec(t, `UPDATE vipps_import_rows SET amount = -100 WHERE id = $1`, negative)

	got := f.unbookedAges(t)
	if len(got) != 7 {
		t.Errorf("series = %v, want merchants 100001, 100002, 100004, 100005, 100006, 100007 and unknown", got)
	}
	for msn, want := range map[string]time.Duration{
		"100001": 10 * day, "100002": 8 * day, "100004": time.Hour, "100005": 2 * day, "100006": 4 * day, "100007": 0, unknownMSN: 3 * day,
	} {
		if !near(got[msn], want) {
			t.Errorf("msn %s = %.0f s, want about %.0f", msn, got[msn], want.Seconds())
		}
	}

	// The registered gauge exports the same, one series per merchant.
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if _, err := NewBacklogGauges(c.Pool, zerolog.Nop()).registerMetrics(mp.Meter("brygge-api")); err != nil {
		t.Fatal(err)
	}
	m, ok := collectMetric(t, reader, "brygge.vipps.unbooked_charge.oldest_age")
	if !ok {
		t.Fatal("unbooked charge gauge not exported")
	}
	var msns []string
	for _, dp := range m.Data.(metricdata.Gauge[float64]).DataPoints {
		v, _ := dp.Attributes.Value(attribute.Key("msn"))
		msns = append(msns, v.AsString())
	}
	slices.Sort(msns)
	if !slices.Equal(msns, []string{"100001", "100002", "100004", "100005", "100006", "100007", unknownMSN}) {
		t.Errorf("exported merchants = %v", msns)
	}
	p, ok := collectMetric(t, reader, "brygge.vipps.ready_payout.oldest_age")
	if !ok || len(p.Data.(metricdata.Gauge[float64]).DataPoints) != 1 || p.Data.(metricdata.Gauge[float64]).DataPoints[0].Value != 0 {
		t.Errorf("ready payout gauge with no deposit = %v (present %v), want one point at 0", p.Data, ok)
	}
}

// Only clubs on the per-charge model with automatic booking on are counted
// (owner decision O2).
func TestUnbookedChargeAgeOnlyAutomaticClubs(t *testing.T) {
	f := newBacklogFixture(t)
	other := f.extraClub(t, true, false)
	legacy := f.extraClub(t, false, true)
	f.charge(t, other, "200001", june(2), 30*24*time.Hour)
	f.charge(t, legacy, "200002", june(2), 30*24*time.Hour)
	if got := f.unbookedAges(t); len(got) != 0 {
		t.Errorf("series = %v, want none", got)
	}
}

func TestReadyPayoutAgeFromGreatest(t *testing.T) {
	f := newBacklogFixture(t)
	c := f.club
	const day = 24 * time.Hour

	if got := f.readyPayoutAge(t); got != 0 {
		t.Fatalf("no deposit = %v, want 0", got)
	}

	ch := f.charge(t, c, "100001", june(2), 10*day)
	f.entry(t, c, ch, "posted", 3*day)
	dep := f.settlement(t, "3000101", "100001", june(2), time.Hour)

	// The deposit was uploaded an hour ago, after its charge was posted:
	// the clock starts at the upload.
	if got := f.readyPayoutAge(t); !near(got, time.Hour) {
		t.Errorf("late upload = %.0f s, want about 3600", got)
	}

	// Uploaded three days ago, but its last charge was only posted two hours
	// ago: the clock restarts at that posting.
	f.exec(t, `UPDATE bank_import_rows SET created_at = now() - interval '3 days' WHERE id = $1`, dep)
	late := f.charge(t, c, "100001", june(2), 9*day)
	f.entry(t, c, late, "posted", 2*time.Hour)
	if got := f.readyPayoutAge(t); !near(got, 2*time.Hour) {
		t.Errorf("late posting = %.0f s, want about 7200", got)
	}

	// A settlement whose charges were all dismissed counts from the upload.
	lone := f.charge(t, c, "100002", june(5), 10*day)
	f.dismissCharge(t, lone)
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now() - interval '60 hours' WHERE id = $1`, lone)
	f.settlement(t, "3000102", "100002", june(5), 50*time.Hour)
	if got := f.readyPayoutAge(t); !near(got, 50*time.Hour) {
		t.Errorf("all charges dismissed = %.0f s, want about 180000", got)
	}

	// A booked deposit is done.
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = (SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1)
	            WHERE description LIKE 'Utb. 3000102 %'`, late)
	if got := f.readyPayoutAge(t); !near(got, 2*time.Hour) {
		t.Errorf("after the older deposit was booked = %.0f s, want about 7200", got)
	}
}

func TestReadyPayoutDeclinedDepositExcluded(t *testing.T) {
	f := newBacklogFixture(t)
	ch := f.charge(t, f.club, "100001", june(2), 10*24*time.Hour)
	f.entry(t, f.club, ch, "posted", 5*24*time.Hour)
	dep := f.settlement(t, "3000101", "100001", june(2), 4*24*time.Hour)
	if got := f.readyPayoutAge(t); !near(got, 4*24*time.Hour) {
		t.Fatalf("ready deposit = %.0f s, want about 4 days", got)
	}
	f.exec(t, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, dep)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("declined deposit = %v, want 0", got)
	}
	// A dismissed deposit is out too.
	f.exec(t, `UPDATE bank_import_rows SET auto_declined_at = NULL, dismissed_at = now(), dismissed_by = $2,
	                   dismissed_reason = 'duplicate' WHERE id = $1`, dep, f.club.Treasurer.ID)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("dismissed deposit = %v, want 0", got)
	}
}

// A settlement with a charge not yet posted waits for the treasurer, not for
// the sweep: VippsChargesUnbooked covers it, this gauge does not.
func TestReadyPayoutWaitingSettlementExcluded(t *testing.T) {
	f := newBacklogFixture(t)
	c := f.club
	const day = 24 * time.Hour
	booked := f.charge(t, c, "100001", june(2), 10*day)
	f.entry(t, c, booked, "posted", 5*day)
	open := f.charge(t, c, "100001", june(2), 10*day)
	f.settlement(t, "3000101", "100001", june(2), 4*day)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("charge in Tildel = %v, want 0", got)
	}
	f.entry(t, c, open, "draft", 0)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("charge on a draft = %v, want 0", got)
	}
	// A charge of another day or merchant does not hold it up.
	f.charge(t, c, "100001", june(3), day)
	f.charge(t, c, "100009", june(2), day)
	f.exec(t, `UPDATE journal_entries SET status = 'posted', posted_at = now() - interval '3 days'
	            WHERE id = (SELECT journal_entry_id FROM vipps_import_rows WHERE id = $1)`, open)
	if got := f.readyPayoutAge(t); !near(got, 3*day) {
		t.Errorf("all charges posted = %.0f s, want about 3 days", got)
	}
	// A payout row without a booking date drops out (the sweep reports it).
	f.exec(t, `UPDATE vipps_import_rows SET booking_date = NULL WHERE row_type = 'payout'`)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("payout without a date = %v, want 0", got)
	}
}

// Rows dated in a reference year never alert (BRY-418), nor do rows in a
// missing, closed or locked year: BRY-575's notice tells the treasurer, and
// the operator is not paged for the same thing.
func TestBacklogGaugesIgnoreReferenceYears(t *testing.T) {
	f := newBacklogFixture(t)
	c := f.club
	const day = 24 * time.Hour
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, reference_only)
	           VALUES ($1, 2025, '2025-01-01', '2025-12-31', 'open', true)`, c.ID)
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
	           VALUES ($1, 2024, '2024-01-01', '2024-12-31', 'closed')`, c.ID)
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
	           VALUES ($1, 2023, '2023-01-01', '2023-12-31', 'locked')`, c.ID)
	f.ageYears(t)

	cases := []struct {
		msn, settlement string
		date            time.Time
	}{
		{"100001", "3000101", time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)}, // reference
		{"100002", "3000102", time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)}, // closed
		{"100003", "3000103", time.Date(2023, 6, 2, 0, 0, 0, 0, time.UTC)}, // locked
		{"100004", "3000104", time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)}, // missing
	}
	for _, tc := range cases {
		f.charge(t, c, tc.msn, tc.date, 30*day)
		posted := f.charge(t, c, tc.msn, tc.date, 30*day)
		f.entry(t, c, posted, "posted", 20*day)
		f.settlement(t, tc.settlement, tc.msn, tc.date, 20*day)
	}
	// With the unbooked charges dismissed, each settlement would be ready
	// but for its year.
	if got := f.unbookedAges(t); len(got) != 0 {
		t.Errorf("unbooked charges = %v, want none", got)
	}
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate'
	            WHERE club_id = $1 AND row_type = 'belastning' AND journal_entry_id IS NULL`, c.ID, c.Treasurer.ID)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("ready payout = %v, want 0", got)
	}

	// The same rows in the open year do alert, so the exclusion is the year.
	f.charge(t, c, "100005", june(2), 30*day)
	f.settlement(t, "3000105", "100006", june(2), 20*day)
	if got := f.unbookedAges(t); !near(got["100005"], 30*day) || len(got) != 1 {
		t.Errorf("open year unbooked = %v, want 100005 at 30 days", got)
	}
	if got := f.readyPayoutAge(t); !near(got, 20*day) {
		t.Errorf("open year ready payout = %.0f s, want about 20 days", got)
	}
}

// A club on the legacy single-bilag model, or with automatic booking off, is
// never swept, so its deposits are not counted.
func TestReadyPayoutLegacyClubExcluded(t *testing.T) {
	f := newBacklogFixture(t)
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = false WHERE id = $1`, f.club.ID)
	f.settlement(t, "3000101", "100001", june(2), 10*24*time.Hour)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("legacy club = %v, want 0", got)
	}
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = true, automatic_booking = false WHERE id = $1`, f.club.ID)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("automatic booking off = %v, want 0", got)
	}
	f.exec(t, `UPDATE clubs SET automatic_booking = true WHERE id = $1`, f.club.ID)
	if got := f.readyPayoutAge(t); !near(got, 10*24*time.Hour) {
		t.Errorf("per-charge club with automatic booking = %.0f s, want about 10 days", got)
	}
}

// Postgres' regexp_match with vippsSettlementPatternSQL captures what
// VippsSettlementPattern captures in Go.
func TestVippsSettlementPatternSQLMatchesPostgres(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	for _, desc := range settlementPatternSamples {
		var got []string
		if err := pool.QueryRow(context.Background(),
			`SELECT regexp_match($1, $2, 'i')`, desc, vippsSettlementPatternSQL).Scan(&got); err != nil {
			t.Fatalf("%q: %v", desc, err)
		}
		var want []string
		if m := VippsSettlementPattern.FindStringSubmatch(desc); m != nil {
			want = m[1:]
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: Postgres = %q, Go = %q", desc, got, want)
		}
	}
}

// The payout clock restarts when the sweep could first clear the deposit:
// when the last blocking charge is dismissed, and when the deposit's year is
// reopened. The charge clock restarts when the charge's year is created or
// reopened.
func TestBacklogClocksRestart(t *testing.T) {
	f := newBacklogFixture(t)
	c := f.club
	ctx := context.Background()
	const day = 24 * time.Hour

	booked := f.charge(t, c, "100001", june(2), 10*day)
	f.entry(t, c, booked, "posted", 5*day)
	blocking := f.charge(t, c, "100001", june(2), 10*day)
	f.settlement(t, "3000101", "100001", june(2), 4*day)
	if got := f.readyPayoutAge(t); got != 0 {
		t.Fatalf("held by a charge in Tildel = %v, want 0", got)
	}
	f.dismissCharge(t, blocking)
	if got := f.readyPayoutAge(t); !near(got, 0) {
		t.Errorf("after the last blocking charge was dismissed = %.0f s, want about 0", got)
	}

	// Close and reopen the year: both gauges count from the reopening.
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now() - interval '3 days' WHERE id = $1`, blocking)
	f.charge(t, c, "100002", june(3), 10*day)
	if got := f.readyPayoutAge(t); !near(got, 3*day) {
		t.Fatalf("before the close = %.0f s, want about 3 days", got)
	}
	if err := f.svc.ClosePeriod(ctx, c.ID, c.PeriodID, c.Treasurer.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := f.readyPayoutAge(t); got != 0 {
		t.Errorf("closed year = %v, want 0", got)
	}
	if got := f.unbookedAges(t); len(got) != 0 {
		t.Errorf("closed year unbooked = %v, want none", got)
	}
	if err := f.svc.ReopenPeriod(ctx, c.ID, c.PeriodID); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := f.readyPayoutAge(t); !near(got, 0) {
		t.Errorf("after the reopening = %.0f s, want about 0", got)
	}
	if got := f.unbookedAges(t); !near(got["100002"], 0) {
		t.Errorf("unbooked after the reopening = %v, want 100002 at about 0", got)
	}

	// A charge waiting for a year nobody has opened yet counts from the
	// year's creation.
	f.charge(t, c, "100003", time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC), 30*day)
	if got := f.unbookedAges(t); len(got) != 1 {
		t.Errorf("missing year unbooked = %v, want only 100002", got)
	}
	if _, err := f.svc.CreatePeriod(ctx, c.ID, 2025); err != nil {
		t.Fatalf("create 2025: %v", err)
	}
	if got := f.unbookedAges(t); !near(got["100003"], 0) {
		t.Errorf("after the year was created = %v, want 100003 at about 0", got)
	}
}

// A run that books nothing by design (accounting off, or the club's switch
// off) tells the observer it did not fail, even when its context ended
// during the ingest, so the operator's last-run-failed metric resets.
func TestRunAutomaticBookingOffReportsNotFailed(t *testing.T) {
	f := newSlotFixture(t)
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if res := f.svc.RunAutomaticBooking(ctx, f.club.ID, AutoBookingOpts{Trigger: AutoBookingTriggerBoot}); !res.Aborted {
		t.Fatalf("accounting off with an ended context: aborted %v, want true", res.Aborted)
	}
	if res := f.svc.RunAutomaticBooking(ctx, f.club.ID, AutoBookingOpts{Trigger: AutoBookingTriggerBoot, Accounting: true}); !res.Aborted || res.Booked {
		t.Fatalf("switch off with an ended context = %+v, want aborted and nothing booked", res)
	}
	if !slices.Equal(obs.runs, []bool{false, false}) {
		t.Errorf("observer runs = %v, want two not failed", obs.runs)
	}
}
