package accounting

import (
	"math"
	"testing"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

func TestVippsReportRows(t *testing.T) {
	funds := []vipps.FundsEntry{
		{
			PspReference:    "psp-1",
			Reference:       "chk-abc123",
			EntryType:       "capture",
			Amount:          vipps.Money{Currency: "NOK", Value: 12500},
			SettlementID:    "settle-9",
			BookingDate:     "2026-08-15",
			TransactionDate: "2026-08-14",
		},
	}
	fees := []vipps.FeeEntry{
		{
			PspReference: "psp-1",
			Reference:    "chk-abc123",
			Name:         "Transaction fee",
			Amount:       vipps.Money{Currency: "NOK", Value: 145},
			SettlementID: "settle-9",
			BookingDate:  "2026-08-15",
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
	if f.OrderID != "chk-abc123" || f.PSPRef != "psp-1" || f.SettlementNumber != "settle-9" || f.MSN != "123456" {
		t.Errorf("unexpected funds row fields: %+v", f)
	}
	if math.Abs(f.Amount-125.00) > 0.001 {
		t.Errorf("funds amount = %v, want 125.00", f.Amount)
	}
	if f.BookingDate.IsZero() {
		t.Error("funds booking date not parsed")
	}

	fee := rows[1]
	if fee.RowType != VippsRowFee || fee.Message != "Transaction fee" {
		t.Errorf("unexpected fee row: %+v", fee)
	}
	if math.Abs(fee.Fee-1.45) > 0.001 {
		t.Errorf("fee = %v, want 1.45", fee.Fee)
	}
}

func TestVippsReportRowsRefundToOther(t *testing.T) {
	funds := []vipps.FundsEntry{
		{EntryType: "refund", Reference: "chk-x", Amount: vipps.Money{Value: 5000}},
	}
	rows := VippsReportRows("m", funds, nil)
	if len(rows) != 1 || rows[0].RowType != VippsRowOther {
		t.Errorf("refund should map to 'other', got %+v", rows)
	}
}
