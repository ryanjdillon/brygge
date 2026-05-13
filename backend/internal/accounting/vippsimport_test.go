package accounting

import (
	"strings"
	"testing"
)

func TestParseVippsCSVHandlesThreeRowTypes(t *testing.T) {
	csv := `Salgssted,MSN/Vippsnummer,Land,Betalingsløsning,Tidspunkt,Bokføringsdato,Type,Beløp,Balanse,Gebyr,Nettobeløp,Valuta,Kundens navn,Kundens telefonnummer,Melding,Kategori,PSP-referanse,Ordre-ID/Referanse,Utbetalingsnummer,Bankkonto for utbetaling,Planlagt utbetalingsdato
Klokkarvik Båtlag,548005,Norge,Valgfritt beløp,2026-03-23 14:24:30,2026-03-23,Belastning,2700.00,2700.00,-47.25,2652.75,NOK,Geir Magne Ellingsen,+47 xxxx 5202,"Sommer sesong
 Askeladden ",,35859820875,13120637102,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Gebyrer fratrukket,-47.25,2652.75,,,NOK,,,,,460017-20260323,,,,
Klokkarvik Båtlag,548005,Norge,,2026-03-24 00:23:17,2026-03-23,Utbetaling planlagt,-2652.75,0.00,,,NOK,,,,,460017-2000167,Utb. 2000167 Vippsnr 548005,2000167,NO34 7005 0279 3,2026-03-25
`
	rows, err := ParseVippsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseVippsCSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	belastning := rows[0]
	if belastning.RowType != VippsRowBelastning {
		t.Errorf("row 0 type = %q, want belastning", belastning.RowType)
	}
	if belastning.MSN != "548005" {
		t.Errorf("row 0 msn = %q", belastning.MSN)
	}
	if belastning.Amount != 2700.00 || belastning.Fee != -47.25 || belastning.NetAmount != 2652.75 {
		t.Errorf("row 0 amounts = (%f, %f, %f)", belastning.Amount, belastning.Fee, belastning.NetAmount)
	}
	if belastning.CustomerName != "Geir Magne Ellingsen" {
		t.Errorf("row 0 customer = %q", belastning.CustomerName)
	}
	if !strings.Contains(belastning.Message, "Sommer sesong") || !strings.Contains(belastning.Message, "Askeladden") {
		t.Errorf("row 0 message did not preserve multi-line content: %q", belastning.Message)
	}
	if belastning.PSPRef != "35859820875" || belastning.OrderID != "13120637102" {
		t.Errorf("row 0 psp/order = (%q, %q)", belastning.PSPRef, belastning.OrderID)
	}
	if belastning.TxAt.Year() != 2026 || belastning.TxAt.Hour() != 14 {
		t.Errorf("row 0 tx_at = %v", belastning.TxAt)
	}

	fee := rows[1]
	if fee.RowType != VippsRowFee {
		t.Errorf("row 1 type = %q, want fee", fee.RowType)
	}
	if fee.Amount != -47.25 {
		t.Errorf("row 1 amount = %f", fee.Amount)
	}

	payout := rows[2]
	if payout.RowType != VippsRowPayout {
		t.Errorf("row 2 type = %q, want payout", payout.RowType)
	}
	if payout.Amount != -2652.75 {
		t.Errorf("row 2 amount = %f", payout.Amount)
	}
	// SettlementNumber is the integer column; OrderID carries the human-readable
	// "Utb. <n> Vippsnr <msn>" label that also appears in the bank statement.
	if payout.SettlementNumber != "2000167" {
		t.Errorf("row 2 settlement_number = %q, want 2000167", payout.SettlementNumber)
	}
	if payout.OrderID != "Utb. 2000167 Vippsnr 548005" {
		t.Errorf("row 2 order_id = %q (expected bank-matchable label)", payout.OrderID)
	}
	if payout.PayoutAccount != "NO34 7005 0279 3" {
		t.Errorf("row 2 payout_account = %q", payout.PayoutAccount)
	}
	if payout.ScheduledPayoutDate.Day() != 25 || payout.ScheduledPayoutDate.Month() != 3 {
		t.Errorf("row 2 scheduled_payout_date = %v", payout.ScheduledPayoutDate)
	}
}

func TestVippsRowHashDistinguishesRowTypes(t *testing.T) {
	base := VippsRow{
		RowType:          VippsRowBelastning,
		PSPRef:           "35038478561",
		OrderID:          "12665557171",
		Amount:           1300.00,
		SettlementNumber: "",
	}
	h1 := VippsRowHash("club-1", base)
	h2 := VippsRowHash("club-1", base)
	if h1 != h2 {
		t.Errorf("hash not stable")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(h1))
	}

	other := base
	other.RowType = VippsRowFee
	if VippsRowHash("club-1", other) == h1 {
		t.Errorf("hash collision across row types")
	}

	otherClub := base
	if VippsRowHash("club-2", otherClub) == h1 {
		t.Errorf("hash collision across clubs")
	}
}

func TestParseDotNumber(t *testing.T) {
	cases := map[string]float64{
		"":             0,
		"0":            0,
		"1300.00":      1300.00,
		"-47.25":       -47.25,
		" 200.00 ":     200.00,
		"not-a-number": 0,
	}
	for in, want := range cases {
		if got := parseDotNumber(in); got != want {
			t.Errorf("parseDotNumber(%q) = %f, want %f", in, got, want)
		}
	}
}
