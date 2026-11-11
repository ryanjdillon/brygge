package accounting

import (
	"strconv"
	"testing"
)

func TestCapListTruncates(t *testing.T) {
	cases := []struct {
		name      string
		n         int
		wantLen   int
		truncated int
	}{
		{"nil", -1, 0, 0},
		{"under", 3, 3, 0},
		{"at cap", autoBookingAuditListCap, autoBookingAuditListCap, 0},
		{"over cap", autoBookingAuditListCap + 7, autoBookingAuditListCap, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var list []string
			for i := 0; i < tc.n; i++ {
				list = append(list, strconv.Itoa(i))
			}
			m := map[string]any{}
			capList(m, "items", list)
			got, ok := m["items"].([]string)
			if !ok || len(got) != tc.wantLen {
				t.Fatalf("items = %#v, want %d entries", m["items"], tc.wantLen)
			}
			tr, has := m["items_truncated"]
			if tc.truncated == 0 && has {
				t.Errorf("items_truncated = %v, want absent", tr)
			}
			if tc.truncated > 0 && tr != tc.truncated {
				t.Errorf("items_truncated = %v, want %d", tr, tc.truncated)
			}
		})
	}
}

func TestAutoBookingResultClassification(t *testing.T) {
	item := []VippsPayoutSweepItem{{SettlementNumber: "1"}}
	cases := []struct {
		name                        string
		res                         AutoBookingResult
		changed, failed, needsAudit bool
	}{
		{"nothing", AutoBookingResult{}, false, false, false},
		{"kid matched", AutoBookingResult{KIDMatched: 1}, true, false, true},
		{"transfer draft", AutoBookingResult{TransfersLinked: 1}, true, false, true},
		{"bank auto-assigned", AutoBookingResult{BankAutoAssign: &AutoAssignResult{Assigned: []AutoAssignItem{{}}}}, true, false, true},
		{"order booked", AutoBookingResult{Vipps: &VippsBookingResult{Orders: &VippsOrderReconcileResult{Booked: 1}}}, true, false, true},
		{"charge auto-assigned", AutoBookingResult{Vipps: &VippsBookingResult{AutoAssign: &AutoAssignResult{Assigned: []AutoAssignItem{{}}}}}, true, false, true},
		{"payout cleared", AutoBookingResult{Vipps: &VippsBookingResult{Payouts: &VippsPayoutSweepResult{Cleared: item}}}, true, false, true},
		{"repair only", AutoBookingResult{Vipps: &VippsBookingResult{Orders: &VippsOrderReconcileResult{Repaired: 3}}}, false, false, false},
		{"back-link healed", AutoBookingResult{PaymentsLinked: 1}, false, false, true},
		{"step error", AutoBookingResult{Errors: []string{"x"}}, false, true, true},
		{"aborted", AutoBookingResult{Aborted: true}, false, true, true},
		{"busy", AutoBookingResult{Busy: true}, false, false, true},
		{"ingest failed", AutoBookingResult{IngestFailed: true}, false, false, true},
		{"sweep error", AutoBookingResult{Vipps: &VippsBookingResult{Payouts: &VippsPayoutSweepResult{Error: "x"}}}, false, true, true},
		{"bank auto-assign error", AutoBookingResult{BankAutoAssign: &AutoAssignResult{Error: "x"}}, false, true, true},
		{"payout failed", AutoBookingResult{Vipps: &VippsBookingResult{Payouts: &VippsPayoutSweepResult{Failed: item}}}, false, false, true},
		{"not netting", AutoBookingResult{Vipps: &VippsBookingResult{Payouts: &VippsPayoutSweepResult{Skipped: item, notNetting: item}}}, false, false, true},
		{"other skip", AutoBookingResult{Vipps: &VippsBookingResult{Payouts: &VippsPayoutSweepResult{Skipped: item}}}, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.res.Changed(); got != tc.changed {
				t.Errorf("Changed = %v, want %v", got, tc.changed)
			}
			if got := tc.res.Failed(); got != tc.failed {
				t.Errorf("Failed = %v, want %v", got, tc.failed)
			}
			if got := tc.res.NeedsAudit(); got != tc.needsAudit {
				t.Errorf("NeedsAudit = %v, want %v", got, tc.needsAudit)
			}
		})
	}
}
