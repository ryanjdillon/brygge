package vipps_test

import (
	"context"
	"strings"
	"testing"
)

func TestListLedgers(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	raw, err := c.ListLedgers(context.Background())
	if err != nil {
		t.Fatalf("ListLedgers: %v", err)
	}
	// MSN echoed from the Merchant-Serial-Number header the client sets.
	if !strings.Contains(string(raw), `"ledgerId":"123456"`) || !strings.Contains(string(raw), "123456") {
		t.Fatalf("unexpected ledgers response: %s", raw)
	}
}

func TestLedgerFundsAndFees(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	funds, err := c.LedgerFunds(context.Background(), "123456", "2026-08-15")
	if err != nil {
		t.Fatalf("LedgerFunds: %v", err)
	}
	if !strings.Contains(string(funds), `"reference":"inv-abc"`) {
		t.Errorf("funds missing expected item: %s", funds)
	}

	fees, err := c.LedgerFees(context.Background(), "123456", "2026-08-15")
	if err != nil {
		t.Fatalf("LedgerFees: %v", err)
	}
	if len(fees) == 0 {
		t.Error("fees response empty")
	}
}
