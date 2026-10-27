package vipps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// recordingServer answers the ePayment calls the provider makes and keeps
// what it received.
type recordingServer struct {
	mu        sync.Mutex
	creates   []vipps.CreatePaymentRequest
	keys      []string
	cancelled []string
}

func newRecordingServer(t *testing.T) (*recordingServer, *httptest.Server) {
	t.Helper()
	rs := &recordingServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accesstoken/get", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3600"}`))
	})
	mux.HandleFunc("POST /epayment/v1/payments", func(w http.ResponseWriter, r *http.Request) {
		var req vipps.CreatePaymentRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		rs.mu.Lock()
		rs.creates = append(rs.creates, req)
		rs.keys = append(rs.keys, r.Header.Get("Idempotency-Key"))
		rs.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"reference":   req.Reference,
			"redirectUrl": "https://vipps.example.invalid/pay/" + req.Reference,
		})
	})
	mux.HandleFunc("POST /epayment/v1/payments/{ref}/cancel", func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.cancelled = append(rs.cancelled, r.PathValue("ref"))
		rs.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return rs, srv
}

func TestPaymentProvider_CreatePaymentMapping(t *testing.T) {
	refPattern := regexp.MustCompile(`^chk-[0-9a-f]{32}$`)
	long := strings.Repeat("a", 120)

	tests := []struct {
		name        string
		req         payments.Request
		wantDesc    string
		wantProfile *vipps.Profile
	}{
		{
			name:     "member",
			req:      payments.Request{OrderID: "o1", AmountOre: 45000, Currency: "NOK", Description: "Faktura #1", ReturnURL: "https://club.example.invalid"},
			wantDesc: "Faktura #1",
		},
		{
			name:        "guest asks for name",
			req:         payments.Request{OrderID: "o2", AmountOre: 100, Currency: "NOK", Description: "Gjesteplass", IsGuest: true, ReturnURL: "https://club.example.invalid"},
			wantDesc:    "Gjesteplass",
			wantProfile: &vipps.Profile{Scope: "name"},
		},
		{
			name:     "description cut to 100",
			req:      payments.Request{OrderID: "o3", AmountOre: 1, Currency: "NOK", Description: long, ReturnURL: "https://club.example.invalid"},
			wantDesc: long[:100],
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rs, srv := newRecordingServer(t)
			p := vipps.NewPaymentProvider(newTestClient(srv.URL))

			res, err := p.CreatePayment(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("CreatePayment: %v", err)
			}
			if len(rs.creates) != 1 {
				t.Fatalf("create calls = %d, want 1", len(rs.creates))
			}
			got := rs.creates[0]
			if !refPattern.MatchString(res.Reference) || got.Reference != res.Reference || rs.keys[0] != res.Reference {
				t.Errorf("reference %q, sent %q, idempotency key %q", res.Reference, got.Reference, rs.keys[0])
			}
			if res.RedirectURL != "https://vipps.example.invalid/pay/"+res.Reference {
				t.Errorf("redirect = %q", res.RedirectURL)
			}
			if got.Amount != (vipps.Amount{Currency: "NOK", Value: tc.req.AmountOre}) {
				t.Errorf("amount = %+v", got.Amount)
			}
			if got.ReturnURL != tc.req.ReturnURL || got.PaymentDescription != tc.wantDesc || got.UserFlow != "WEB_REDIRECT" {
				t.Errorf("return/desc/flow = %q/%q/%q", got.ReturnURL, got.PaymentDescription, got.UserFlow)
			}
			if (got.Profile == nil) != (tc.wantProfile == nil) || (got.Profile != nil && *got.Profile != *tc.wantProfile) {
				t.Errorf("profile = %+v, want %+v", got.Profile, tc.wantProfile)
			}
		})
	}
}

func TestPaymentProvider_FreshReferencePerAttempt(t *testing.T) {
	_, srv := newRecordingServer(t)
	p := vipps.NewPaymentProvider(newTestClient(srv.URL))
	req := payments.Request{AmountOre: 100, Currency: "NOK", ReturnURL: "https://club.example.invalid"}
	a, errA := p.CreatePayment(context.Background(), req)
	b, errB := p.CreatePayment(context.Background(), req)
	if errA != nil || errB != nil || a.Reference == b.Reference {
		t.Errorf("references %q and %q (errs %v, %v), want two distinct", a.Reference, b.Reference, errA, errB)
	}
}

func TestPaymentProvider_RefundAndCancel(t *testing.T) {
	rs, srv := newRecordingServer(t)
	p := vipps.NewPaymentProvider(newTestClient(srv.URL))
	if p.Name() != vipps.ProviderName {
		t.Errorf("Name() = %q", p.Name())
	}
	if _, err := p.Refund(context.Background(), "chk-x", 100, "test"); !errors.Is(err, payments.ErrNotSupported) {
		t.Errorf("Refund err = %v, want ErrNotSupported", err)
	}
	if err := p.CancelPayment(context.Background(), "chk-x"); err != nil {
		t.Fatalf("CancelPayment: %v", err)
	}
	if len(rs.cancelled) != 1 || rs.cancelled[0] != "chk-x" {
		t.Errorf("cancelled = %v", rs.cancelled)
	}
}
