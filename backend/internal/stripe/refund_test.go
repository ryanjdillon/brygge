package stripe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/payments"
)

const (
	refundKey     = "5b9f6c0e-8d1f-4c55-9f3a-2f6d2a6f0c11"
	sessionWithPI = `{"id":"cs_1","status":"complete","payment_intent":{"id":"pi_1","status":"succeeded"}}`
)

func refundRequest() payments.RefundRequest {
	return payments.RefundRequest{
		Reference: "cs_1", AmountOre: 12300, Currency: "NOK", RequestKey: refundKey,
		OrderID: "order-1", AttemptID: "attempt-1",
	}
}

func TestRefund_RequestShape(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET", "/v1/checkout/sessions/cs_1", 200, sessionWithPI)
	f.on("POST", "/v1/refunds", 200, `{"id":"re_1","amount":12300,"currency":"nok","status":"pending","charge":"ch_1","payment_intent":"pi_1"}`)

	res, err := NewPaymentProvider(f.client(), "testklubb").Refund(context.Background(), refundRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := payments.RefundResult{ProviderRefundID: "re_1", Status: payments.RefundRequested, PaymentRef: "pi_1", ChargeRef: "ch_1"}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	calls := f.recorded()
	if len(calls) != 2 || calls[0].Path != "/v1/checkout/sessions/cs_1" || calls[1].Method != "POST" || calls[1].Path != "/v1/refunds" {
		t.Fatalf("calls = %+v, want the session lookup then one POST /v1/refunds", calls)
	}
	post := calls[1]
	if post.IdempotencyKey != "refund-"+refundKey || post.Auth != "Bearer "+testSecretKey || post.Version != APIVersion {
		t.Errorf("idempotency/auth/version = %q/%q/%q", post.IdempotencyKey, post.Auth, post.Version)
	}
	wantForm := map[string]string{
		"payment_intent":               "pi_1",
		"amount":                       "12300",
		"reason":                       "requested_by_customer",
		"metadata[refund_request_key]": refundKey,
		"metadata[order_id]":           "order-1",
		"metadata[attempt_id]":         "attempt-1",
		"metadata[club_slug]":          "testklubb",
	}
	for k, v := range wantForm {
		if got := post.Form.Get(k); got != v {
			t.Errorf("form %s = %q, want %q", k, got, v)
		}
	}
	if len(post.Form) != len(wantForm) {
		t.Errorf("form has %d fields, want %d: %v", len(post.Form), len(wantForm), post.Form)
	}
}

// A refund Stripe answers with a 4xx did not happen; one whose outcome is
// unknown (5xx, an idempotency refusal, a dropped connection, a body that
// does not decode) is returned as it is. Either way the refund is sent
// once, under one key.
func TestRefund_ErrorsAreClassifiedAndNeverResent(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantRejected bool
		wantCode     string
	}{
		{"disputed", 400, `{"error":{"type":"invalid_request_error","code":"charge_disputed","message":"disputed"}}`, true, "charge_disputed"},
		{"already refunded", 400, `{"error":{"type":"invalid_request_error","code":"charge_already_refunded","message":"done"}}`, true, "charge_already_refunded"},
		{"no permission", 403, `{"error":{"type":"invalid_request_error","message":"restricted key"}}`, true, "invalid_request_error"},
		{"rate limited", 429, `{"error":{"type":"invalid_request_error","code":"rate_limit","message":"slow down"}}`, false, ""},
		{"lock timeout", 429, `{"error":{"type":"invalid_request_error","code":"lock_timeout","message":"busy"}}`, false, ""},
		{"request timeout", 408, `{"error":{"type":"api_error","message":"slow"}}`, false, ""},
		{"proxy 400 with an html body", 400, `<html>Bad Request</html>`, false, ""},
		{"proxy 403 with an empty body", 403, ``, false, ""},
		{"4xx json that is not a stripe error", 400, `{"message":"blocked"}`, false, ""},
		{"server error", 500, `{"error":{"type":"api_error","message":"boom"}}`, false, ""},
		{"idempotency refused", 400, `{"error":{"type":"idempotency_error","message":"other parameters"}}`, false, ""},
		{"key in use", 409, `{"error":{"type":"invalid_request_error","code":"idempotency_key_in_use","message":"in flight"}}`, false, ""},
		{"undecodable success", 200, `not json`, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.on("GET", "/v1/checkout/sessions/cs_1", 200, sessionWithPI)
			f.on("POST", "/v1/refunds", tc.status, tc.body)

			_, err := NewPaymentProvider(f.client(), "testklubb").Refund(context.Background(), refundRequest())
			rej, rejected := payments.IsRefundRejected(err)
			if rejected != tc.wantRejected || (rejected && rej.Code != tc.wantCode) {
				t.Fatalf("err = %v (rejected %v, code %q), want rejected %v code %q", err, rejected, rej, tc.wantRejected, tc.wantCode)
			}
			if err == nil {
				t.Fatal("no error")
			}
			var posts int
			for _, c := range f.recorded() {
				if c.Method == "POST" {
					posts++
					if c.IdempotencyKey != "refund-"+refundKey {
						t.Errorf("POST under key %q", c.IdempotencyKey)
					}
				}
			}
			if posts != 1 {
				t.Errorf("POST /v1/refunds %d times, want 1", posts)
			}
		})
	}
}

func TestRefund_DroppedConnectionIsUncertain(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(sessionWithPI))
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	p := NewPaymentProvider(New(srv.URL, testSecretKey, zerolog.Nop()), "testklubb")
	_, err := p.Refund(context.Background(), refundRequest())
	if _, rejected := payments.IsRefundRejected(err); err == nil || rejected {
		t.Fatalf("err = %v, want an uncertain error", err)
	}
	// net/http itself resends a POST that carries an Idempotency-Key when the
	// connection drops before an answer; that is safe only because the key
	// is the same.
	if len(keys) == 0 {
		t.Fatal("no POST")
	}
	for _, k := range keys {
		if k != "refund-"+refundKey {
			t.Errorf("POST under key %q", k)
		}
	}
}

// A payment Stripe cannot be asked about was never sent a refund.
func TestRefund_LookupFailureSendsNothing(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET", "/v1/checkout/sessions/cs_1", 404, `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"no session"}}`)

	_, err := NewPaymentProvider(f.client(), "testklubb").Refund(context.Background(), refundRequest())
	if rej, ok := payments.IsRefundRejected(err); !ok || rej.Code != "payment_lookup_failed" {
		t.Fatalf("err = %v, want rejected payment_lookup_failed", err)
	}
	for _, c := range f.recorded() {
		if c.Method == "POST" {
			t.Errorf("sent %s %s", c.Method, c.Path)
		}
	}
}

func TestFindRefund(t *testing.T) {
	list := `{"data":[
	  {"id":"re_other","amount":100,"status":"succeeded","payment_intent":"pi_1","charge":"ch_1","metadata":{"refund_request_key":"another"}},
	  {"id":"re_dashboard","amount":200,"status":"succeeded","payment_intent":"pi_1","charge":"ch_1","metadata":{}},
	  {"id":"re_mine","amount":12300,"status":"failed","failure_reason":"expired_or_canceled_card","payment_intent":"pi_1","charge":"ch_1","metadata":{"refund_request_key":"` + refundKey + `"}}
	],"has_more":false}`

	t.Run("found", func(t *testing.T) {
		f := newFakeAPI(t)
		f.on("GET", "/v1/checkout/sessions/cs_1", 200, sessionWithPI)
		f.on("GET", "/v1/refunds", 200, list)

		res, found, err := NewPaymentProvider(f.client(), "testklubb").FindRefund(context.Background(), "cs_1", refundKey)
		if err != nil || !found {
			t.Fatalf("found = %v, err = %v", found, err)
		}
		want := payments.RefundResult{ProviderRefundID: "re_mine", Status: payments.RefundFailed, FailureReason: "expired_or_canceled_card", PaymentRef: "pi_1", ChargeRef: "ch_1"}
		if res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
		q, _ := url.ParseQuery(f.recorded()[1].Query)
		if q.Get("payment_intent") != "pi_1" || q.Get("limit") != "100" {
			t.Errorf("list query = %v", q)
		}
	})

	t.Run("not found", func(t *testing.T) {
		f := newFakeAPI(t)
		f.on("GET", "/v1/checkout/sessions/cs_1", 200, sessionWithPI)
		f.on("GET", "/v1/refunds", 200, list)

		_, found, err := NewPaymentProvider(f.client(), "testklubb").FindRefund(context.Background(), "cs_1", "a-key-never-sent")
		if err != nil || found {
			t.Errorf("found = %v, err = %v, want neither", found, err)
		}
	})

	t.Run("list error", func(t *testing.T) {
		f := newFakeAPI(t)
		f.on("GET", "/v1/checkout/sessions/cs_1", 200, sessionWithPI)
		f.on("GET", "/v1/refunds", 500, `{"error":{"type":"api_error","message":"boom"}}`)

		_, found, err := NewPaymentProvider(f.client(), "testklubb").FindRefund(context.Background(), "cs_1", refundKey)
		var apiErr *APIError
		if found || !errors.As(err, &apiErr) {
			t.Errorf("found = %v, err = %v", found, err)
		}
	})
}

func TestListRefundsByCharge(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET", "/v1/refunds", 200, `{"data":[{"id":"re_1","amount":500,"currency":"nok","status":"pending","charge":{"id":"ch_9"},"payment_intent":"pi_9"}],"has_more":false}`)

	got, err := f.client().ListRefundsByCharge(context.Background(), "ch_9")
	if err != nil || len(got) != 1 || got[0].ID != "re_1" || got[0].Charge != "ch_9" || got[0].PaymentIntent != "pi_9" {
		t.Fatalf("refunds = %+v, err = %v", got, err)
	}
	q, _ := url.ParseQuery(f.recorded()[0].Query)
	if q.Get("charge") != "ch_9" || q.Get("limit") != "100" || q.Get("payment_intent") != "" {
		t.Errorf("list query = %v", q)
	}
}

func TestRefundBryggeStatus(t *testing.T) {
	for stripeStatus, want := range map[string]string{
		RefundPending:        payments.RefundRequested,
		RefundRequiresAction: payments.RefundRequested,
		RefundSucceeded:      payments.RefundSucceeded,
		RefundFailed:         payments.RefundFailed,
		RefundCanceled:       payments.RefundFailed,
		"":                   payments.RefundRequested,
		"something_new":      payments.RefundRequested,
	} {
		if got := (Refund{Status: stripeStatus}).BryggeStatus(); got != want {
			t.Errorf("status %q maps to %q, want %q", stripeStatus, got, want)
		}
	}
}

func TestValidPaymentIntentID(t *testing.T) {
	for id, want := range map[string]bool{
		"pi_3Abc123":      true,
		"pi_":             false,
		"":                false,
		"ch_123":          false,
		"pi_1/../secrets": false,
		"pi_1 2":          false,
	} {
		if got := ValidPaymentIntentID(id); got != want {
			t.Errorf("ValidPaymentIntentID(%q) = %v, want %v", id, got, want)
		}
	}
	if !strings.HasPrefix(DashboardURL("sk_test_x", "/payments/pi_1"), "https://dashboard.stripe.com/test/") {
		t.Error("dashboard link lost its test-mode prefix")
	}
}
