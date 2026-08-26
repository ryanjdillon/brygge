package accounting

import (
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// VippsReportRows converts parsed Report API v2 funds + fees entries into the
// canonical VippsRow shape, so settlement data from the API flows through the
// exact same idempotent import + reconcile pipeline as the Vipps CSV export
// (BRY-4). The merchant reference (our chk-… order reference) rides on
// VippsRow.OrderID, which is what lets a settled charge auto-link back to its
// checkout order/invoice later (BRY-264).
func VippsReportRows(msn string, funds []vipps.FundsEntry, fees []vipps.FeeEntry) []VippsRow {
	rows := make([]VippsRow, 0, len(funds)+len(fees))

	for _, f := range funds {
		rowType := VippsRowBelastning
		if !strings.Contains(strings.ToLower(f.EntryType), "capture") && f.EntryType != "" {
			// Refunds and any non-capture entry types are surfaced for review
			// rather than silently booked as a customer payment.
			rowType = VippsRowOther
		}
		msg := f.EntryType
		rows = append(rows, VippsRow{
			RowType:     rowType,
			MSN:         msn,
			TxAt:        parseReportDate(f.Time),
			BookingDate: parseReportDate(f.LedgerDate),
			Amount:      minorToMajor(f.Amount),
			Message:     msg,
			PSPRef:      f.PspReference,
			OrderID:     f.Reference,
		})
	}

	for _, fee := range fees {
		rows = append(rows, VippsRow{
			RowType:     VippsRowFee,
			MSN:         msn,
			TxAt:        parseReportDate(fee.Time),
			BookingDate: parseReportDate(fee.LedgerDate),
			Fee:         minorToMajor(fee.Amount),
			Message:     fee.EntryType,
			PSPRef:      fee.PspReference,
			OrderID:     fee.Reference,
		})
	}

	return rows
}

// minorToMajor converts øre to kroner.
func minorToMajor(v int64) float64 { return float64(v) / 100.0 }

// parseReportDate accepts a bare date (YYYY-MM-DD) or an RFC3339 timestamp and
// returns a zero time.Time when empty/unparseable.
func parseReportDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
