package accounting

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The split guard must refuse everything postJournalEntryTx would refuse:
// journal lines are NUMERIC(12,2), so any whole øre of difference unbalances
// the entry. Float representation noise must still pass.
func TestCheckOrderChargeSplitToleranceMatchesPost(t *testing.T) {
	line := func(credit float64) VippsReconcileLine {
		return VippsReconcileLine{AccountCode: "3300", Credit: credit}
	}
	tenths := make([]VippsReconcileLine, 10)
	for i := range tenths {
		tenths[i] = line(0.1)
	}

	tests := []struct {
		name   string
		split  []VippsReconcileLine
		amount float64
		ok     bool
	}{
		{"exact", []VippsReconcileLine{line(250)}, 250, true},
		{"float noise 0.1+0.2", []VippsReconcileLine{line(0.1), line(0.2)}, 0.3, true},
		{"ten tenths", tenths, 1, true},
		{"noise on a large total", []VippsReconcileLine{line(19999.99), line(0.01)}, 20000, true},
		// 0.01 apart: float noise decided whether the old 0.01 guard held.
		{"one øre over, noise below 0.01", []VippsReconcileLine{line(250)}, 250.01, false},
		{"one øre under, noise below 0.01", []VippsReconcileLine{line(250.01)}, 250, false},
		{"one øre over, noise above 0.01", []VippsReconcileLine{line(100)}, 100.01, false},
		{"one øre short on a sum", []VippsReconcileLine{line(450), line(2550)}, 2999.99, false},
		{"several øre", []VippsReconcileLine{line(250)}, 250.05, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// No fakturas on the split, so the guard never touches the database.
			_, _, err := checkOrderChargeSplit(context.Background(), nil, "club", tc.split, tc.amount, time.Now())
			var mismatch *orderSplitMismatchError
			switch {
			case tc.ok && err != nil:
				t.Errorf("err = %v, want the split accepted", err)
			case !tc.ok && !errors.As(err, &mismatch):
				t.Errorf("err = %v, want *orderSplitMismatchError", err)
			}
		})
	}
}
