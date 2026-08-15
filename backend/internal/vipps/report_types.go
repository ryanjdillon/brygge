package vipps

import (
	"context"
	"encoding/json"
	"fmt"
)

// Typed Report API v2 parsing (BRY-4). The response envelope
// {items, tryLater, hasMore} is confirmed against the live API; the per-item
// field names below follow the documented Report v2 shape and are PENDING
// VERIFICATION against the first real settlement. Unknown fields degrade to zero
// values, and each entry keeps its Raw JSON so a mismatch is easy to diagnose
// and correct. These rows land in the vipps_import_rows review queue (not the
// GL), so a wrong tag is low-risk and self-evident in the Vipps-kø.

// reportEnvelope is the shared page wrapper for funds and fees. tryLater=true
// means the report for the date is not finalised yet (retry later).
type reportEnvelope struct {
	Items    []json.RawMessage `json:"items"`
	TryLater bool              `json:"tryLater"`
	HasMore  bool              `json:"hasMore"`
}

// Money is a Vipps amount in minor units (øre).
type Money struct {
	Currency string `json:"currency"`
	Value    int64  `json:"value"`
}

// Ledger is one settlement ledger from /settlement/v1/ledgers.
type Ledger struct {
	LedgerID          string `json:"ledgerId"`
	Currency          string `json:"currency"`
	PayoutBankAccount struct {
		Scheme string `json:"scheme"`
		ID     string `json:"id"`
	} `json:"payoutBankAccount"`
}

// FundsEntry is one settled transaction from the funds report.
type FundsEntry struct {
	PspReference    string          `json:"pspReference"`
	Reference       string          `json:"reference"` // merchant reference (our chk-…)
	EntryType       string          `json:"entryType"` // e.g. capture / refund
	Amount          Money           `json:"amount"`
	SettlementID    string          `json:"settlementId"`
	BookingDate     string          `json:"bookingDate"`
	TransactionDate string          `json:"transactionDate"`
	Raw             json.RawMessage `json:"-"`
}

// FeeEntry is one fee line from the fees report.
type FeeEntry struct {
	PspReference string          `json:"pspReference"`
	Reference    string          `json:"reference"`
	Name         string          `json:"name"`
	Amount       Money           `json:"amount"`
	SettlementID string          `json:"settlementId"`
	BookingDate  string          `json:"bookingDate"`
	Raw          json.RawMessage `json:"-"`
}

// LedgersParsed lists the merchant's settlement ledgers.
func (c *Client) LedgersParsed(ctx context.Context) ([]Ledger, error) {
	raw, err := c.ListLedgers(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []Ledger `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse ledgers: %w", err)
	}
	return out.Items, nil
}

// FundsForDate returns the settled funds entries for a date. tryLater=true means
// the report is not finalised yet (no entries returned; retry later). Only the
// first page is read — daily volumes are small; if hasMore is ever set the
// caller should revisit pagination once the cursor param is confirmed live.
func (c *Client) FundsForDate(ctx context.Context, ledgerID, date string) (entries []FundsEntry, tryLater bool, err error) {
	raw, err := c.LedgerFunds(ctx, ledgerID, date)
	if err != nil {
		return nil, false, err
	}
	return parseFundsPage(raw)
}

// FeesForDate returns the fee entries for a date. tryLater as in FundsForDate.
func (c *Client) FeesForDate(ctx context.Context, ledgerID, date string) (entries []FeeEntry, tryLater bool, err error) {
	raw, err := c.LedgerFees(ctx, ledgerID, date)
	if err != nil {
		return nil, false, err
	}
	return parseFeesPage(raw)
}

// parseFundsPage decodes one funds report page. Split out for testing without
// the HTTP round-trip.
func parseFundsPage(raw json.RawMessage) (entries []FundsEntry, tryLater bool, err error) {
	var env reportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false, fmt.Errorf("parse funds envelope: %w", err)
	}
	if env.TryLater {
		return nil, true, nil
	}
	for _, item := range env.Items {
		var e FundsEntry
		if err := json.Unmarshal(item, &e); err != nil {
			return nil, false, fmt.Errorf("parse funds item: %w", err)
		}
		e.Raw = item
		entries = append(entries, e)
	}
	return entries, false, nil
}

func parseFeesPage(raw json.RawMessage) (entries []FeeEntry, tryLater bool, err error) {
	var env reportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false, fmt.Errorf("parse fees envelope: %w", err)
	}
	if env.TryLater {
		return nil, true, nil
	}
	for _, item := range env.Items {
		var e FeeEntry
		if err := json.Unmarshal(item, &e); err != nil {
			return nil, false, fmt.Errorf("parse fee item: %w", err)
		}
		e.Raw = item
		entries = append(entries, e)
	}
	return entries, false, nil
}
