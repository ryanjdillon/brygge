package accounting

import "testing"

func TestExtractKIDFromDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want string
	}{
		{"DNB Betalt pattern", "Fra: Ryan James Dillon Betalt: 12.05.26 · 000000880013", "000000880013"},
		{"different KID", "Fra: Claus Grindheim Betalt: 13.05.26 · 000000830018", "000000830018"},
		{"empty input", "", ""},
		{"no Betalt", "Mobilbank Dato 12.05.2026 · Fakturanummer 51", ""},
		{"Nettgiro/Overførsel — no payer info", "Nettgiro I Dag", ""},
		{"Luhn rejects arbitrary digit runs", "Some text · 123456789", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractKIDFromDescription(tt.desc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractInvoiceNumberFromDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want string
	}{
		{"Fakturanummer N", "Mobilbank Dato 12.05.2026 Kl. 21.17.23 · Fakturanummer 51", "51"},
		{"Faktura N (short form)", "Innbetaling Faktura 12", "12"},
		{"Faktura nr. N", "Faktura nr. 17 betalt", "17"},
		{"empty", "", ""},
		{"no match", "Fra: Anette Betalt: 12.05.26 · 000000290015", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractInvoiceNumberFromDescription(tt.desc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractPayerFromDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want string
	}{
		{"single name", "Fra: Atle Harald Berge Betalt: 13.05.26 · 000000420018", "Atle Harald Berge"},
		{"three parts", "Fra: Anette Rakli Børnes Betalt: 12.05.26 · 000000290015", "Anette Rakli Børnes"},
		{"no prefix", "Mobilbank Dato 12.05.2026 · Fakturanummer 51", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractPayerFromDescription(tt.desc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
