package accounting

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A bank payment linked to a join faktura (KID match or manual assign, both
// through linkInvoicePayment) grants membership in the same transaction, once.
func TestLinkInvoicePaymentGrantsJoinMembership(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	c := fin.NewClub(t, pool)
	s := NewService(pool, nil, zerolog.Nop())

	item := c.PriceItem(t, "membership")
	mustExecSQL(t, pool, `UPDATE price_items SET amount = 650 WHERE id = $1`, item)
	mustExecSQL(t, pool, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, c.ID, item)

	joinInvoice := func(t *testing.T, status string) (fin.Member, string) {
		t.Helper()
		a := c.NewMember(t, "applicant")
		inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &a, Status: status, Lines: []fin.Line{{Category: "membership", Cents: 65000}}})
		mustExecSQL(t, pool, `UPDATE invoices SET purpose = 'membership_join' WHERE id = $1`, inv.ID)
		return a, inv.ID
	}
	state := func(t *testing.T, userID string) (roles []string, queued int) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT array_agg(role::text ORDER BY role::text) FROM user_roles WHERE user_id = $1), '{}'),
			        (SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind LIKE 'membership_%')`,
			userID,
		).Scan(&roles, &queued); err != nil {
			t.Fatal(err)
		}
		return roles, queued
	}

	t.Run("kid payment grants once", func(t *testing.T) {
		a, invID := joinInvoice(t, "open")
		for range 2 {
			if err := s.linkInvoicePayment(ctx, c.ID, invID, time.Now()); err != nil {
				t.Fatalf("linkInvoicePayment: %v", err)
			}
		}
		roles, queued := state(t, a.ID)
		if !slices.Equal(roles, []string{"member"}) || queued != 2 {
			t.Fatalf("roles = %v, queued = %d; want [member] and 2", roles, queued)
		}
		var source string
		if err := pool.QueryRow(ctx,
			`SELECT details->>'source' FROM audit_log WHERE resource_id = $1 AND action = 'membership.granted'`, a.ID,
		).Scan(&source); err != nil || source != "kid" {
			t.Fatalf("audit source = %q, %v", source, err)
		}
	})

	t.Run("a payment linked to a voided join faktura does not grant", func(t *testing.T) {
		a, invID := joinInvoice(t, "voided")
		if err := s.linkInvoicePayment(ctx, c.ID, invID, time.Now()); err != nil {
			t.Fatalf("linkInvoicePayment: %v", err)
		}
		if roles, queued := state(t, a.ID); !slices.Equal(roles, []string{"applicant"}) || queued != 0 {
			t.Fatalf("roles = %v, queued = %d", roles, queued)
		}
	})

	t.Run("inside a caller's transaction the grant commits with it", func(t *testing.T) {
		a, invID := joinInvoice(t, "open")
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := linkInvoicePaymentIn(ctx, tx, c.ID, invID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if roles, _ := state(t, a.ID); !slices.Equal(roles, []string{"applicant"}) {
			t.Fatalf("rolled back, yet roles = %v", roles)
		}
	})
}

// joinFixture is a club taking joins at 650 kr, for the concurrency and
// failure tests below.
type joinFixture struct {
	pool *pgxpool.Pool
	club *fin.Club
	svc  *Service
}

func newJoinFixture(t *testing.T) *joinFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(8))
	c := fin.NewClub(t, pool)
	item := c.PriceItem(t, "membership")
	mustExecSQL(t, pool, `UPDATE price_items SET amount = 650 WHERE id = $1`, item)
	mustExecSQL(t, pool, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, c.ID, item)
	return &joinFixture{pool: pool, club: c, svc: NewService(pool, nil, zerolog.Nop())}
}

func (f *joinFixture) joinInvoice(t *testing.T) (fin.Member, fin.Invoice) {
	t.Helper()
	a := f.club.NewMember(t, "applicant")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &a, Lines: []fin.Line{{Category: "membership", Cents: 65000}}})
	mustExecSQL(t, f.pool, `UPDATE invoices SET purpose = 'membership_join' WHERE id = $1`, inv.ID)
	return a, inv
}

func (f *joinFixture) counts(t *testing.T, userID, invoiceID string) (payments, grants, queued int, roles []string) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM payments WHERE user_id = $1),
		        (SELECT count(*) FROM audit_log WHERE resource_id = $1::text AND action = 'membership.granted'),
		        (SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind IN ('membership_welcome', 'membership_board_notice')),
		        COALESCE((SELECT array_agg(role::text ORDER BY role::text) FROM user_roles WHERE user_id = $1), '{}')`,
		userID,
	).Scan(&payments, &grants, &queued, &roles); err != nil {
		t.Fatal(err)
	}
	return payments, grants, queued, roles
}

// Two links of the same faktura at once leave one payment row: the second
// waits on the faktura's lock, finds it linked and writes nothing.
func TestLinkInvoicePaymentRaceLeavesOnePayment(t *testing.T) {
	f := newJoinFixture(t)
	ctx := context.Background()
	for i := range 5 {
		a, inv := f.joinInvoice(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for g := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[g] = f.svc.linkInvoicePayment(ctx, f.club.ID, inv.ID, time.Now())
			}()
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: %v", i, errs)
		}
		payments, grants, queued, roles := f.counts(t, a.ID, inv.ID)
		if payments != 1 || grants != 1 || queued != 2 || !slices.Equal(roles, []string{"member"}) {
			t.Fatalf("round %d: payments %d, grants %d, queued %d, roles %v", i, payments, grants, queued, roles)
		}
	}
}

// An online capture and a KID link of the same join faktura at once grant
// once and queue the two emails once. The capture side is what settleCapture
// writes for an order paying the faktura.
func TestCaptureAndKIDLinkRaceGrantOnce(t *testing.T) {
	f := newJoinFixture(t)
	ctx := context.Background()
	for i := range 5 {
		a, inv := f.joinInvoice(t)
		order := f.club.NewOrderForInvoice(t, inv)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE orders SET status = 'paid', paid_at = now() WHERE id = $1`, order.ID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount, status)
					 VALUES ($1, $2, 'vipps', $3, 650, 'captured')`, f.club.ID, order.ID, "ref-"+testutil.RandomHex(8)); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE invoices SET online_paid_at = now(), online_paid_provider = 'vipps' WHERE id = $1`, inv.ID); err != nil {
					return err
				}
				membership.GrantForOrderInSavepoint(ctx, tx, order.ID, membership.SourceOnline)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			errs[1] = f.svc.linkInvoicePayment(ctx, f.club.ID, inv.ID, time.Now())
		}()
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: %v", i, errs)
		}
		_, grants, queued, roles := f.counts(t, a.ID, inv.ID)
		if grants != 1 || queued != 2 || !slices.Equal(roles, []string{"member"}) {
			t.Fatalf("round %d: grants %d, queued %d, roles %v", i, grants, queued, roles)
		}
	}
}

// A grant that fails inside the bank link is rolled back alone: the payment
// is linked, and the membership sweep grants afterwards.
func TestLinkInvoicePaymentSurvivesGrantFailure(t *testing.T) {
	f := newJoinFixture(t)
	ctx := context.Background()
	a, inv := f.joinInvoice(t)
	mustExecSQL(t, f.pool, `CREATE OR REPLACE FUNCTION test_fail_member_grant() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.role = 'member' THEN RAISE EXCEPTION 'synthetic grant failure'; END IF;
			RETURN NEW;
		END $$`)
	mustExecSQL(t, f.pool, `CREATE TRIGGER test_fail_member_grant BEFORE INSERT ON user_roles
		FOR EACH ROW EXECUTE FUNCTION test_fail_member_grant()`)

	if err := f.svc.linkInvoicePayment(ctx, f.club.ID, inv.ID, time.Now()); err != nil {
		t.Fatalf("linkInvoicePayment failed with the grant: %v", err)
	}
	var linked bool
	if err := f.pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&linked); err != nil || !linked {
		t.Fatalf("faktura linked = %v, %v", linked, err)
	}
	if _, grants, _, roles := f.counts(t, a.ID, inv.ID); grants != 0 || !slices.Equal(roles, []string{"applicant"}) {
		t.Fatalf("grants %d, roles %v; want none yet", grants, roles)
	}

	mustExecSQL(t, f.pool, `DROP TRIGGER test_fail_member_grant ON user_roles`)
	if res, err := membership.Sweep(ctx, f.pool); err != nil || res.Granted != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	if _, grants, queued, roles := f.counts(t, a.ID, inv.ID); grants != 1 || queued != 2 || !slices.Equal(roles, []string{"member"}) {
		t.Fatalf("after sweep: grants %d, queued %d, roles %v", grants, queued, roles)
	}
}

func mustExecSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
