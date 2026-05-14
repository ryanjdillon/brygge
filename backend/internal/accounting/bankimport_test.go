package accounting

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
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

func TestCollapseWhitespace(t *testing.T) {
	cases := map[string]string{
		"":                                              "",
		"plain":                                         "plain",
		"  trim me  ":                                   "trim me",
		"line1\nline2":                                  "line1 line2",
		"tab\there":                                     "tab here",
		"multi   spaces":                                "multi spaces",
		"FAKTURANUMMER\n----\n996066430125 1898,69 0":   "FAKTURANUMMER ---- 996066430125 1898,69 0",
		"a\r\nb":                                        "a b",
	}
	for in, want := range cases {
		if got := collapseWhitespace(in); got != want {
			t.Errorf("collapseWhitespace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCSVParserDescriptionStripsNewlines(t *testing.T) {
	// A multi-line Melding field (matches the Norsk Tipping invoice
	// rows the user reported) should be flattened into one line.
	csv := "Bokført;Beskrivelse;Melding;Beløp (NOK);Arkivref.;Kreditornavn;Debitornavn\n" +
		"07.01.2026;Innbetaling;\"FAKTURANUMMER  BELØP  FAKTURADATO\n----------------  ------\n996066430125 1898,69 0\";1898.69;R1;Norsk Tipping As;\n"

	parser := &CSVParser{Format: BankFormats["sparebank-norge-v1"]}
	rows, err := parser.Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if strings.Contains(rows[0].Description, "\n") {
		t.Errorf("description still contains newlines: %q", rows[0].Description)
	}
	if !strings.Contains(rows[0].Description, "Innbetaling · FAKTURANUMMER") {
		t.Errorf("description does not look flattened: %q", rows[0].Description)
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

func TestBankRowHashStableAndDistinct(t *testing.T) {
	clubA := "00000000-0000-0000-0000-00000000000a"
	d := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	row := BankRow{
		Date:        d,
		Description: "Overførsel",
		Amount:      1572.00,
		Reference:   "272437290",
		Counterpart: "Vipps Mobilepay As",
	}

	h1 := BankRowHash(clubA, row)
	h2 := BankRowHash(clubA, row)
	if h1 != h2 {
		t.Errorf("hash is not stable: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(h1))
	}

	row2 := row
	row2.Amount = 1573.00
	if BankRowHash(clubA, row2) == h1 {
		t.Errorf("hash collision across distinct amounts")
	}

	row3 := row
	row3.Date = d.AddDate(0, 0, 1)
	if BankRowHash(clubA, row3) == h1 {
		t.Errorf("hash collision across distinct dates")
	}

	row4 := row
	row4.Reference = "OTHER"
	if BankRowHash(clubA, row4) == h1 {
		t.Errorf("hash collision across distinct references")
	}
}

func TestBankRowHashMatchesSQLBackfill(t *testing.T) {
	// This recipe must match the SQL backfill in 000037_bank_import_dedup.up.sql.
	// If you change either side, change both.
	row := BankRow{
		Date:        time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		Description: "Strømregning",
		Amount:      -2500.00,
		Reference:   "REF001",
		Counterpart: "",
	}
	got := BankRowHash("club", row)

	expectedInput := strings.ToLower(strings.Join([]string{
		"2026-03-15",
		"-2500.00",
		"REF001",
		"Strømregning",
		"",
	}, "|"))
	sum := sha256.Sum256([]byte(expectedInput))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("hash recipe mismatch:\n got  %s\n want %s", got, want)
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
