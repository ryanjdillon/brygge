package vipps

import "testing"

// Fixture matches the documented Report API v2 funds item: amount is a flat
// minor-unit integer with a sibling currency, dates are time/ledgerDate.
func TestParseFundsPage(t *testing.T) {
	raw := []byte(`{
		"items": [
			{
				"pspReference": "psp-1",
				"reference": "chk-abc123",
				"entryType": "capture",
				"currency": "NOK",
				"amount": 12500,
				"time": "2026-08-14T00:00:00.000000Z",
				"ledgerDate": "2026-08-15",
				"recipientHandle": "NO:123455"
			}
		],
		"tryLater": false,
		"hasMore": false
	}`)

	entries, tryLater, err := parseFundsPage(raw)
	if err != nil {
		t.Fatalf("parseFundsPage: %v", err)
	}
	if tryLater {
		t.Fatal("tryLater should be false")
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Reference != "chk-abc123" || e.PspReference != "psp-1" || e.EntryType != "capture" {
		t.Errorf("unexpected refs/type: %+v", e)
	}
	if e.Amount != 12500 || e.Currency != "NOK" {
		t.Errorf("unexpected amount: value=%d currency=%s", e.Amount, e.Currency)
	}
	if e.LedgerDate != "2026-08-15" || e.Time != "2026-08-14T00:00:00.000000Z" {
		t.Errorf("unexpected dates: %+v", e)
	}
	if len(e.Raw) == 0 {
		t.Error("raw item not retained")
	}
}

func TestParseFundsPageTryLater(t *testing.T) {
	raw := []byte(`{"items": [], "tryLater": true, "hasMore": false}`)
	entries, tryLater, err := parseFundsPage(raw)
	if err != nil {
		t.Fatalf("parseFundsPage: %v", err)
	}
	if !tryLater {
		t.Error("tryLater should be true")
	}
	if len(entries) != 0 {
		t.Errorf("want 0 entries, got %d", len(entries))
	}
}

func TestParseFeesPage(t *testing.T) {
	raw := []byte(`{"items":[{"pspReference":"psp-1","reference":"chk-abc123","entryType":"fee","currency":"NOK","amount":145,"ledgerDate":"2026-08-15","time":"2026-08-15T00:00:00.000000Z"}],"tryLater":false,"hasMore":false}`)
	entries, tryLater, err := parseFeesPage(raw)
	if err != nil || tryLater || len(entries) != 1 {
		t.Fatalf("parseFeesPage: err=%v tryLater=%v n=%d", err, tryLater, len(entries))
	}
	if entries[0].EntryType != "fee" || entries[0].Amount != 145 || entries[0].Currency != "NOK" {
		t.Errorf("unexpected fee: %+v", entries[0])
	}
}
