package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
)

// pagedRefunds serves n refunds (re_0 … re_n-1, newest first as Stripe
// lists them) in pages of the requested limit, following starting_after;
// the session answers with a PaymentIntent. forever makes every page claim
// another one follows.
func pagedRefunds(t *testing.T, n int, targetKey string, forever bool) (*httptest.Server, *int) {
	t.Helper()
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/refunds" {
			_, _ = w.Write([]byte(sessionWithPI))
			return
		}
		pages++
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		start := 0
		if after := q.Get("starting_after"); after != "" {
			var idx int
			if _, err := fmt.Sscanf(after, "re_%d", &idx); err != nil {
				t.Errorf("starting_after %q", after)
			}
			start = idx + 1
		}
		end := min(start+limit, n)
		var data []map[string]any
		for i := start; i < end; i++ {
			meta := map[string]string{}
			if i == n-1 {
				meta["refund_request_key"] = targetKey
			}
			data = append(data, map[string]any{
				"id": fmt.Sprintf("re_%d", i), "amount": 100, "status": "succeeded",
				"payment_intent": "pi_1", "charge": "ch_1", "metadata": meta,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": forever || end < n})
	}))
	t.Cleanup(srv.Close)
	return srv, &pages
}

// A refund on a later page is found: FindRefund never answers "not found"
// for a refund on a page it did not read.
func TestFindRefundFollowsPages(t *testing.T) {
	srv, pages := pagedRefunds(t, 250, refundKey, false)
	p := NewPaymentProvider(New(srv.URL, testSecretKey, zerolog.Nop()), "testklubb")

	res, found, err := p.FindRefund(context.Background(), "cs_1", refundKey)
	if err != nil || !found || res.ProviderRefundID != "re_249" {
		t.Fatalf("found = %v, %+v, err = %v", found, res, err)
	}
	if *pages != 3 {
		t.Errorf("pages read = %d, want 3", *pages)
	}
	if _, found, err := p.FindRefund(context.Background(), "cs_1", "another-key"); err != nil || found {
		t.Errorf("an unknown key: found = %v, err = %v", found, err)
	}
}

// A list longer than the page cap is an error, not a shorter answer.
func TestFindRefundTruncatedListIsAnError(t *testing.T) {
	srv, pages := pagedRefunds(t, 100000, refundKey, true)
	p := NewPaymentProvider(New(srv.URL, testSecretKey, zerolog.Nop()), "testklubb")

	_, found, err := p.FindRefund(context.Background(), "cs_1", "a-key-on-a-page-never-read")
	if found || err == nil {
		t.Fatalf("found = %v, err = %v, want an error", found, err)
	}
	if *pages != refundListMaxPages {
		t.Errorf("pages read = %d, want the cap %d", *pages, refundListMaxPages)
	}
}
