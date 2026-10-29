package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

// The refund half of the fake Stripe API: POST /v1/refunds, which answers a
// repeated Idempotency-Key with the first refund as Stripe does, and GET
// /v1/refunds, filtered by charge or PaymentIntent.

func (f *fakeStripe) handleCreateRefund(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	f.refundPosts = append(f.refundPosts, fakeStripeCreate{IdempotencyKey: key, Form: r.PostForm})
	failStatus, failBody, lost := f.refundFailStatus, f.refundFailBody, f.refundLost
	if first, ok := f.refundByKey[key]; ok && key != "" {
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(first)
		return
	}
	if failStatus != 0 {
		f.mu.Unlock()
		w.WriteHeader(failStatus)
		_, _ = w.Write([]byte(failBody))
		return
	}
	status := f.refundStatus
	if status == "" {
		status = stripe.RefundSucceeded
	}
	amount, _ := strconv.ParseInt(r.PostForm.Get("amount"), 10, 64)
	refund := map[string]any{
		"id": fmt.Sprintf("re_fake_%d", len(f.refunds)+1), "object": "refund", "amount": amount, "currency": "nok",
		"status": status, "payment_intent": r.PostForm.Get("payment_intent"), "charge": "ch_" + r.PostForm.Get("payment_intent"),
		"metadata": map[string]string{
			"refund_request_key": r.PostForm.Get("metadata[refund_request_key]"),
			"order_id":           r.PostForm.Get("metadata[order_id]"),
			"attempt_id":         r.PostForm.Get("metadata[attempt_id]"),
		},
	}
	f.refunds = append(f.refunds, refund)
	if key != "" {
		f.refundByKey[key] = refund
	}
	f.mu.Unlock()
	if lost {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"lost after acting"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(refund)
}

func (f *fakeStripe) handleListRefunds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.refundLists = append(f.refundLists, r.URL.RawQuery)
	status := f.refundListStatus
	var data []map[string]any
	for _, rf := range f.refunds {
		if (q.Get("charge") != "" && rf["charge"] == q.Get("charge")) ||
			(q.Get("payment_intent") != "" && rf["payment_intent"] == q.Get("payment_intent")) {
			data = append(data, rf)
		}
	}
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"synthetic outage"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}

// addRefund puts a refund into the fake, as one made in the Dashboard (no
// metadata of Brygge's) or by the button (with the request key).
func (f *fakeStripe) addRefund(id, paymentIntent, charge, status string, amountOre int64, requestKey string) {
	meta := map[string]string{}
	if requestKey != "" {
		meta["refund_request_key"] = requestKey
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, map[string]any{
		"id": id, "object": "refund", "amount": amountOre, "currency": "nok", "status": status,
		"payment_intent": paymentIntent, "charge": charge, "metadata": meta,
	})
}

func (f *fakeStripe) setRefundStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rf := range f.refunds {
		if rf["id"] == id {
			rf["status"] = status
		}
	}
}

func (f *fakeStripe) refundCalls() []fakeStripeCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeStripeCreate(nil), f.refundPosts...)
}
