package stripe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// balanceAPI is a fake Stripe API whose answer depends on the path and
// query of each request; every query is recorded.
type balanceAPI struct {
	srv *httptest.Server

	mu      sync.Mutex
	queries []url.Values
}

func newBalanceAPI(t *testing.T, answer func(path string, q url.Values) (int, string)) *balanceAPI {
	t.Helper()
	b := &balanceAPI{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testSecretKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b.mu.Lock()
		b.queries = append(b.queries, r.URL.Query())
		b.mu.Unlock()
		status, body := answer(r.URL.Path, r.URL.Query())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *balanceAPI) client() *Client { return New(b.srv.URL, testSecretKey, zerolog.Nop()) }

const chargeTxnPage1 = `{
  "object": "list", "has_more": true,
  "data": [{
    "id": "txn_test_1", "object": "balance_transaction", "amount": 50000, "fee": 1525, "net": 48475,
    "currency": "nok", "created": 1790000000, "available_on": 1790208000,
    "type": "charge", "reporting_category": "charge",
    "fee_details": [{"amount": 1525, "application": null, "currency": "nok", "description": "Stripe processing fees", "type": "stripe_fee"}],
    "source": {
      "id": "ch_test_1", "object": "charge", "amount": 50000, "currency": "nok",
      "payment_intent": "pi_test_1",
      "metadata": {"attempt_id": "00000000-0000-0000-0000-000000000001", "club_slug": "synthetic"},
      "billing_details": {"name": "Test Payer", "email": "payer@example.invalid"}
    }
  }]
}`

const chargeTxnPage2 = `{
  "object": "list", "has_more": false,
  "data": [{
    "id": "txn_test_2", "object": "balance_transaction", "amount": 150000, "fee": 0, "net": 150000,
    "currency": "nok", "created": 1790000100, "available_on": 1790208000,
    "type": "payout", "reporting_category": "payout",
    "source": {
      "id": "po_test_1", "object": "payout", "amount": 150000, "currency": "nok",
      "arrival_date": 1790208000, "status": "paid", "automatic": true,
      "statement_descriptor": null
    }
  }]
}`

func TestListBalanceTransactionsPaginatesWithExpand(t *testing.T) {
	api := newBalanceAPI(t, func(path string, q url.Values) (int, string) {
		if path != "/v1/balance_transactions" {
			return http.StatusNotFound, `{"error":{"type":"invalid_request_error"}}`
		}
		if q.Get("starting_after") == "txn_test_1" {
			return http.StatusOK, chargeTxnPage2
		}
		return http.StatusOK, chargeTxnPage1
	})
	c := api.client()
	since := time.Unix(1789000000, 0)

	page, err := c.ListBalanceTransactions(context.Background(), since, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !page.HasMore || len(page.Data) != 1 {
		t.Fatalf("page 1 = %+v, want one row and more", page)
	}
	bt := page.Data[0]
	if bt.ID != "txn_test_1" || bt.Amount != 50000 || bt.Fee != 1525 || bt.Net != 48475 ||
		bt.ReportingCategory != CategoryCharge || bt.Currency != "nok" || bt.AvailableOn != 1790208000 {
		t.Errorf("transaction = %+v", bt)
	}
	if len(bt.FeeDetails) != 1 || bt.FeeDetails[0] != (FeeDetail{Amount: 1525, Currency: "nok", Description: "Stripe processing fees", Type: "stripe_fee"}) {
		t.Errorf("fee details = %+v", bt.FeeDetails)
	}
	if bt.Source == nil || bt.Source.ID != "ch_test_1" || bt.Source.Object != "charge" ||
		bt.Source.PaymentIntent != "pi_test_1" || bt.Source.Metadata["attempt_id"] == "" {
		t.Errorf("source = %+v", bt.Source)
	}

	page, err = c.ListBalanceTransactions(context.Background(), since, "txn_test_1")
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if page.HasMore || len(page.Data) != 1 || page.Data[0].Source.ID != "po_test_1" ||
		!page.Data[0].Source.Automatic || page.Data[0].Source.Status != PayoutPaid {
		t.Errorf("page 2 = %+v", page)
	}

	if len(api.queries) != 2 {
		t.Fatalf("calls = %d, want 2", len(api.queries))
	}
	q := api.queries[0]
	if q.Get("created[gte]") != "1789000000" || q.Get("limit") != "50" || q.Get("expand[]") != "data.source" ||
		q.Has("starting_after") || q.Has("payout") {
		t.Errorf("first query = %v", q)
	}
	if api.queries[1].Get("starting_after") != "txn_test_1" {
		t.Errorf("second query = %v", api.queries[1])
	}
}

func TestListPayoutTransactionsFiltersByPayout(t *testing.T) {
	api := newBalanceAPI(t, func(path string, q url.Values) (int, string) {
		if path == "/v1/balance_transactions" && q.Get("payout") == "po_test_1" {
			return http.StatusOK, chargeTxnPage2
		}
		return http.StatusNotFound, `{"error":{"type":"invalid_request_error"}}`
	})
	page, err := api.client().ListPayoutTransactions(context.Background(), "po_test_1", "txn_test_9")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "txn_test_2" {
		t.Errorf("page = %+v", page)
	}
	q := api.queries[0]
	if q.Has("created[gte]") || q.Get("starting_after") != "txn_test_9" || q.Get("expand[]") != "data.source" {
		t.Errorf("query = %v", q)
	}
}

func TestGetPayout(t *testing.T) {
	api := newBalanceAPI(t, func(path string, _ url.Values) (int, string) {
		if path != "/v1/payouts/po_test_1" {
			return http.StatusNotFound, `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such payout"}}`
		}
		return http.StatusOK, `{"id":"po_test_1","object":"payout","amount":150000,"currency":"nok",
			"arrival_date":1790208000,"status":"in_transit","automatic":false,"statement_descriptor":"SYNTHETIC CLUB"}`
	})
	p, err := api.client().GetPayout(context.Background(), "po_test_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := Payout{ID: "po_test_1", Amount: 150000, Currency: "nok", ArrivalDate: 1790208000,
		Status: PayoutInTransit, Automatic: false, StatementDescriptor: "SYNTHETIC CLUB"}
	if *p != want {
		t.Errorf("payout = %+v, want %+v", *p, want)
	}

	_, err = api.client().GetPayout(context.Background(), "po_test_missing")
	if err == nil || !strings.Contains(err.Error(), "resource_missing") {
		t.Errorf("missing payout error = %v", err)
	}
}

func TestBalanceSourceAcceptsBareID(t *testing.T) {
	api := newBalanceAPI(t, func(string, url.Values) (int, string) {
		return http.StatusOK, `{"has_more":false,"data":[{"id":"txn_test_3","reporting_category":"fee",
			"amount":-500,"net":-500,"currency":"nok","source":"ab_test_1"},
			{"id":"txn_test_4","reporting_category":"refund","amount":-100,"net":-100,"currency":"nok",
			"source":{"id":"re_test_1","object":"refund","charge":{"id":"ch_test_1"},"payment_intent":null}},
			{"id":"txn_test_5","reporting_category":"fee","amount":-1,"net":-1,"currency":"nok","source":null}]}`
	})
	page, err := api.client().ListBalanceTransactions(context.Background(), time.Unix(0, 0), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if s := page.Data[0].Source; s == nil || s.ID != "ab_test_1" || s.Object != "" {
		t.Errorf("bare-id source = %+v", s)
	}
	if s := page.Data[1].Source; s == nil || s.Charge != "ch_test_1" || s.PaymentIntent != "" {
		t.Errorf("refund source = %+v", s)
	}
	if page.Data[2].Source != nil {
		t.Errorf("null source = %+v", page.Data[2].Source)
	}
}
