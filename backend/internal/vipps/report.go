package vipps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Report API (settlement / ledger) access. Same Access Token + subscription-key
// + MSN auth as ePayment, so it reuses the shared client. The response shapes
// are returned raw for now (BRY-4 phase 1 discovery) — the Vipps docs don't
// publish the field names, so we capture a live response before defining typed
// structs and the vipps_import_rows mapper.
//
// Endpoints:
//   GET /settlement/v1/ledgers                                 → your ledgers
//   GET /report/v2/ledgers/{ledgerId}/funds/dates/{YYYY-MM-DD} → settled funds
//   GET /report/v2/ledgers/{ledgerId}/fees/dates/{YYYY-MM-DD}  → fees

// ListLedgers returns the raw ledgers response for this merchant. The ledgerId
// it yields is the key for the funds/fees calls. Also the cheapest probe for
// whether Report API access is enabled on the merchant.
func (c *Client) ListLedgers(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/settlement/v1/ledgers", "", nil, &out); err != nil {
		return nil, fmt.Errorf("list ledgers: %w", err)
	}
	return out, nil
}

// LedgerFunds returns the raw settled-funds report for a ledger on a date
// (date is YYYY-MM-DD).
func (c *Client) LedgerFunds(ctx context.Context, ledgerID, date string) (json.RawMessage, error) {
	var out json.RawMessage
	path := "/report/v2/ledgers/" + ledgerID + "/funds/dates/" + date
	if err := c.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, fmt.Errorf("ledger funds %s %s: %w", ledgerID, date, err)
	}
	return out, nil
}

// LedgerFees returns the raw fees report for a ledger on a date (YYYY-MM-DD).
func (c *Client) LedgerFees(ctx context.Context, ledgerID, date string) (json.RawMessage, error) {
	var out json.RawMessage
	path := "/report/v2/ledgers/" + ledgerID + "/fees/dates/" + date
	if err := c.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, fmt.Errorf("ledger fees %s %s: %w", ledgerID, date, err)
	}
	return out, nil
}
