package accounting

import "testing"

func TestBuildOrderChargeSplit(t *testing.T) {
	sources := []orderChargeSource{
		{InvoiceID: "inv-1", InvoiceNumber: 285, Amount: 300},
	}
	lines := []orderChargeLine{
		{Name: "Gjestedøgn", Category: "guest", Total: 400},
		{Name: "T-skjorte", Category: "merchandise", Total: 150},
		{Name: "Ukjent vare", Category: "no-such-category", Total: 50},
	}

	got := buildOrderChargeSplit(sources, lines)
	if len(got) != 4 {
		t.Fatalf("want 4 credit lines, got %d", len(got))
	}

	// Faktura source → receivables clearing, with the invoice back-link.
	if got[0].Kind != "receivable" || got[0].AccountCode != receivablesAccountCode ||
		got[0].Credit != 300 || got[0].LinkedInvoiceID != "inv-1" {
		t.Errorf("faktura credit wrong: %+v", got[0])
	}
	// Mapped categories → their revenue accounts.
	if got[1].AccountCode != "3200" || got[1].Credit != 400 || got[1].Kind != "revenue" {
		t.Errorf("guest credit wrong: %+v", got[1])
	}
	if got[2].AccountCode != "3300" || got[2].Credit != 150 {
		t.Errorf("merchandise credit wrong: %+v", got[2])
	}
	// Unmapped category → fallback revenue.
	if got[3].AccountCode != vippsFallbackRevenueCode || got[3].Credit != 50 {
		t.Errorf("fallback credit wrong: %+v", got[3])
	}

	// The split must credit exactly the charge total (no money invented/lost).
	var sum float64
	for _, c := range got {
		sum += c.Credit
	}
	if sum != 900 {
		t.Errorf("credits sum = %v, want 900 (300 + 400 + 150 + 50)", sum)
	}
}

func TestBuildOrderChargeSplitEmpty(t *testing.T) {
	if got := buildOrderChargeSplit(nil, nil); len(got) != 0 {
		t.Errorf("empty order should yield no credits, got %d", len(got))
	}
}
