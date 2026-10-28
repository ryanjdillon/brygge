package stripe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/payments"
)

const testSecretKey = "sk_test_fake"

type recordedCall struct {
	Method, Path, Query, IdempotencyKey, Auth, Version string
	Form                                               url.Values
}

// fakeAPI is a scripted stand-in for the Stripe API: each route answers with
// a fixed status and body, and every call is recorded.
type fakeAPI struct {
	srv *httptest.Server

	mu     sync.Mutex
	calls  []recordedCall
	routes map[string]fakeAnswer
}

type fakeAnswer struct {
	status int
	body   string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{routes: map[string]fakeAnswer{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.calls = append(f.calls, recordedCall{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			IdempotencyKey: r.Header.Get("Idempotency-Key"), Auth: r.Header.Get("Authorization"),
			Version: r.Header.Get("Stripe-Version"),
			Form:    r.PostForm,
		})
		a, ok := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":{"type":"invalid_request_error","message":"no route"}}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = fakeAnswer{status, body}
}

func (f *fakeAPI) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.calls...)
}

func (f *fakeAPI) client() *Client { return New(f.srv.URL, testSecretKey, zerolog.Nop()) }

func TestCreatePayment_SessionShape(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST", "/v1/checkout/sessions", 200, `{"id":"cs_test_1","url":"https://checkout.stripe.example.invalid/c/cs_test_1","status":"open"}`)
	p := NewPaymentProvider(f.client(), "testklubb")
	fixed := time.Unix(1767225600, 0)
	p.now = func() time.Time { return fixed }

	res, err := p.CreatePayment(context.Background(), payments.Request{
		OrderID: "order-1", AttemptID: "attempt-1", AmountOre: 45050, Currency: "NOK",
		Description: "Faktura #12, Gjesteplass", PayerEmail: "member@example.invalid",
		ReturnURL: "https://club.example.invalid/betal/tok/kvittering", CancelURL: "https://club.example.invalid/betal/tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reference != "cs_test_1" || res.RedirectURL != "https://checkout.stripe.example.invalid/c/cs_test_1" {
		t.Errorf("result = %+v", res)
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	if c.IdempotencyKey != "attempt-1" || c.Auth != "Bearer "+testSecretKey || c.Version != APIVersion {
		t.Errorf("idempotency/auth/version = %q/%q/%q", c.IdempotencyKey, c.Auth, c.Version)
	}
	want := map[string]string{
		"mode":                                          "payment",
		"payment_method_types[]":                        "card",
		"line_items[0][quantity]":                       "1",
		"line_items[0][price_data][currency]":           "nok",
		"line_items[0][price_data][unit_amount]":        "45050",
		"line_items[0][price_data][product_data][name]": "Faktura #12, Gjesteplass",
		"customer_email":                                "member@example.invalid",
		"payment_intent_data[capture_method]":           "manual",
		"payment_intent_data[description]":              "Faktura #12, Gjesteplass",
		"metadata[attempt_id]":                          "attempt-1",
		"metadata[order_id]":                            "order-1",
		"metadata[club_slug]":                           "testklubb",
		"payment_intent_data[metadata][attempt_id]":     "attempt-1",
		"payment_intent_data[metadata][order_id]":       "order-1",
		"payment_intent_data[metadata][club_slug]":      "testklubb",
		"success_url":                                   "https://club.example.invalid/betal/tok/kvittering",
		"cancel_url":                                    "https://club.example.invalid/betal/tok",
		"expires_at":                                    "1767229200",
	}
	for k, v := range want {
		if got := c.Form.Get(k); got != v {
			t.Errorf("form %s = %q, want %q", k, got, v)
		}
	}
	if len(c.Form) != len(want) {
		t.Errorf("form has %d fields, want %d: %v", len(c.Form), len(want), c.Form)
	}
}

func TestCreatePayment_GuestHasNoEmail(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST", "/v1/checkout/sessions", 200, `{"id":"cs_test_2","url":"https://checkout.stripe.example.invalid/c/2"}`)
	p := NewPaymentProvider(f.client(), "testklubb")
	if _, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.recorded()[0].Form["customer_email"]; ok {
		t.Error("guest session carries customer_email")
	}
}

func TestCreatePayment_Errors(t *testing.T) {
	f := newFakeAPI(t)
	p := NewPaymentProvider(f.client(), "testklubb")
	if _, err := p.CreatePayment(context.Background(), payments.Request{Currency: "NOK"}); err == nil {
		t.Error("request without attempt id accepted")
	}
	f.on("POST", "/v1/checkout/sessions", 400, `{"error":{"type":"invalid_request_error","code":"parameter_invalid_integer","message":"bad amount"}}`)
	_, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "parameter_invalid_integer" {
		t.Errorf("err = %v, want APIError 400", err)
	}
}

func TestMinAmountOre(t *testing.T) {
	p := NewPaymentProvider(nil, "testklubb")
	tests := []struct {
		currency string
		want     int64
		wantOK   bool
	}{
		{"NOK", 300, true},
		{"nok", 300, true},
		{"SEK", 300, true},
		{"DKK", 250, true},
		{"EUR", 50, true},
		{"USD", 50, true},
		{"GBP", 30, true},
		{"CHF", 50, true},
		{"XYZ", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.currency, func(t *testing.T) {
			got, ok := p.MinAmountOre(tc.currency)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("MinAmountOre(%q) = %d, %v; want %d, %v", tc.currency, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestCreatePayment_BelowMinimum(t *testing.T) {
	const session = `{"id":"cs_test_3","url":"https://checkout.stripe.example.invalid/c/3"}`
	tests := []struct {
		name      string
		currency  string
		amountOre int64
		wantBelow bool
	}{
		{"one øre under", "NOK", 299, true},
		{"at the minimum", "NOK", 300, false},
		{"lower-case currency", "nok", 100, true},
		{"unknown currency is left to Stripe", "XYZ", 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.on("POST", "/v1/checkout/sessions", 200, session)
			p := NewPaymentProvider(f.client(), "testklubb")
			_, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: tc.currency, AmountOre: tc.amountOre})
			if !tc.wantBelow {
				if err != nil || len(f.recorded()) != 1 {
					t.Fatalf("err = %v, calls = %d; want a created session", err, len(f.recorded()))
				}
				return
			}
			var below *payments.BelowMinimumError
			if !errors.Is(err, payments.ErrAmountBelowMinimum) || !errors.As(err, &below) {
				t.Fatalf("err = %v, want BelowMinimumError", err)
			}
			if below.MinOre != 300 || below.Provider != ProviderName || below.Currency != tc.currency {
				t.Errorf("error = %+v", below)
			}
			if n := len(f.recorded()); n != 0 {
				t.Errorf("Stripe was called %d times for an amount under its minimum", n)
			}
		})
	}
}

func TestCreatePayment_StripeAmountTooSmall(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST", "/v1/checkout/sessions", 400,
		`{"error":{"type":"invalid_request_error","code":"amount_too_small","message":"The Checkout Session's total amount due must add up to at least kr3.00 nok."}}`)
	p := NewPaymentProvider(f.client(), "testklubb")
	_, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000})
	var below *payments.BelowMinimumError
	if !errors.Is(err, payments.ErrAmountBelowMinimum) || !errors.As(err, &below) || below.MinOre != 300 {
		t.Fatalf("err = %v, want BelowMinimumError with the NOK minimum", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "amount_too_small" {
		t.Errorf("err = %v, want Stripe's answer kept in the chain", err)
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"kort", 10, "kort"},
		{"Bølgebryter", 2, "Bø"},
		{"æøå", 3, "æøå"},
		{"æøåæ", 3, "æøå"},
		{"x", 0, ""},
	}
	for _, tc := range tests {
		if got := truncateRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestCreatePayment_LongDescriptionRuneSafe(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST", "/v1/checkout/sessions", 200, `{"id":"cs","url":"u"}`)
	p := NewPaymentProvider(f.client(), "testklubb")
	long := ""
	for range 200 {
		long += "ø"
	}
	if _, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000, Description: long}); err != nil {
		t.Fatal(err)
	}
	form := f.recorded()[0].Form
	if got := []rune(form.Get("line_items[0][price_data][product_data][name]")); len(got) != maxProductNameRunes {
		t.Errorf("name runes = %d, want %d", len(got), maxProductNameRunes)
	}
	if form.Get("payment_intent_data[description]") != long {
		t.Error("PaymentIntent description under its limit was shortened")
	}
}

func TestCreatePayment_IntentDescriptionCappedInRunes(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST", "/v1/checkout/sessions", 200, `{"id":"cs","url":"u"}`)
	p := NewPaymentProvider(f.client(), "testklubb")
	long := strings.Repeat("å", 1200)
	if _, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000, Description: long}); err != nil {
		t.Fatal(err)
	}
	got := []rune(f.recorded()[0].Form.Get("payment_intent_data[description]"))
	if len(got) != maxIntentDescriptionRunes {
		t.Errorf("description runes = %d, want %d", len(got), maxIntentDescriptionRunes)
	}
}

func TestCreatePayment_CustomerEmailOnlyWhenBareAddress(t *testing.T) {
	tests := []struct {
		email string
		want  string // "" means omitted
	}{
		{"member@example.invalid", "member@example.invalid"},
		{" member@example.invalid ", "member@example.invalid"},
		{"Test Member <member@example.invalid>", ""},
		{"not-an-address", ""},
		{"member@", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.email, func(t *testing.T) {
			f := newFakeAPI(t)
			f.on("POST", "/v1/checkout/sessions", 200, `{"id":"cs","url":"u"}`)
			p := NewPaymentProvider(f.client(), "testklubb")
			if _, err := p.CreatePayment(context.Background(), payments.Request{AttemptID: "a", Currency: "NOK", AmountOre: 45000, PayerEmail: tc.email}); err != nil {
				t.Fatal(err)
			}
			got, present := f.recorded()[0].Form["customer_email"]
			if tc.want == "" {
				if present {
					t.Errorf("customer_email = %v, want omitted", got)
				}
				return
			}
			if !present || got[0] != tc.want {
				t.Errorf("customer_email = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestCancelPayment(t *testing.T) {
	tests := []struct {
		name      string
		session   string
		wantCalls []string
		wantErr   error
	}{
		{
			name:      "authorized intent is voided",
			session:   `{"id":"cs_1","status":"complete","payment_intent":{"id":"pi_1","status":"requires_capture"}}`,
			wantCalls: []string{"GET /v1/checkout/sessions/cs_1", "POST /v1/payment_intents/pi_1/cancel"},
		},
		{
			name:      "open session is expired",
			session:   `{"id":"cs_1","status":"open","payment_intent":null}`,
			wantCalls: []string{"GET /v1/checkout/sessions/cs_1", "POST /v1/checkout/sessions/cs_1/expire"},
		},
		{
			name:      "expired session needs nothing",
			session:   `{"id":"cs_1","status":"expired"}`,
			wantCalls: []string{"GET /v1/checkout/sessions/cs_1"},
		},
		{
			name:      "cancelled intent needs nothing",
			session:   `{"id":"cs_1","status":"complete","payment_intent":{"id":"pi_1","status":"canceled"}}`,
			wantCalls: []string{"GET /v1/checkout/sessions/cs_1"},
		},
		{
			name:      "captured intent cannot be cancelled",
			session:   `{"id":"cs_1","status":"complete","payment_intent":{"id":"pi_1","status":"succeeded"}}`,
			wantCalls: []string{"GET /v1/checkout/sessions/cs_1"},
			wantErr:   ErrNotCancellable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.on("GET", "/v1/checkout/sessions/cs_1", 200, tc.session)
			f.on("POST", "/v1/payment_intents/pi_1/cancel", 200, `{"id":"pi_1","status":"canceled"}`)
			f.on("POST", "/v1/checkout/sessions/cs_1/expire", 200, `{"id":"cs_1","status":"expired"}`)
			err := NewPaymentProvider(f.client(), "testklubb").CancelPayment(context.Background(), "cs_1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			calls := f.recorded()
			if len(calls) != len(tc.wantCalls) {
				t.Fatalf("calls = %+v, want %v", calls, tc.wantCalls)
			}
			for i, c := range calls {
				if got := c.Method + " " + c.Path; got != tc.wantCalls[i] {
					t.Errorf("call %d = %s, want %s", i, got, tc.wantCalls[i])
				}
			}
			if calls[0].Query != "expand[]=payment_intent" && calls[0].Query != "expand%5B%5D=payment_intent" {
				t.Errorf("session retrieve query = %q, want expand payment_intent", calls[0].Query)
			}
		})
	}
}

func TestRefundNotSupported(t *testing.T) {
	p := NewPaymentProvider(nil, "")
	if _, err := p.Refund(context.Background(), "cs", 1, ""); !errors.Is(err, payments.ErrNotSupported) {
		t.Errorf("err = %v", err)
	}
	if p.Name() != ProviderName {
		t.Errorf("name = %q", p.Name())
	}
}

func TestCapturePaymentIntent(t *testing.T) {
	const unexpected = `{"error":{"type":"invalid_request_error","code":"payment_intent_unexpected_state","message":"already captured"}}`
	tests := []struct {
		name          string
		captureStatus int
		captureBody   string
		piStatus      string
		wantErr       bool
	}{
		{"captured", 200, `{"id":"pi_1","status":"succeeded"}`, "", false},
		{"already captured counts as success", 400, unexpected, "succeeded", false},
		{"unexpected state but cancelled", 400, unexpected, "canceled", true},
		{"other failure", 402, `{"error":{"type":"card_error","code":"card_declined","message":"declined"}}`, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.on("POST", "/v1/payment_intents/pi_1/capture", tc.captureStatus, tc.captureBody)
			f.on("GET", "/v1/payment_intents/pi_1", 200, `{"id":"pi_1","status":"`+tc.piStatus+`"}`)
			err := f.client().CapturePaymentIntent(context.Background(), "pi_1", "capture-a1")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if k := f.recorded()[0].IdempotencyKey; k != "capture-a1" {
				t.Errorf("idempotency key = %q", k)
			}
		})
	}
}

func TestPaymentIntentRefDecodesIDOrObject(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET", "/v1/checkout/sessions/cs_a", 200, `{"id":"cs_a","payment_intent":"pi_a"}`)
	s, err := f.client().GetCheckoutSession(context.Background(), "cs_a")
	if err != nil {
		t.Fatal(err)
	}
	if s.PaymentIntent == nil || s.PaymentIntent.ID != "pi_a" || s.PaymentIntent.Object != nil {
		t.Errorf("payment_intent = %+v", s.PaymentIntent)
	}
}
