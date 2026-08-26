package vipps

import (
	"context"
	"encoding/json"
	"fmt"
)

// Typed Report API v2 parsing (BRY-4). The response envelope
// {items, tryLater, hasMore} is confirmed against the live API, and the per-item
// field names match the documented Report v2 schema (funds item: pspReference,
// time, ledgerDate, entryType, reference, currency, amount, balanceBefore,
// balanceAfter, recipientHandle). Note amount is a flat integer in minor units
// with a *sibling* currency field — NOT the nested {currency,value} the ePayment
// API uses. Each entry keeps its Raw JSON so any residual mismatch against a
// real settlement is easy to diagnose. These rows land in the vipps_import_rows
// review queue (not the GL), so a wrong tag is low-risk.

// reportEnvelope is the shared page wrapper for funds and fees. tryLater=true
// means the report for the date is not finalised yet (retry later).
type reportEnvelope struct {
	Items    []json.RawMessage `json:"items"`
	TryLater bool              `json:"tryLater"`
	HasMore  bool              `json:"hasMore"`
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

// FundsEntry is one settled transaction from the funds report. amount is minor
// units (øre) with a sibling currency; time is the transaction timestamp and
// ledgerDate the accounting date.
type FundsEntry struct {
	PspReference    string          `json:"pspReference"`
	Reference       string          `json:"reference"`  // merchant reference (our chk-…)
	EntryType       string          `json:"entryType"`  // e.g. capture / refund
	Amount          int64           `json:"amount"`     // minor units (øre)
	Currency        string          `json:"currency"`
	Time            string          `json:"time"`       // transaction timestamp (RFC3339)
	LedgerDate      string          `json:"ledgerDate"` // accounting date (YYYY-MM-DD)
	RecipientHandle string          `json:"recipientHandle"`
	Message         string          `json:"message"` // present only with includeGDPRSensitiveData
	Raw             json.RawMessage `json:"-"`
}

// FeeEntry is one fee line from the fees report. Same amount/date convention as
// FundsEntry.
type FeeEntry struct {
	PspReference string          `json:"pspReference"`
	Reference    string          `json:"reference"`
	EntryType    string          `json:"entryType"`
	Amount       int64           `json:"amount"` // minor units (øre)
	Currency     string          `json:"currency"`
	Time         string          `json:"time"`
	LedgerDate   string          `json:"ledgerDate"`
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
