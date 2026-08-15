package vipps

import "testing"

// Fixture follows the documented Report API v2 funds shape. Field names are
// pending verification against the first real settlement (BRY-4); this test
// pins the current expected mapping so a real-data diff is obvious.
func TestParseFundsPage(t *testing.T) {
	raw := []byte(`{
		"items": [
			{
				"pspReference": "psp-1",
				"reference": "chk-abc123",
				"entryType": "capture",
				"amount": {"currency": "NOK", "value": 12500},
				"settlementId": "settle-9",
				"bookingDate": "2026-08-15",
				"transactionDate": "2026-08-14"
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
	if e.Amount.Value != 12500 || e.Amount.Currency != "NOK" {
		t.Errorf("unexpected amount: %+v", e.Amount)
	}
	if e.SettlementID != "settle-9" || e.BookingDate != "2026-08-15" {
		t.Errorf("unexpected settlement/date: %+v", e)
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
	raw := []byte(`{"items":[{"pspReference":"psp-1","reference":"chk-abc123","name":"Transaction fee","amount":{"currency":"NOK","value":145},"settlementId":"settle-9","bookingDate":"2026-08-15"}],"tryLater":false,"hasMore":false}`)
	entries, tryLater, err := parseFeesPage(raw)
	if err != nil || tryLater || len(entries) != 1 {
		t.Fatalf("parseFeesPage: err=%v tryLater=%v n=%d", err, tryLater, len(entries))
	}
	if entries[0].Name != "Transaction fee" || entries[0].Amount.Value != 145 {
		t.Errorf("unexpected fee: %+v", entries[0])
	}
}
