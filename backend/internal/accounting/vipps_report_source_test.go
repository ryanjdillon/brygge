package accounting

import (
	"math"
	"testing"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

func TestVippsReportRows(t *testing.T) {
	funds := []vipps.FundsEntry{
		{
			PspReference: "psp-1",
			Reference:    "chk-abc123",
			EntryType:    "capture",
			Amount:       12500,
			Currency:     "NOK",
			LedgerDate:   "2026-08-15",
			Time:         "2026-08-14T00:00:00.000000Z",
		},
	}
	fees := []vipps.FeeEntry{
		{
			PspReference: "psp-1",
			Reference:    "chk-abc123",
			EntryType:    "fee",
			Amount:       145,
			Currency:     "NOK",
			LedgerDate:   "2026-08-15",
			Time:         "2026-08-15T00:00:00.000000Z",
		},
	}

	rows := VippsReportRows("123456", funds, fees)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}

	f := rows[0]
	if f.RowType != VippsRowBelastning {
		t.Errorf("funds row type = %q, want belastning", f.RowType)
	}
	if f.OrderID != "chk-abc123" || f.PSPRef != "psp-1" || f.MSN != "123456" {
		t.Errorf("unexpected funds row fields: %+v", f)
	}
	if math.Abs(f.Amount-125.00) > 0.001 {
		t.Errorf("funds amount = %v, want 125.00", f.Amount)
	}
	if f.BookingDate.IsZero() {
		t.Error("funds booking date not parsed")
	}

	fee := rows[1]
	if fee.RowType != VippsRowFee || fee.Message != "fee" {
		t.Errorf("unexpected fee row: %+v", fee)
	}
	if math.Abs(fee.Fee-1.45) > 0.001 {
		t.Errorf("fee = %v, want 1.45", fee.Fee)
	}
}

func TestVippsReportRowsRefundToOther(t *testing.T) {
	funds := []vipps.FundsEntry{
		{EntryType: "refund", Reference: "chk-x", Amount: 5000, Currency: "NOK"},
	}
	rows := VippsReportRows("m", funds, nil)
	if len(rows) != 1 || rows[0].RowType != VippsRowOther {
		t.Errorf("refund should map to 'other', got %+v", rows)
	}
}
