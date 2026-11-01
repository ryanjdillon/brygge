package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A crossing settlement whose charges were dismissed never nets on the
// clearing account (its payout holds money the ledger has no debit for), yet
// the reference-year guidance promises the reason that points to the opening
// balance, with or without a fee.
func TestVippsPayoutSweepReferenceSettlementWithDismissedCharges(t *testing.T) {
	for name, feeCents := range map[string]int64{"with a fee": 1000, "without a fee": 0} {
		t.Run(name, func(t *testing.T) {
			f := newPayoutSweepFixture(t)
			ctx := context.Background()
			refYear := f.club.Year - 1
			period := addPeriod(t, f.club, refYear)
			s := f.settlementOn(t, "9", time.Date(refYear, 12, 31, 0, 0, 0, 0, time.UTC), []int64{50000, 30000}, feeCents, true)
			if _, err := f.club.Pool.Exec(ctx,
				`UPDATE vipps_import_rows SET dismissed_at = now() WHERE club_id = $1 AND row_type = 'belastning'`, f.club.ID); err != nil {
				t.Fatalf("dismiss charges: %v", err)
			}
			if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
				t.Fatalf("mark: %v", err)
			}

			res, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			skipped := settlementsOf(res.Skipped)
			if len(res.Cleared) != 0 || len(res.Failed) != 0 || len(res.Waiting) != 0 ||
				len(skipped) != 1 || !strings.Contains(skipped["9"], "referanseår") {
				t.Errorf("sweep = %+v, want only settlement 9 skipped with the reference reason", res)
			}
			if f.bankEntry(t, s.bankRow) != nil {
				t.Errorf("the deposit was booked")
			}
			if n := f.postedEntries(t); n != 0 {
				t.Errorf("the sweep left %d entries behind", n)
			}
		})
	}
}

// A settlement off by one øre is refused, nothing posted.
func TestClearVippsPayoutRefusesSettlementOffByOneOre(t *testing.T) {
	f := newPayoutSweepFixture(t)
	// 100.00 booked, no fee, but Vipps paid out 99.99.
	s := f.feeSettlement(t, "5000601", []int64{10000}, nil, 9999)
	before := f.postedEntries(t)

	_, err := f.clearFee(t, s)
	if !errors.Is(err, ErrVippsSettlementNotNetting) {
		t.Fatalf("clear err = %v, want ErrVippsSettlementNotNetting", err)
	}
	if after := f.postedEntries(t); after != before {
		t.Errorf("a refused clearing left %d new journal entries", after-before)
	}
	if f.bankEntry(t, s.bankRow) != nil {
		t.Errorf("bank row linked after a refused clearing")
	}
}

// fakeBalanceQ answers the two queries checkSettlementNets makes: the club's
// accounts and the settlement's clearing balance.
type fakeBalanceQ struct{ balance float64 }

type fakeRow struct{ vals []any }

func (r fakeRow) Scan(dest ...any) error {
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = r.vals[i].(string)
		case *float64:
			*p = r.vals[i].(float64)
		}
	}
	return nil
}

func (q fakeBalanceQ) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "FROM clubs") {
		return fakeRow{vals: []any{"2900", "7700"}}
	}
	return fakeRow{vals: []any{q.balance}}
}

// The check tolerates less than half an øre (float dust from øre sums) and
// nothing more. Stored amounts have two decimals, so the sub-øre side is
// exercised against the balance directly.
func TestCheckSettlementNetsTolerance(t *testing.T) {
	cases := []struct {
		name        string
		balance     float64
		feeExpense  float64
		bankAmount  float64
		wantRefused bool
	}{
		{"exact", 100, 5, 95, false},
		{"under half an øre", 100.004, 5, 95, false},
		{"under half an øre the other way", 99.996, 5, 95, false},
		{"one øre over", 100.01, 5, 95, true},
		{"one øre under", 99.99, 5, 95, true},
		{"refund nets", 100, -5, 105, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := &VippsPayoutClearResult{SettlementNumber: "1", MSN: "2"}
			err := checkSettlementNets(context.Background(), fakeBalanceQ{balance: c.balance}, "club", res,
				time.Now(), "bank", c.feeExpense, c.bankAmount)
			if refused := errors.Is(err, ErrVippsSettlementNotNetting); refused != c.wantRefused || (err != nil && !refused) {
				t.Errorf("err = %v, want refused = %v", err, c.wantRefused)
			}
		})
	}
}
