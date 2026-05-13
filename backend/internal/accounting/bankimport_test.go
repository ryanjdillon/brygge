package accounting

import (
	"strings"
	"testing"
)

func TestParseNorwegianNumber(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1234,56", 1234.56},
		{"1 234,56", 1234.56},
		{"-500,00", -500.00},
		{"0", 0},
		{"", 0},
		{"1234.56", 1234.56},
		{"  2 500,00  ", 2500.00},
	}

	for _, tt := range tests {
		got := parseNorwegianNumber(tt.input)
		if got != tt.want {
			t.Errorf("parseNorwegianNumber(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestCSVParserSparebanken(t *testing.T) {
	csv := `Dato;Forklaring;Rentedato;Ut av konto;Inn på konto;KID;Arkivref
15.03.2026;Strømregning;15.03.2026;2 500,00;;12345;REF001
16.03.2026;Medlemskontingent;16.03.2026;;1 500,00;67890;REF002
17.03.2026;Bankgebyr;17.03.2026;50,00;;;;`

	parser := &CSVParser{Format: BankFormats["sparebanken"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// Row 1: outflow (debit column)
	if rows[0].Amount != -2500.00 {
		t.Errorf("row 0 amount = %f, want -2500.00", rows[0].Amount)
	}
	if rows[0].Description != "Strømregning" {
		t.Errorf("row 0 description = %q, want %q", rows[0].Description, "Strømregning")
	}
	if rows[0].KID != "12345" {
		t.Errorf("row 0 KID = %q, want %q", rows[0].KID, "12345")
	}
	if rows[0].Reference != "REF001" {
		t.Errorf("row 0 reference = %q, want %q", rows[0].Reference, "REF001")
	}

	// Row 2: inflow (credit column)
	if rows[1].Amount != 1500.00 {
		t.Errorf("row 1 amount = %f, want 1500.00", rows[1].Amount)
	}
	if rows[1].KID != "67890" {
		t.Errorf("row 1 KID = %q, want %q", rows[1].KID, "67890")
	}

	// Row 3: outflow, no KID
	if rows[2].Amount != -50.00 {
		t.Errorf("row 2 amount = %f, want -50.00", rows[2].Amount)
	}
	if rows[2].KID != "" {
		t.Errorf("row 2 KID = %q, want empty", rows[2].KID)
	}
}

func TestCSVParserDNB(t *testing.T) {
	csv := `Dato;Forklaring;Beløp;KID;Motpart
15.03.2026;Varekjøp;-1249,00;;Biltema AS
16.03.2026;Innbetaling;3000,00;99887;Ola Nordmann`

	parser := &CSVParser{Format: BankFormats["dnb"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0].Amount != -1249.00 {
		t.Errorf("row 0 amount = %f, want -1249.00", rows[0].Amount)
	}
	if rows[0].Counterpart != "Biltema AS" {
		t.Errorf("row 0 counterpart = %q, want %q", rows[0].Counterpart, "Biltema AS")
	}

	if rows[1].Amount != 3000.00 {
		t.Errorf("row 1 amount = %f, want 3000.00", rows[1].Amount)
	}
	if rows[1].KID != "99887" {
		t.Errorf("row 1 KID = %q, want %q", rows[1].KID, "99887")
	}
}

func TestCSVParserSparebank1(t *testing.T) {
	csv := `Bokført dato;Beskrivelse;Ut;Inn;Saldo;KID
01.03.2026;Forsikring;8 500,00;;;
05.03.2026;Havneavgift;;12 000,00;50 000,00;11223`

	parser := &CSVParser{Format: BankFormats["sparebank1"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0].Amount != -8500.00 {
		t.Errorf("row 0 amount = %f, want -8500.00", rows[0].Amount)
	}
	if rows[0].Balance != nil {
		t.Errorf("row 0 balance should be nil (empty cell)")
	}

	if rows[1].Amount != 12000.00 {
		t.Errorf("row 1 amount = %f, want 12000.00", rows[1].Amount)
	}
	if rows[1].Balance == nil || *rows[1].Balance != 50000.00 {
		t.Errorf("row 1 balance = %v, want 50000.00", rows[1].Balance)
	}
	if rows[1].KID != "11223" {
		t.Errorf("row 1 KID = %q, want %q", rows[1].KID, "11223")
	}
}

func TestCSVParserMalformedRowsSkipped(t *testing.T) {
	csv := `Dato;Forklaring;Beløp;KID;Motpart
not-a-date;Bad row;100,00;;
15.03.2026;Good row;-200,00;;Vendor`

	parser := &CSVParser{Format: BankFormats["dnb"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row (malformed skipped), got %d", len(rows))
	}
	if rows[0].Description != "Good row" {
		t.Errorf("expected 'Good row', got %q", rows[0].Description)
	}
}

func TestCSVParserEmptyFile(t *testing.T) {
	csv := `Dato;Forklaring;Beløp;KID;Motpart`

	parser := &CSVParser{Format: BankFormats["dnb"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for header-only file, got %d", len(rows))
	}
}

func TestListBankFormats(t *testing.T) {
	formats := ListBankFormats()
	if len(formats) < 3 {
		t.Errorf("expected at least 3 formats, got %d", len(formats))
	}

	has := make(map[string]bool)
	for _, f := range formats {
		has[f] = true
	}
	for _, expected := range []string{"sparebanken", "dnb", "sparebank1"} {
		if !has[expected] {
			t.Errorf("missing format: %s", expected)
		}
	}
}

func TestCSVParserSparebankNorge(t *testing.T) {
	csv := "\ufeffBokført;Transaksjonsdato;Rentedato;Kategori;Type;Beskrivelse;Melding;Beløp (NOK);Beløp (valuta);Valuta;Vekslingskurs;Arkivref.;Fra konto;Til konto;Kreditornavn;Debitornavn\n" +
		"04.05.2026;;04.05.2026;Innbetaling;OVERF;Overførsel;;3281,55;3281,55;NOK;1;273196130;;34700502793;;\n" +
		"30.04.2026;;30.04.2026;Gebyr;GEBYR;Avtalegiro;;-6;-6;NOK;1;0;34700502793;;;\n" +
		"30.04.2026;;30.04.2026;Innbetaling;OVERFØRSEL;Utb. 2000591 Vippsnr 698382;;1572;1572;NOK;1;272437290;15200281214;34700502793;;Vipps Mobilepay As\n"

	parser := &CSVParser{Format: BankFormats["sparebank-norge-v1"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	if rows[0].Amount != 3281.55 {
		t.Errorf("row 0 amount = %f, want 3281.55", rows[0].Amount)
	}
	if rows[0].Description != "Overførsel" {
		t.Errorf("row 0 description = %q", rows[0].Description)
	}
	if rows[0].Reference != "273196130" {
		t.Errorf("row 0 reference = %q", rows[0].Reference)
	}

	if rows[1].Amount != -6 {
		t.Errorf("row 1 amount = %f, want -6", rows[1].Amount)
	}

	if rows[2].Amount != 1572 {
		t.Errorf("row 2 amount = %f, want 1572", rows[2].Amount)
	}
	if rows[2].Description != "Utb. 2000591 Vippsnr 698382" {
		t.Errorf("row 2 description = %q", rows[2].Description)
	}
	if rows[2].Counterpart != "Vipps Mobilepay As" {
		t.Errorf("row 2 counterpart = %q (expected Debitornavn fallback)", rows[2].Counterpart)
	}
}

func TestCSVParserSparebankNorgeDescriptionJoin(t *testing.T) {
	csv := "Bokført;Beskrivelse;Melding;Beløp (NOK);Arkivref.;Kreditornavn;Debitornavn\n" +
		"15.03.2026;Overførsel;Faktura 2026-001;500,00;REF1;;Ola Nordmann\n"

	parser := &CSVParser{Format: BankFormats["sparebank-norge-v1"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Description != "Overførsel · Faktura 2026-001" {
		t.Errorf("description = %q, want joined", rows[0].Description)
	}
	if rows[0].Counterpart != "Ola Nordmann" {
		t.Errorf("counterpart = %q, want Debitornavn fallback", rows[0].Counterpart)
	}
}

func TestBankRowDateParsing(t *testing.T) {
	csv := `Dato;Forklaring;Beløp;KID;Motpart
01.01.2026;New Year;-100,00;;
31.12.2026;New Years Eve;-200,00;;`

	parser := &CSVParser{Format: BankFormats["dnb"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if rows[0].Date.Day() != 1 || rows[0].Date.Month() != 1 {
		t.Errorf("row 0 date = %v, want Jan 1", rows[0].Date)
	}
	if rows[1].Date.Day() != 31 || rows[1].Date.Month() != 12 {
		t.Errorf("row 1 date = %v, want Dec 31", rows[1].Date)
	}
}
