package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/stripe"
)

const (
	stripeTestKey           = "sk_test_fake"
	stripeTestWebhookSecret = "whsec_test_fake"
)

// fakeStripeCreate is one Checkout Session create as the Stripe API got it.
type fakeStripeCreate struct {
	IdempotencyKey string
	Form           url.Values
}

// fakeStripe is an in-process stand-in for the Stripe API, so handler tests
// drive the real stripe.Client over HTTP and assert on what it sent.
type fakeStripe struct {
	srv *httptest.Server

	mu       sync.Mutex
	creates  []fakeStripeCreate
	captures []string // "<pi id> <idempotency key>"
	cancels  []string // PaymentIntent ids
	expires  []string // session ids
	// piStatus is the status of a session's PaymentIntent on retrieve,
	// keyed by session id; requires_capture when unset.
	piStatus map[string]string
	// captureStatus, when non-zero, makes capture fail with that status.
	captureStatus int
	// createError, when set, is the error body a session create answers
	// with status 400.
	createError string
	// intentAfterCapture is the PaymentIntent status a retrieve reports
	// (requires_capture when unset), with the full 450 kr received when
	// it is succeeded.
	intentAfterCapture string
	// latestCharge, when set, is the expanded latest_charge both capture
	// and retrieve answer with.
	latestCharge map[string]any
	// captureBody, when set, is the raw body a successful capture answers
	// with instead of the PaymentIntent.
	captureBody string
	// refuseKey, when set, makes a capture under that Idempotency-Key answer
	// with Stripe's idempotency_error, as a key first used with other
	// parameters does.
	refuseKey string
	// intentMeta is the metadata a retrieve answers with, by PaymentIntent
	// id; intentMissing lists ids a retrieve answers 404 for.
	intentMeta    map[string]map[string]string
	intentMissing map[string]bool
	// intentErrors makes a retrieve answer that HTTP status.
	intentErrors map[string]int
	intentGets   []string
}

func newFakeStripe(t *testing.T) *fakeStripe {
	t.Helper()
	f := &fakeStripe{piStatus: map[string]string{}, intentMeta: map[string]map[string]string{}, intentMissing: map[string]bool{}, intentErrors: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/checkout/sessions", f.handleCreate)
	mux.HandleFunc("GET /v1/checkout/sessions/{id}", f.handleGetSession)
	mux.HandleFunc("POST /v1/checkout/sessions/{id}/expire", f.handleExpire)
	mux.HandleFunc("GET /v1/payment_intents/{id}", f.handleGetIntent)
	mux.HandleFunc("POST /v1/payment_intents/{id}/capture", f.handleCapture)
	mux.HandleFunc("POST /v1/payment_intents/{id}/cancel", f.handleCancel)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStripe) client() *stripe.Client {
	return stripe.New(f.srv.URL, stripeTestKey, zerolog.Nop())
}

// fakeStripePI is the PaymentIntent id the fake gives a session.
func fakeStripePI(sessionID string) string { return "pi_for_" + sessionID }

func (f *fakeStripe) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.creates = append(f.creates, fakeStripeCreate{IdempotencyKey: r.Header.Get("Idempotency-Key"), Form: r.PostForm})
	id := fmt.Sprintf("cs_test_%d", len(f.creates))
	createError := f.createError
	f.mu.Unlock()
	if createError != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(createError))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": id, "status": "open", "url": "https://checkout.stripe.example.invalid/" + id,
	})
}

func (f *fakeStripe) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	status := f.piStatus[id]
	f.mu.Unlock()
	if status == "" {
		status = stripe.PaymentIntentRequiresCapture
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": id, "status": "complete",
		"payment_intent": map[string]any{"id": fakeStripePI(id), "status": status},
	})
}

func (f *fakeStripe) handleGetIntent(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	status := f.intentAfterCapture
	f.intentGets = append(f.intentGets, r.PathValue("id"))
	meta, missing := f.intentMeta[r.PathValue("id")], f.intentMissing[r.PathValue("id")]
	failWith := f.intentErrors[r.PathValue("id")]
	f.mu.Unlock()
	if failWith != 0 {
		w.WriteHeader(failWith)
		_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"synthetic outage"}}`))
		return
	}
	if missing {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"resource_missing","message":"no such payment_intent"}}`))
		return
	}
	if status == "" {
		status = stripe.PaymentIntentRequiresCapture
	}
	received := 0
	if status == stripe.PaymentIntentSucceeded {
		received = 45000
	}
	f.mu.Lock()
	charge := f.latestCharge
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": r.PathValue("id"), "status": status, "amount": 45000, "amount_received": received, "currency": "nok",
		"latest_charge": charge, "metadata": meta,
	})
}

func (f *fakeStripe) handleExpire(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.expires = append(f.expires, r.PathValue("id"))
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"status":"expired"}`))
}

func (f *fakeStripe) handleCapture(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.captures = append(f.captures, r.PathValue("id")+" "+r.Header.Get("Idempotency-Key"))
	status, charge, body := f.captureStatus, f.latestCharge, f.captureBody
	refused := f.refuseKey != "" && r.Header.Get("Idempotency-Key") == f.refuseKey
	f.mu.Unlock()
	if refused {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"idempotency_error","message":"Keys for idempotent requests can only be used with the same parameters they were first used with."}}`))
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"fake failure"}}`))
		return
	}
	if body != "" {
		_, _ = w.Write([]byte(body))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": r.PathValue("id"), "status": "succeeded", "latest_charge": charge,
	})
}

func (f *fakeStripe) handleCancel(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.cancels = append(f.cancels, r.PathValue("id"))
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"status":"canceled"}`))
}

func (f *fakeStripe) createCalls() []fakeStripeCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeStripeCreate(nil), f.creates...)
}

// intentGetCalls are the PaymentIntent ids retrieved so far.
func (f *fakeStripe) intentGetCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.intentGets...)
}

func (f *fakeStripe) captureCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.captures...)
}

func (f *fakeStripe) cancelCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}
