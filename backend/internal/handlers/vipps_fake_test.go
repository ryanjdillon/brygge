package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// fakeVippsCreate is one CreatePayment call as the Vipps API received it.
type fakeVippsCreate struct {
	IdempotencyKey string
	Body           vipps.CreatePaymentRequest
}

// fakeVippsCapture is one CapturePayment call as the Vipps API received it.
type fakeVippsCapture struct {
	IdempotencyKey string
	Reference      string
	Body           vipps.CaptureRequest
}

// fakeVipps is an in-process stand-in for the Vipps ePayment API, so handler
// tests drive the real vipps.Client over HTTP and assert on what it sent.
type fakeVipps struct {
	srv *httptest.Server

	mu       sync.Mutex
	creates  []fakeVippsCreate
	captures []fakeVippsCapture
	gets     []string
	cancels  []string

	// createStatus, when non-zero, makes CreatePayment fail with that status.
	createStatus int
	// captureStatus, when non-zero, makes CapturePayment fail with that status.
	captureStatus int
	// onCreate, when set, runs as CreatePayment arrives, before it answers.
	onCreate func()
	// payerName is returned as the consented profile name on GET payment.
	payerName string
}

func newFakeVipps(t *testing.T) *fakeVipps {
	t.Helper()
	f := &fakeVipps{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accesstoken/get", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"fake-token","expires_in":"3600"}`))
	})
	mux.HandleFunc("POST /epayment/v1/payments", f.handleCreate)
	mux.HandleFunc("GET /epayment/v1/payments/{ref}", f.handleGet)
	mux.HandleFunc("POST /epayment/v1/payments/{ref}/capture", f.handleCapture)
	mux.HandleFunc("POST /epayment/v1/payments/{ref}/cancel", f.handleCancel)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a vipps.Client pointed at the fake.
func (f *fakeVipps) client() *vipps.Client {
	return vipps.New(f.srv.URL, "test-client", "test-secret", "test-sub", "123456", zerolog.Nop())
}

func (f *fakeVipps) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body vipps.CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.creates = append(f.creates, fakeVippsCreate{IdempotencyKey: r.Header.Get("Idempotency-Key"), Body: body})
	status := f.createStatus
	hook := f.onCreate
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if status != 0 {
		http.Error(w, `{"title":"fake failure"}`, status)
		return
	}
	_ = json.NewEncoder(w).Encode(vipps.PaymentResponse{
		Reference:   body.Reference,
		RedirectURL: "https://vipps.example.invalid/checkout/" + body.Reference,
	})
}

func (f *fakeVipps) handleGet(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	f.mu.Lock()
	f.gets = append(f.gets, ref)
	name := f.payerName
	f.mu.Unlock()
	out := vipps.PaymentDetails{Reference: ref, State: "CAPTURED"}
	if name != "" {
		out.UserDetails = &vipps.UserDetails{Name: name}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeVipps) handleCapture(w http.ResponseWriter, r *http.Request) {
	var body vipps.CaptureRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.captures = append(f.captures, fakeVippsCapture{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Reference:      r.PathValue("ref"),
		Body:           body,
	})
	status := f.captureStatus
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, `{"title":"fake failure"}`, status)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (f *fakeVipps) handleCancel(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.cancels = append(f.cancels, r.PathValue("ref"))
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeVipps) createCalls() []fakeVippsCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVippsCreate(nil), f.creates...)
}

func (f *fakeVipps) captureCalls() []fakeVippsCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVippsCapture(nil), f.captures...)
}

func (f *fakeVipps) cancelCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}

func (f *fakeVipps) getCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.gets...)
}
