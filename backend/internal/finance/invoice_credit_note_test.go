package finance

import (
	"strings"
	"testing"
	"time"
)

func TestGeneratePDFFakturaAsksForPayment(t *testing.T) {
	inv := Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 289,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 289, 1),
		BankAccount:   "1234.56.78901",
		Lines:         []InvoiceLine{{Description: "Plassleie", Quantity: 1, UnitPrice: 5}},
	}
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{
		"(Faktura)", "Fakturanummer:", "Kontonummer:", "KID:", inv.KID, "Forfallsdato:", "31.03.2026",
		"kr 5.00", "Vennligst betal kr 5.00 til kontonummer 1234.56.78901",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("faktura PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Kreditnota", "Kreditbel", "Ingen betaling"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("faktura PDF shows %q", unwanted)
		}
	}
}

// A credit note asks nothing of its recipient: no account, KID, due date or
// amount to pay, only the amount credited.
func TestGeneratePDFCreditNoteAsksForNothing(t *testing.T) {
	inv := Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 289,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 289, 1),
		BankAccount:   "1234.56.78901",
		CreditNote:    true,
		Lines: []InvoiceLine{
			{Description: "Plassleie", SubDescription: "Kreditnota for faktura 12 – refusjon kortbetaling 01.03.2026", Quantity: 1, UnitPrice: -5},
		},
	}
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{
		"(Kreditnota)", "Kreditnotanummer:", "289", "Kreditnotadato:", "01.03.2026", "Kreditbel",
		"(kr 5.00)", "Ingen betaling kreves.", "(kr -5.00)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("credit note PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{
		"(Faktura)", "Fakturanummer", "Fakturadato", "Kontonummer", "KID", inv.KID, inv.BankAccount,
		"Forfallsdato", "betale", "Vennligst", "refunderes", "orientering",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("credit note PDF shows %q", unwanted)
		}
	}

	inv.CardRefund = true
	pdf, err = GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	if !strings.Contains(pdfText(t, pdf), "refunderes til kortet som ble brukt ved betalingen") {
		t.Errorf("card refund credit note does not say the amount is refunded to the card")
	}
}

// A caller that forgets CreditNote still gets the credit note layout for a
// negative total, never a request to pay a negative amount.
func TestGeneratePDFNegativeTotalFailsClosed(t *testing.T) {
	inv := Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 289,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 289, 1),
		BankAccount:   "1234.56.78901",
		Lines:         []InvoiceLine{{Description: "Plassleie", Quantity: 1, UnitPrice: -5}},
	}
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{"(Kreditnota)", "Kreditnotanummer:", "Kreditbel", "Ingen betaling kreves."} {
		if !strings.Contains(text, want) {
			t.Errorf("negative-total PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{"(Faktura)", "Kontonummer", "KID", "Forfallsdato", "Vennligst", "refunderes"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("negative-total PDF shows %q", unwanted)
		}
	}
}

func TestGeneratePDFZeroTotalStaysAFaktura(t *testing.T) {
	inv := Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 7,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 7, 1),
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
	for _, want := range []string{"(Faktura)", "Fakturanummer:", "(kr 0.00)", "Ingen betaling"} {
		if !strings.Contains(text, want) {
			t.Errorf("zero faktura PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Kreditnota", "Kontonummer", "Forfallsdato", "Vennligst"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("zero faktura PDF shows %q", unwanted)
		}
	}
}
