package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-575: the treasurer's notice that payments wait for a fiscal year. Every
// club, row and address is synthetic.

type fySenderEnv struct {
	pool   *pgxpool.Pool
	svc    *accounting.Service
	club   *fin.Club
	imp    string
	mailer *email.MockSender
	sender *FiscalYearWaitingSender
}

func newFYSenderEnv(t *testing.T) *fySenderEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	mailer := &email.MockSender{}
	cfg := &config.Config{FrontendURL: "https://club.example.invalid/"}
	e := &fySenderEnv{pool: pool, svc: svc, club: club, imp: club.NewBankImport(t), mailer: mailer,
		sender: NewFiscalYearWaitingSender(pool, cfg, mailer, svc, zerolog.Nop())}
	e.exec(t, `UPDATE clubs SET treasurer_email = '', board_notification_email = '',
	                            secretary_email = '', chairman_email = '' WHERE id = $1`, club.ID)
	return e
}

func (e *fySenderEnv) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *fySenderEnv) kidRow(t *testing.T, date time.Time) {
	t.Helper()
	inv := e.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	e.club.NewBankRow(t, e.imp, fin.BankRowOpts{Date: date, Cents: 45000, KID: inv.KID})
}

// queued notifies and returns the club's notices as the worker claims them.
func (e *fySenderEnv) queued(t *testing.T) []bcast.Claimed {
	t.Helper()
	ctx := context.Background()
	if _, err := e.svc.NotifyWaitingFiscalYears(ctx, e.club.ID, time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("notify: %v", err)
	}
	claimed, err := bcast.NewStore(e.pool).ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return claimed
}

func (e *fySenderEnv) send(t *testing.T, c bcast.Claimed) error {
	t.Helper()
	return e.sender.Send(context.Background(), c)
}

func TestFiscalYearNoticeRecipient(t *testing.T) {
	cases := []struct {
		name, treasurer, board, want string
	}{
		{"treasurer", "kasserer@example.invalid", "styret@example.invalid", "kasserer@example.invalid"},
		{"board when no treasurer", "", "styret@example.invalid", "styret@example.invalid"},
		{"board when the treasurer address is not a bare mailbox", "Kasserer <kasserer@example.invalid>", "styret@example.invalid", "styret@example.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFYSenderEnv(t)
			e.exec(t, `UPDATE clubs SET treasurer_email = $2, board_notification_email = $3 WHERE id = $1`,
				e.club.ID, tc.treasurer, tc.board)
			e.kidRow(t, time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC))
			claimed := e.queued(t)
			if len(claimed) != 1 || claimed[0].Kind != bcast.KindAccountingFiscalYearWaiting {
				t.Fatalf("claimed = %+v, want one fiscal year notice", claimed)
			}
			if err := e.send(t, claimed[0]); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(e.mailer.Calls) != 1 {
				t.Fatalf("mails = %d, want 1", len(e.mailer.Calls))
			}
			m := e.mailer.Calls[0]
			if m.To != tc.want {
				t.Errorf("to = %q, want %q", m.To, tc.want)
			}
			if m.Subject != "Opprett regnskapsåret 2027" {
				t.Errorf("subject = %q", m.Subject)
			}
			for _, want := range []string{
				"https://club.example.invalid/admin/accounting/journal?openPeriod=2027",
				"1 betaling datert i 2027", "02.01.2027", "Lukk det gamle året først når det nye finnes",
			} {
				if !strings.Contains(m.HTML, want) {
					t.Errorf("body lacks %q", want)
				}
			}
		})
	}
}

func TestFiscalYearNoticeUnaddressedIsRequeued(t *testing.T) {
	ctx := context.Background()
	e := newFYSenderEnv(t)
	e.kidRow(t, time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC))
	claimed := e.queued(t)
	err := e.send(t, claimed[0])
	if !errors.Is(err, bcast.ErrUnsendable) || !errors.Is(err, errNoTreasurerAddress) {
		t.Fatalf("send with no address = %v, want errNoTreasurerAddress", err)
	}
	if err := bcast.NewStore(e.pool).Mark(ctx, claimed[0].DeliveryID, bcast.DeliveryFailed, err.Error()); err != nil {
		t.Fatal(err)
	}
	if n, err := e.sender.RequeueUnaddressed(ctx); err != nil || n != 0 {
		t.Fatalf("requeue with still no address = %d, %v; want 0", n, err)
	}
	// The hourly sweep leaves it failed: only RequeueUnaddressed puts it back.
	if n, err := e.svc.NotifyWaitingFiscalYears(ctx, e.club.ID, time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)); err != nil || n != 0 {
		t.Fatalf("sweep with no address = %d, %v; want 0", n, err)
	}
	e.exec(t, `UPDATE clubs SET treasurer_email = 'kasserer@example.invalid' WHERE id = $1`, e.club.ID)
	if n, err := e.sender.RequeueUnaddressed(ctx); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v; want 1", n, err)
	}
	again, err := bcast.NewStore(e.pool).ClaimPending(ctx, 10)
	if err != nil || len(again) != 1 {
		t.Fatalf("claim after requeue = %+v, %v", again, err)
	}
	if err := e.send(t, again[0]); err != nil || len(e.mailer.Calls) != 1 {
		t.Fatalf("send after requeue = %v, mails %d", err, len(e.mailer.Calls))
	}
}

func TestFiscalYearNoticeDroppedOnceResolved(t *testing.T) {
	e := newFYSenderEnv(t)
	e.exec(t, `UPDATE clubs SET treasurer_email = 'kasserer@example.invalid' WHERE id = $1`, e.club.ID)
	e.kidRow(t, time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC))
	claimed := e.queued(t)
	if _, err := e.svc.CreatePeriod(context.Background(), e.club.ID, 2027); err != nil {
		t.Fatal(err)
	}
	if err := e.send(t, claimed[0]); !errors.Is(err, errFiscalYearResolved) {
		t.Fatalf("send after the year exists = %v, want errFiscalYearResolved", err)
	}
	if len(e.mailer.Calls) != 0 {
		t.Errorf("mailed %d notices for a condition that no longer holds", len(e.mailer.Calls))
	}
}

func TestFiscalYearNoticeClosedAndLockedWording(t *testing.T) {
	cases := []struct {
		status, subject, link string
		want, never           []string
	}{
		{
			status:  "closed",
			subject: "Regnskapsåret 2025 er lukket – 2 betalinger venter",
			link:    "?reopenPeriod=",
			want:    []string{"Gjenåpne", "lukk året igjen etterpå"},
		},
		{
			status:  "locked",
			subject: "Regnskapsåret 2025 er låst – 2 betalinger venter",
			link:    "?period=",
			want:    []string{"låses opp utenfor appen"},
			never:   []string{"reopenPeriod", "Gjenåpne", "Tildel"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			e := newFYSenderEnv(t)
			e.exec(t, `UPDATE clubs SET treasurer_email = 'kasserer@example.invalid' WHERE id = $1`, e.club.ID)
			var periodID string
			if err := e.pool.QueryRow(context.Background(),
				`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at)
				 VALUES ($1, 2025, '2025-01-01', '2025-12-31', $2::fiscal_period_status, now()) RETURNING id`,
				e.club.ID, tc.status).Scan(&periodID); err != nil {
				t.Fatal(err)
			}
			e.kidRow(t, time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC))
			e.kidRow(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
			claimed := e.queued(t)
			if len(claimed) != 1 {
				t.Fatalf("claimed = %+v", claimed)
			}
			var p accounting.FiscalYearWaitingPayload
			if err := json.Unmarshal(claimed[0].Payload, &p); err != nil || p.State != tc.status || p.PeriodID != periodID {
				t.Fatalf("payload = %+v, %v", p, err)
			}
			if err := e.send(t, claimed[0]); err != nil {
				t.Fatal(err)
			}
			m := e.mailer.Calls[0]
			if m.Subject != tc.subject {
				t.Errorf("subject = %q, want %q", m.Subject, tc.subject)
			}
			if !strings.Contains(m.HTML, "/admin/accounting/journal"+tc.link+periodID) {
				t.Errorf("body lacks the link %s%s", tc.link, periodID)
			}
			for _, w := range tc.want {
				if !strings.Contains(m.HTML, w) {
					t.Errorf("body lacks %q", w)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(m.HTML, n) {
					t.Errorf("body has %q", n)
				}
			}
		})
	}
}

// The whole loop: payments dated in a year nobody opened wait; the treasurer
// creates the year through the route the email's link leads to; the condition
// clears, and the next sync books each payment in its own year.
func TestMissingYearClosesAndPaymentBookedInOwnYear(t *testing.T) {
	ctx := context.Background()
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Post("/periods", h.HandleCreatePeriod)
	})
	club := env.club
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := club.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	exec(`UPDATE clubs SET vipps_auto_assign = true, vipps_per_charge_enabled = true WHERE id = $1`, club.ID)
	exec(`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
	      VALUES ($1, 'service', 'Synthetic slipping', 300, 'once', true, 'all')`, club.ID)
	jan2 := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: jan2, CustomerName: "Synthetic Walkup"})
	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	bankRow := club.NewBankRow(t, club.NewBankImport(t), fin.BankRowOpts{Date: jan2, Cents: 45000, KID: inv.KID})

	// 1. The condition holds.
	waiting, err := svc.WaitingFiscalYears(ctx, club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Year != 2027 || waiting[0].State != accounting.WaitingMissing || waiting[0].Payments != 2 {
		t.Fatalf("waiting = %+v, want 2027 missing with 2 payments", waiting)
	}

	// 2. The treasurer creates the year.
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/periods", map[string]int{"year": 2027})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create period = %d %s", rec.Code, rec.Body.String())
	}
	var created accounting.FiscalPeriod
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// 3. Nothing waits any more.
	if waiting, err = svc.WaitingFiscalYears(ctx, club.ID); err != nil || len(waiting) != 0 {
		t.Fatalf("waiting after the year exists = %+v, %v", waiting, err)
	}

	// 4. The next sync books both payments in 2027, on their own date.
	if _, err := svc.BankSync(ctx, club.ID, club.Treasurer.ID); err != nil {
		t.Fatalf("BankSync: %v", err)
	}
	if _, err := svc.BookVippsQueue(ctx, club.ID, club.Treasurer.ID, time.Second); err != nil {
		t.Fatalf("BookVippsQueue: %v", err)
	}
	for name, q := range map[string]string{
		"bank row":     `SELECT je.entry_date::text, je.fiscal_period_id::text FROM bank_import_rows r JOIN journal_entries je ON je.id = r.journal_entry_id WHERE r.id = $1`,
		"vipps charge": `SELECT je.entry_date::text, je.fiscal_period_id::text FROM vipps_import_rows r JOIN journal_entries je ON je.id = r.journal_entry_id WHERE r.id = $1`,
	} {
		id := bankRow
		if name == "vipps charge" {
			id = charge
		}
		var date, period string
		if err := club.Pool.QueryRow(ctx, q, id).Scan(&date, &period); err != nil {
			t.Fatalf("%s not booked: %v", name, err)
		}
		if date != "2027-01-02" || period != created.ID {
			t.Errorf("%s booked on %s in period %s, want 2027-01-02 in %s", name, date, period, created.ID)
		}
	}

	// 5. No other period was created.
	var periods int
	if err := club.Pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1`, club.ID).Scan(&periods); err != nil {
		t.Fatal(err)
	}
	if periods != 2 {
		t.Errorf("fiscal periods = %d, want 2026 and 2027", periods)
	}
}
