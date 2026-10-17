package finance

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"
	"time"
)

func TestGeneratePDF(t *testing.T) {
	inv := Invoice{
		ClubName:    "Testvik Båtforening",
		OrgNumber:   "912 345 678",
		ClubAddress: "Havnegata 1, 0150 Oslo",

		MemberName:    "Ola Nordmann",
		MemberAddress: "Storgata 10, 0182 Oslo",

		InvoiceNumber: 42,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 1, 42),
		BankAccount:   "1234.56.78901",

		Lines: []InvoiceLine{
			{Description: "Medlemskontingent 2026", Quantity: 1, UnitPrice: 1500.00},
			{Description: "Båtplass sesong", Quantity: 1, UnitPrice: 5000.00},
			{Description: "Slipavgift", Quantity: 2, UnitPrice: 250.00},
		},
	}

	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}

	// Should produce valid PDF (starts with %PDF-)
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("PDF output doesn't start with %PDF- header")
	}

	// Should be non-trivially sized (at least 1KB for a real PDF)
	if len(pdf) < 1024 {
		t.Errorf("PDF too small: %d bytes", len(pdf))
	}
}

func TestGeneratePDFEmptyLines(t *testing.T) {
	inv := Invoice{
		ClubName:      "Test",
		OrgNumber:     "000 000 000",
		InvoiceNumber: 1,
		IssueDate:     time.Now(),
		DueDate:       time.Now().AddDate(0, 0, 30),
		KID:           "12345",
		BankAccount:   "0000.00.00000",
		Lines:         []InvoiceLine{},
	}

	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF with empty lines: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("PDF output doesn't start with %PDF-")
	}
}

func TestFormatNOK(t *testing.T) {
	tests := []struct {
		amount float64
		want   string
	}{
		{1500.00, "kr 1500.00"},
		{0.50, "kr 0.50"},
		{99999.99, "kr 99999.99"},
	}
	for _, tt := range tests {
		got := formatNOK(tt.amount)
		if got != tt.want {
			t.Errorf("formatNOK(%f) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

// pdfText inflates every content stream of a PDF so tests can look for the
// text drawn on the page.
func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	var out strings.Builder
	rest := pdf
	for {
		start := bytes.Index(rest, []byte("stream\n"))
		if start < 0 {
			return out.String()
		}
		rest = rest[start+len("stream\n"):]
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			return out.String()
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:end])); err == nil {
			b, _ := io.ReadAll(r)
			out.Write(b)
		} else {
			out.Write(rest[:end])
		}
		rest = rest[end:]
	}
}

// A faktura that nets to zero asks for nothing, so its PDF carries neither
// the KID nor the bank account.
func TestGeneratePDFZeroTotalHidesKID(t *testing.T) {
	inv := Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 7,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 1, 7),
		BankAccount:   "1234.56.78901",
		Lines: []InvoiceLine{
			{Description: "Plassleie", Quantity: 1, UnitPrice: 2600},
			{Description: "Fritak", Quantity: 1, UnitPrice: -2600},
		},
	}
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	text := pdfText(t, pdf)
	if strings.Contains(text, inv.KID) || strings.Contains(text, inv.BankAccount) {
		t.Errorf("zero faktura PDF shows the KID or bank account")
	}

	inv.Lines = inv.Lines[:1]
	pdf, err = GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	if !strings.Contains(pdfText(t, pdf), inv.KID) {
		t.Errorf("owed faktura PDF lacks the KID; the text check is not seeing page content")
	}
}
