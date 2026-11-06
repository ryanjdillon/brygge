package membership

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// joinEnv is a club that takes joins: a membership price item of 650 kr set
// as the club's join item. All data is synthetic.
type joinEnv struct {
	pool   *pgxpool.Pool
	club   *fin.Club
	itemID string
}

const joinPriceCents = 65000

func setupJoin(t *testing.T) *joinEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	c := fin.NewClub(t, pool)
	item := c.PriceItem(t, "membership")
	exec(t, pool, `UPDATE price_items SET amount = $2 WHERE id = $1`, item, fin.Kr(joinPriceCents))
	exec(t, pool, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, c.ID, item)
	return &joinEnv{pool: pool, club: c, itemID: item}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// joinOrder seeds an applicant's join order for the join item at
// lineCents, paid by a captured attempt of the same amount when paid.
func (e *joinEnv) joinOrder(t *testing.T, applicant fin.Member, lineCents int64, paid bool) string {
	t.Helper()
	ctx := context.Background()
	var orderID string
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token, purpose)
		 VALUES ($1, $2, $3, $4, 'membership_join') RETURNING id`,
		e.club.ID, applicant.ID, fin.Kr(lineCents), "tok-"+testutil.RandomHex(12),
	).Scan(&orderID); err != nil {
		t.Fatalf("join order: %v", err)
	}
	exec(t, e.pool,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, 'Medlemskontingent', 1, $3, $3)`,
		orderID, e.itemID, fin.Kr(lineCents))
	if paid {
		exec(t, e.pool, `UPDATE orders SET status = 'paid', paid_at = now() WHERE id = $1`, orderID)
		exec(t, e.pool,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount, status)
			 VALUES ($1, $2, 'vipps', $3, $4, 'captured')`,
			e.club.ID, orderID, "ref-"+testutil.RandomHex(8), fin.Kr(lineCents))
	}
	return orderID
}

// joinInvoice seeds an applicant's join faktura (the "Betal med bank" path).
func (e *joinEnv) joinInvoice(t *testing.T, applicant fin.Member, o fin.InvoiceOpts) fin.Invoice {
	t.Helper()
	o.Member = &applicant
	if len(o.Lines) == 0 {
		o.Lines = []fin.Line{{Category: "membership", Cents: joinPriceCents}}
	}
	inv := e.club.NewInvoice(t, o)
	exec(t, e.pool, `UPDATE invoices SET purpose = 'membership_join' WHERE id = $1`, inv.ID)
	return inv
}

type memberState struct {
	roles       []string
	memberSince bool
	welcome     int
	board       int
	granted     int
}

func (e *joinEnv) state(t *testing.T, userID string) memberState {
	t.Helper()
	ctx := context.Background()
	var s memberState
	if err := e.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT array_agg(role::text ORDER BY role::text) FROM user_roles WHERE user_id = u.id), '{}'),
		        u.member_since IS NOT NULL,
		        (SELECT count(*) FROM deliveries WHERE user_id = u.id AND kind = 'membership_welcome'),
		        (SELECT count(*) FROM deliveries WHERE user_id = u.id AND kind = 'membership_board_notice'),
		        (SELECT count(*) FROM audit_log WHERE resource_id = u.id::text AND action = 'membership.granted')
		   FROM users u WHERE u.id = $1`, userID,
	).Scan(&s.roles, &s.memberSince, &s.welcome, &s.board, &s.granted); err != nil {
		t.Fatalf("load state: %v", err)
	}
	return s
}

func (e *joinEnv) withheldNotices(t *testing.T, userID string) int {
	t.Helper()
	var n, audits int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind = 'membership_grant_withheld'),
		        (SELECT count(*) FROM audit_log WHERE resource_id = $1::text AND action = 'membership.grant_withheld')`,
		userID,
	).Scan(&n, &audits); err != nil {
		t.Fatal(err)
	}
	if n != audits {
		t.Fatalf("withheld notices = %d but audit rows = %d", n, audits)
	}
	return n
}

func (e *joinEnv) assertMember(t *testing.T, userID string) {
	t.Helper()
	s := e.state(t, userID)
	if !slices.Equal(s.roles, []string{"member"}) || !s.memberSince || s.welcome != 1 || s.board != 1 || s.granted != 1 {
		t.Fatalf("want a member granted once with both emails queued, got %+v", s)
	}
}

func (e *joinEnv) assertApplicant(t *testing.T, userID string) {
	t.Helper()
	s := e.state(t, userID)
	if !slices.Equal(s.roles, []string{"applicant"}) || s.memberSince || s.welcome != 0 || s.board != 0 || s.granted != 0 {
		t.Fatalf("want an untouched applicant, got %+v", s)
	}
}

func grantOrderTx(t *testing.T, e *joinEnv, orderID string) []Outcome {
	t.Helper()
	var out []Outcome
	if err := inSavepoint(context.Background(), e.pool, func(tx pgx.Tx) error {
		var err error
		out, err = GrantForOrderTx(context.Background(), tx, orderID, SourceOnline)
		return err
	}); err != nil {
		t.Fatalf("GrantForOrderTx: %v", err)
	}
	return out
}

func grantInvoiceTx(t *testing.T, e *joinEnv, invoiceID string) Outcome {
	t.Helper()
	var out Outcome
	if err := inSavepoint(context.Background(), e.pool, func(tx pgx.Tx) error {
		var err error
		out, err = GrantForInvoiceTx(context.Background(), tx, invoiceID, SourceKID)
		return err
	}); err != nil {
		t.Fatalf("GrantForInvoiceTx: %v", err)
	}
	return out
}

func TestGrantForOrder(t *testing.T) {
	e := setupJoin(t)

	t.Run("a captured join order grants once", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		out := grantOrderTx(t, e, order)
		if len(out) != 1 || !out[0].Granted {
			t.Fatalf("outcomes = %+v, want one grant", out)
		}
		e.assertMember(t, a.ID)

		// A redelivered capture grants nothing and queues nothing more.
		out = grantOrderTx(t, e, order)
		if len(out) != 1 || out[0].Granted || out[0].Withheld != "" {
			t.Fatalf("second outcomes = %+v, want a silent no-op", out)
		}
		e.assertMember(t, a.ID)
	})

	t.Run("an unpaid order does not grant", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, false)
		if out := grantOrderTx(t, e, order); len(out) != 1 || out[0].Granted {
			t.Fatalf("outcomes = %+v", out)
		}
		e.assertApplicant(t, a.ID)
	})

	t.Run("a refunded order does not grant", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		exec(t, e.pool, `UPDATE orders SET status = 'refunded' WHERE id = $1`, order)
		grantOrderTx(t, e, order)
		e.assertApplicant(t, a.ID)
	})

	t.Run("an ordinary order is a no-op", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		exec(t, e.pool, `UPDATE orders SET purpose = NULL WHERE id = $1`, order)
		if out := grantOrderTx(t, e, order); len(out) != 0 {
			t.Fatalf("outcomes = %+v, want none", out)
		}
		e.assertApplicant(t, a.ID)
	})

	tamper := []struct {
		name   string
		mutate func(t *testing.T, order string)
		reason string
	}{
		{"line total differs from quantity times unit price", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE order_lines SET total_price = 1 WHERE order_id = $1`, order)
		}, withheldLineMismatch},
		{"line with no amount", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE order_lines SET unit_price = 0, total_price = 0 WHERE order_id = $1`, order)
		}, withheldLineNoAmount},
		{"line moved to an item that is no join item", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE order_lines SET price_item_id = $2 WHERE order_id = $1`, order, e.club.PriceItem(t, "slip_fee"))
		}, withheldNoJoinLine},
		{"order total below the line", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE orders SET total_amount = 1 WHERE id = $1`, order)
		}, withheldTotalBelowLine},
		{"captured less than the order total", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE payment_attempts SET amount = 1 WHERE order_id = $1`, order)
		}, withheldCaptureBelow},
		{"no capture", func(t *testing.T, order string) {
			exec(t, e.pool, `UPDATE payment_attempts SET status = 'failed' WHERE order_id = $1`, order)
		}, withheldCaptureBelow},
	}
	for _, tc := range tamper {
		t.Run("withheld: "+tc.name, func(t *testing.T) {
			a := e.club.NewMember(t, "applicant")
			order := e.joinOrder(t, a, joinPriceCents, true)
			tc.mutate(t, order)
			out := grantOrderTx(t, e, order)
			if len(out) != 1 || out[0].Granted || out[0].Withheld != tc.reason {
				t.Fatalf("outcomes = %+v, want withheld %q", out, tc.reason)
			}
			e.assertApplicant(t, a.ID)
			// The board hears of it once, however often settlement runs.
			grantOrderTx(t, e, order)
			if n := e.withheldNotices(t, a.ID); n != 1 {
				t.Fatalf("withheld notices = %d, want 1", n)
			}
		})
	}

	// The source is judged against itself: a price raised, the join item
	// moved, or joining closed after the applicant confirmed does not
	// withhold what they paid.
	clubChanges := []struct {
		name   string
		change string
		undo   string
	}{
		{"price raised after confirm", `UPDATE price_items SET amount = 700 WHERE id = $2 AND club_id = $1`, `UPDATE price_items SET amount = 650 WHERE id = $2 AND club_id = $1`},
		{"joining closed after payment", `UPDATE clubs SET join_price_item_id = NULL WHERE id = $1 AND $2::uuid IS NOT NULL`, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`},
		{"join item moved to a new item", `UPDATE clubs SET join_price_item_id = (SELECT id FROM price_items WHERE club_id = $1 AND category = 'slip_fee' AND $2::uuid IS NOT NULL) WHERE id = $1`, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`},
	}
	for _, tc := range clubChanges {
		t.Run("granted: "+tc.name, func(t *testing.T) {
			e.club.PriceItem(t, "slip_fee")
			a := e.club.NewMember(t, "applicant")
			order := e.joinOrder(t, a, joinPriceCents, true)
			exec(t, e.pool, tc.change, e.club.ID, e.itemID)
			defer exec(t, e.pool, tc.undo, e.club.ID, e.itemID)
			if out := grantOrderTx(t, e, order); len(out) != 1 || !out[0].Granted {
				t.Fatalf("outcomes = %+v", out)
			}
			e.assertMember(t, a.ID)
		})
	}

	t.Run("a deactivated applicant is not granted", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		exec(t, e.pool, `UPDATE users SET deactivated_at = now() WHERE id = $1`, a.ID)
		out := grantOrderTx(t, e, order)
		if len(out) != 1 || out[0].Withheld != withheldDeactivated {
			t.Fatalf("outcomes = %+v", out)
		}
		e.assertApplicant(t, a.ID)
		if n := e.withheldNotices(t, a.ID); n != 1 {
			t.Fatalf("withheld notices = %d, want 1", n)
		}
	})

	t.Run("an order paying a join faktura grants", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{})
		order := e.club.NewOrderForInvoice(t, inv)
		// What markInvoicesPaidOnline writes in the capture's transaction.
		exec(t, e.pool, `UPDATE invoices SET online_paid_at = now(), online_paid_provider = 'vipps' WHERE id = $1`, inv.ID)
		out := grantOrderTx(t, e, order.ID)
		if len(out) != 1 || !out[0].Granted {
			t.Fatalf("outcomes = %+v", out)
		}
		e.assertMember(t, a.ID)
	})
}

func TestGrantForInvoice(t *testing.T) {
	e := setupJoin(t)

	t.Run("a bank-paid join faktura grants once", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true})
		if out := grantInvoiceTx(t, e, inv.ID); !out.Granted {
			t.Fatalf("outcome = %+v", out)
		}
		if out := grantInvoiceTx(t, e, inv.ID); out.Granted || out.Withheld != "" {
			t.Fatalf("second outcome = %+v", out)
		}
		e.assertMember(t, a.ID)
	})

	t.Run("an unpaid faktura does not grant", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{})
		grantInvoiceTx(t, e, inv.ID)
		e.assertApplicant(t, a.ID)
	})

	t.Run("a voided faktura does not grant, even with a payment", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true, Status: "voided"})
		grantInvoiceTx(t, e, inv.ID)
		e.assertApplicant(t, a.ID)
	})

	t.Run("a credited faktura does not grant, even with a payment", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true})
		cn := e.club.NewInvoice(t, fin.InvoiceOpts{Member: &a, Lines: []fin.Line{{Category: "membership", Cents: -joinPriceCents}}})
		exec(t, e.pool, `UPDATE invoices SET credits_invoice_id = $2 WHERE id = $1`, cn.ID, inv.ID)
		grantInvoiceTx(t, e, inv.ID)
		e.assertApplicant(t, a.ID)
	})

	t.Run("withheld: a join line whose total is not its quantity times unit price", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true})
		exec(t, e.pool, `UPDATE invoice_lines SET line_total = 1 WHERE invoice_id = $1`, inv.ID)
		if out := grantInvoiceTx(t, e, inv.ID); out.Withheld != withheldLineMismatch {
			t.Fatalf("outcome = %+v", out)
		}
		e.assertApplicant(t, a.ID)
	})

	t.Run("withheld: a join line offset by a discount line", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true, Lines: []fin.Line{
			{Category: "membership", Cents: joinPriceCents},
			{Category: "other", Cents: -50000},
		}})
		if out := grantInvoiceTx(t, e, inv.ID); out.Withheld != withheldTotalBelowLine {
			t.Fatalf("outcome = %+v", out)
		}
		e.assertApplicant(t, a.ID)
	})

	t.Run("granted: the club closed joining after payment", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true})
		exec(t, e.pool, `UPDATE clubs SET join_price_item_id = NULL WHERE id = $1`, e.club.ID)
		defer exec(t, e.pool, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, e.club.ID, e.itemID)
		if out := grantInvoiceTx(t, e, inv.ID); !out.Granted {
			t.Fatalf("outcome = %+v", out)
		}
		e.assertMember(t, a.ID)
	})
}

func TestSweep(t *testing.T) {
	e := setupJoin(t)
	ctx := context.Background()

	healed := e.club.NewMember(t, "applicant")
	e.joinOrder(t, healed, joinPriceCents, true)
	bank := e.club.NewMember(t, "applicant")
	e.joinInvoice(t, bank, fin.InvoiceOpts{Paid: true})
	unpaid := e.club.NewMember(t, "applicant")
	e.joinOrder(t, unpaid, joinPriceCents, false)
	tampered := e.club.NewMember(t, "applicant")
	order := e.joinOrder(t, tampered, joinPriceCents, true)
	exec(t, e.pool, `UPDATE order_lines SET total_price = 1 WHERE order_id = $1`, order)

	res, err := Sweep(ctx, e.pool)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Granted != 2 || len(res.Withheld) != 1 {
		t.Fatalf("sweep = %+v, want 2 granted and 1 withheld", res)
	}
	e.assertMember(t, healed.ID)
	e.assertMember(t, bank.ID)
	e.assertApplicant(t, unpaid.ID)
	e.assertApplicant(t, tampered.ID)

	var source string
	if err := e.pool.QueryRow(ctx,
		`SELECT details->>'source' FROM audit_log WHERE resource_id = $1 AND action = 'membership.granted'`, healed.ID,
	).Scan(&source); err != nil || source != SourceSweep {
		t.Fatalf("audit source = %q, %v; want sweep", source, err)
	}

	// A second sweep grants nothing new and tells the board about the
	// withheld source no second time.
	res, err = Sweep(ctx, e.pool)
	if err != nil || res.Granted != 0 || len(res.Withheld) != 1 {
		t.Fatalf("second sweep = %+v, %v", res, err)
	}
	e.assertMember(t, healed.ID)
	if n := e.withheldNotices(t, tampered.ID); n != 1 {
		t.Fatalf("withheld notices = %d, want 1 across sweeps", n)
	}
}

// failMemberGrants makes every insert of the member role fail, standing in
// for a grant that breaks inside a settlement. The returned func undoes it.
func failMemberGrants(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	exec(t, pool, `CREATE OR REPLACE FUNCTION test_fail_member_grant() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.role = 'member' THEN RAISE EXCEPTION 'synthetic grant failure'; END IF;
			RETURN NEW;
		END $$`)
	exec(t, pool, `CREATE TRIGGER test_fail_member_grant BEFORE INSERT ON user_roles
		FOR EACH ROW EXECUTE FUNCTION test_fail_member_grant()`)
	return func() { exec(t, pool, `DROP TRIGGER IF EXISTS test_fail_member_grant ON user_roles`) }
}

// A grant that fails inside a settlement is rolled back alone: the rest of
// the settlement commits, and the sweep grants afterwards.
func TestGrantFailureNeverBlocksSettlement(t *testing.T) {
	e := setupJoin(t)
	ctx := context.Background()
	a := e.club.NewMember(t, "applicant")
	order := e.joinOrder(t, a, joinPriceCents, false)

	undo := failMemberGrants(t, e.pool)
	if err := inSavepoint(ctx, e.pool, func(tx pgx.Tx) error {
		// The settlement's own write, then the hook.
		if _, err := tx.Exec(ctx, `UPDATE orders SET status = 'paid', paid_at = now() WHERE id = $1`, order); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount, status)
			 VALUES ($1, $2, 'vipps', $3, $4, 'captured')`,
			e.club.ID, order, "ref-"+testutil.RandomHex(8), fin.Kr(joinPriceCents)); err != nil {
			return err
		}
		if out := GrantForOrderInSavepoint(ctx, tx, order, SourceOnline); out != nil {
			t.Errorf("outcomes = %+v, want none from a failed grant", out)
		}
		return nil
	}); err != nil {
		t.Fatalf("settlement failed with the grant: %v", err)
	}
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status::text FROM orders WHERE id = $1`, order).Scan(&status); err != nil || status != "paid" {
		t.Fatalf("order status = %q, %v; want paid", status, err)
	}
	e.assertApplicant(t, a.ID)

	undo()
	if res, err := Sweep(ctx, e.pool); err != nil || res.Granted != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	e.assertMember(t, a.ID)
}

// A sweep racing the settlement of the same join grants once.
func TestSweepRacingSettlement(t *testing.T) {
	e := setupJoin(t)
	ctx := context.Background()
	for i := range 10 {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = inSavepoint(ctx, e.pool, func(tx pgx.Tx) error {
				GrantForOrderInSavepoint(ctx, tx, order, SourceOnline)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = Sweep(ctx, e.pool)
		}()
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: %v", i, errs)
		}
		e.assertMember(t, a.ID)
	}
}

func TestJoinRefundNotices(t *testing.T) {
	e := setupJoin(t)
	ctx := context.Background()

	count := func(t *testing.T, userID string) (notices, audits int) {
		t.Helper()
		if err := e.pool.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind = 'membership_join_refunded'),
			        (SELECT count(*) FROM audit_log WHERE resource_id = $1::text AND action = 'membership.join_refunded')`,
			userID,
		).Scan(&notices, &audits); err != nil {
			t.Fatal(err)
		}
		return notices, audits
	}

	t.Run("a refunded join order tells the board once and keeps the membership", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		grantOrderTx(t, e, order)
		for range 2 {
			exec(t, e.pool,
				`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, currency, status, reason, provider_refund_id)
				 SELECT club_id, id, provider, 100, 'NOK', 'succeeded', 'test', $2 FROM payment_attempts WHERE order_id = $1`,
				order, "re_"+testutil.RandomHex(6))
		}
		if _, err := QueueJoinRefundNotices(ctx, e.pool, e.club.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := QueueJoinRefundNotices(ctx, e.pool, ""); err != nil {
			t.Fatal(err)
		}
		if n, a2 := count(t, a.ID); n != 1 || a2 != 1 {
			t.Fatalf("notices = %d, audits = %d; want 1 and 1", n, a2)
		}
		e.assertMember(t, a.ID)
	})

	t.Run("a requested refund is not a refund yet", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		order := e.joinOrder(t, a, joinPriceCents, true)
		exec(t, e.pool,
			`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, currency, status, reason)
			 SELECT club_id, id, provider, 100, 'NOK', 'requested', 'test' FROM payment_attempts WHERE order_id = $1`,
			order)
		if _, err := QueueJoinRefundNotices(ctx, e.pool, e.club.ID); err != nil {
			t.Fatal(err)
		}
		if n, _ := count(t, a.ID); n != 0 {
			t.Fatalf("notices = %d, want 0", n)
		}
	})

	t.Run("a credited paid join faktura tells the board", func(t *testing.T) {
		a := e.club.NewMember(t, "applicant")
		inv := e.joinInvoice(t, a, fin.InvoiceOpts{Paid: true})
		grantInvoiceTx(t, e, inv.ID)
		cn := e.club.NewInvoice(t, fin.InvoiceOpts{Member: &a, Lines: []fin.Line{{Category: "membership", Cents: -joinPriceCents}}})
		exec(t, e.pool, `UPDATE invoices SET credits_invoice_id = $2 WHERE id = $1`, cn.ID, inv.ID)
		res, err := Sweep(ctx, e.pool)
		if err != nil || res.RefundNotices < 1 {
			t.Fatalf("sweep = %+v, %v", res, err)
		}
		if n, _ := count(t, a.ID); n != 1 {
			t.Fatalf("notices = %d, want 1", n)
		}
		e.assertMember(t, a.ID)
	})
}
