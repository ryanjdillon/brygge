package stripe

import (
	"encoding/json"
	"testing"
)

func TestDisputeDecodesTolerantly(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Dispute
	}{
		{
			"full object",
			`{"id":"dp_1","object":"dispute","amount":45000,"currency":"nok","reason":"fraudulent","status":"needs_response",
			  "charge":"ch_1","payment_intent":"pi_1","evidence_details":{"due_by":1790000000,"has_evidence":false},"is_charge_refundable":false}`,
			Dispute{ID: "dp_1", PaymentIntent: "pi_1", Charge: "ch_1", Amount: i64(45000), Currency: "nok",
				Reason: "fraudulent", Status: "needs_response", EvidenceDueBy: 1790000000, ChargeRefundable: bptr(false)},
		},
		{
			"expanded references",
			`{"id":"dp_2","charge":{"id":"ch_2","object":"charge"},"payment_intent":{"id":"pi_2","object":"payment_intent"}}`,
			Dispute{ID: "dp_2", PaymentIntent: "pi_2", Charge: "ch_2"},
		},
		{
			"fields of the wrong type are dropped, the rest kept",
			`{"id":"dp_3","amount":"lots","currency":7,"reason":{"x":1},"status":"under_review","evidence_details":"none","payment_intent":12,"is_charge_refundable":"yes"}`,
			Dispute{ID: "dp_3", Status: "under_review"},
		},
		{"null references", `{"id":"dp_4","charge":null,"payment_intent":null,"evidence_details":null}`, Dispute{ID: "dp_4"}},
		{"not an object", `"dp_5"`, Dispute{}},
		{"an array", `[1,2]`, Dispute{}},
		{"empty", `{}`, Dispute{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Dispute
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("decoding failed: %v", err)
			}
			if got.ID != tt.want.ID || got.PaymentIntent != tt.want.PaymentIntent || got.Charge != tt.want.Charge ||
				got.Currency != tt.want.Currency || got.Reason != tt.want.Reason || got.Status != tt.want.Status ||
				got.EvidenceDueBy != tt.want.EvidenceDueBy || (got.ChargeRefundable == nil) != (tt.want.ChargeRefundable == nil) ||
				(got.ChargeRefundable != nil && *got.ChargeRefundable != *tt.want.ChargeRefundable) || (got.Amount == nil) != (tt.want.Amount == nil) ||
				(got.Amount != nil && *got.Amount != *tt.want.Amount) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func i64(v int64) *int64 { return &v }

func bptr(v bool) *bool { return &v }

func TestIsDisputeEvent(t *testing.T) {
	for _, e := range []string{EventChargeDisputeCreated, EventChargeDisputeUpdated, EventChargeDisputeClosed,
		EventChargeDisputeFundsWithdrawn, EventChargeDisputeFundsReinstated} {
		if !IsDisputeEvent(e) {
			t.Errorf("%s is not a dispute event", e)
		}
	}
	for _, e := range []string{EventPayoutPaid, EventPaymentIntentSucceeded, "charge.refunded", "charge.dispute", ""} {
		if IsDisputeEvent(e) {
			t.Errorf("%s is a dispute event", e)
		}
	}
}

func TestDashboardURL(t *testing.T) {
	tests := []struct{ key, path, want string }{
		{"sk_test_abc", "/disputes/dp_1", "https://dashboard.stripe.com/test/disputes/dp_1"},
		{"rk_test_abc", "disputes/dp_1", "https://dashboard.stripe.com/test/disputes/dp_1"},
		{"sk_live_abc", "/disputes/dp_1", "https://dashboard.stripe.com/disputes/dp_1"},
		{"rk_live_abc", "//payments/pi_1", "https://dashboard.stripe.com/payments/pi_1"},
		{"", "/disputes/dp_1", "https://dashboard.stripe.com/disputes/dp_1"},
	}
	for _, tt := range tests {
		if got := DashboardURL(tt.key, tt.path); got != tt.want {
			t.Errorf("DashboardURL(%q, %q) = %q, want %q", tt.key, tt.path, got, tt.want)
		}
	}
}

func TestValidDisputeID(t *testing.T) {
	for id, want := range map[string]bool{
		"dp_1Abc": true, "du_1Abc": true, "dp_": false, "": false, "pi_1": false, "dp_1/../x": false, "dp_1 2": false, "dp_1?a=b": false,
	} {
		if got := ValidDisputeID(id); got != want {
			t.Errorf("ValidDisputeID(%q) = %v, want %v", id, got, want)
		}
	}
}
