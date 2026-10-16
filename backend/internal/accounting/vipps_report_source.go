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
		rowType, settlement := classifyReportFunds(f.EntryType, f.Reference, f.PspReference)
		rows = append(rows, VippsRow{
			RowType:          rowType,
			MSN:              msn,
			TxAt:             parseReportDate(f.Time),
			BookingDate:      parseReportDate(f.LedgerDate),
			Amount:           minorToMajor(f.Amount),
			Message:          f.EntryType,
			PSPRef:           f.PspReference,
			OrderID:          f.Reference,
			SettlementNumber: settlement,
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

// classifyReportFunds maps a funds-report entry onto the CSV export's row
// vocabulary so both sources reconcile the same way. A capture is a customer
// payment; the settlement's payout (reference "Utb. <n> Vippsnr <msn>") is the
// payout row carrying its settlement number; the fees retained from the
// settlement are the per-settlement fee row (amount negative, as in the CSV).
// Anything else (refunds, adjustments) stays "other" for manual review rather
// than being booked as revenue.
func classifyReportFunds(entryType, reference, pspRef string) (VippsRowType, string) {
	t := strings.ToLower(entryType)
	if m := VippsSettlementPattern.FindStringSubmatch(reference); len(m) == 3 {
		return VippsRowPayout, m[1]
	}
	switch {
	case strings.Contains(t, "refund"), strings.Contains(t, "revers"):
		return VippsRowOther, ""
	case strings.Contains(t, "payout"):
		// No "Utb." reference: fall back to the settlement number Vipps
		// appends to the pspReference ("<ledger>-<settlement>").
		settlement := ""
		if i := strings.LastIndex(pspRef, "-"); i >= 0 {
			settlement = pspRef[i+1:]
		}
		return VippsRowPayout, settlement
	case strings.Contains(t, "fee"):
		return VippsRowFee, ""
	case t == "" || strings.Contains(t, "capture"):
		return VippsRowBelastning, ""
	default:
		return VippsRowOther, ""
	}
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
