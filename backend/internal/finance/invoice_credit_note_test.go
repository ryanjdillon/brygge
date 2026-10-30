package finance

import (
	"regexp"
	"slices"
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

func settledFixture() Invoice {
	return Invoice{
		ClubName:      "Testvik Båtforening",
		InvoiceNumber: 290,
		IssueDate:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		KID:           GenerateKID("000", 290, 1),
		BankAccount:   "1234.56.78901",
		Lines:         []InvoiceLine{{Description: "Plassleie", Quantity: 1, UnitPrice: 5}},
	}
}

// The faktura a failed refund issues is already paid: it stays a Faktura with
// its number and date, but shows no way to pay and says so.
func TestGeneratePDFSettledFakturaAsksForNothing(t *testing.T) {
	inv := settledFixture()
	inv.Settled = true
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatalf("GeneratePDF: %v", err)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{
		"(Faktura)", "Fakturanummer:", "290", "Fakturadato:", "01.03.2026", "Status:", "(Betalt)",
		"kr 5.00", "Ingen betaling kreves. Fakturaen er allerede betalt.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("settled faktura PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{
		"Kreditnota", "Kontonummer", "KID", inv.KID, "1234.56.78901", "Forfallsdato", "Bel\xf8p \xe5 betale", "Vennligst",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("settled faktura PDF shows %q", unwanted)
		}
	}
}

// drawnStrings lists the text a PDF draws, in drawing order.
func drawnStrings(text string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\(([^)]*)\)Tj`).FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// An unsettled faktura renders exactly the text it always has: the whole
// payment block (account, KID, amount to pay, due date) and the request to
// pay, in this order, and nothing of the settled layout.
func TestGeneratePDFUnsettledFakturaUnchanged(t *testing.T) {
	inv := settledFixture()
	inv.Settled = false
	pdf, err := GeneratePDF(inv)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Testvik B\xe5tforening", "Faktura",
		"Fakturanummer:", "290", "Fakturadato:", "01.03.2026",
		"Kontonummer:", "1234.56.78901", "KID:", inv.KID,
		"Bel\xf8p \xe5 betale:", "kr 5.00", "Forfallsdato:", "01.03.2026",
		"#", "Beskrivelse", "Antall", "Enhetspris", "Sum",
		"1", "Plassleie", "1", "kr 5.00", "kr 5.00",
		"Totalt", "kr 5.00",
		"Vennligst betal kr 5.00 til kontonummer 1234.56.78901 med KID " + inv.KID + " innen 01.03.2026.",
		"Testvik B\xe5tforening",
	}
	if got := drawnStrings(pdfText(t, pdf)); !slices.Equal(got, want) {
		t.Errorf("faktura text = %q\nwant %q", got, want)
	}
}

// Settled never overrides the other kinds: a negative total is a credit note
// and a zero total keeps its own wording, whether or not Settled is set.
func TestGeneratePDFSettledDoesNotChangeCreditNoteOrZeroFaktura(t *testing.T) {
	credit := settledFixture()
	credit.Lines[0].UnitPrice = -5
	zero := settledFixture()
	zero.Lines = []InvoiceLine{
		{Description: "Plassleie", Quantity: 1, UnitPrice: 2600},
		{Description: "Fritak", Quantity: 1, UnitPrice: -2600},
	}
	for name, inv := range map[string]Invoice{"credit note": credit, "zero faktura": zero} {
		t.Run(name, func(t *testing.T) {
			without, err := GeneratePDF(inv)
			if err != nil {
				t.Fatal(err)
			}
			inv.Settled = true
			with, err := GeneratePDF(inv)
			if err != nil {
				t.Fatal(err)
			}
			if pdfText(t, without) != pdfText(t, with) {
				t.Error("Settled changes the document")
			}
			if text := pdfText(t, with); strings.Contains(text, "Betalt") || strings.Contains(text, "allerede betalt") {
				t.Errorf("shows a paid status: %s", text)
			}
		})
	}
}
