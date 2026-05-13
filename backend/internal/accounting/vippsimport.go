package accounting

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// VippsRowType is the normalized kind of a Vipps oppgjørsrapport row.
type VippsRowType string

const (
	VippsRowBelastning VippsRowType = "belastning" // customer payment in
	VippsRowFee        VippsRowType = "fee"        // Gebyrer fratrukket (per-settlement fee aggregate)
	VippsRowPayout     VippsRowType = "payout"     // Utbetaling planlagt → bank
	VippsRowOther      VippsRowType = "other"
)

// VippsRow is the normalized representation of a single Vipps CSV row.
type VippsRow struct {
	RowType             VippsRowType
	MSN                 string
	TxAt                time.Time
	BookingDate         time.Time
	Amount              float64
	Fee                 float64
	NetAmount           float64
	CustomerName        string
	CustomerPhoneMasked string
	Message             string
	PSPRef              string
	OrderID             string
	SettlementNumber    string
	PayoutAccount       string
	ScheduledPayoutDate time.Time
}

// vippsCol indexes a header row, normalizing the BOM and trimming whitespace.
type vippsCol struct {
	idx map[string]int
}

func newVippsCol(header []string) vippsCol {
	idx := make(map[string]int)
	for i, c := range header {
		idx[strings.TrimSpace(stripBOM(c))] = i
	}
	return vippsCol{idx: idx}
}

func (c vippsCol) get(record []string, name string) string {
	i, ok := c.idx[name]
	if !ok || i < 0 || i >= len(record) {
		return ""
	}
	return record[i]
}

// ParseVippsCSV parses a Vipps oppgjørsrapport export into VippsRows.
func ParseVippsCSV(reader io.Reader) ([]VippsRow, error) {
	r := csv.NewReader(reader)
	r.Comma = ','
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	cols := newVippsCol(header)

	var rows []VippsRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		typeRaw := strings.TrimSpace(cols.get(rec, "Type"))
		row := VippsRow{
			RowType:             classifyVippsType(typeRaw),
			MSN:                 strings.TrimSpace(cols.get(rec, "MSN/Vippsnummer")),
			Amount:              parseDotNumber(cols.get(rec, "Beløp")),
			Fee:                 parseDotNumber(cols.get(rec, "Gebyr")),
			NetAmount:           parseDotNumber(cols.get(rec, "Nettobeløp")),
			CustomerName:        strings.TrimSpace(cols.get(rec, "Kundens navn")),
			CustomerPhoneMasked: strings.TrimSpace(cols.get(rec, "Kundens telefonnummer")),
			Message:             strings.TrimSpace(cols.get(rec, "Melding")),
			PSPRef:              strings.TrimSpace(cols.get(rec, "PSP-referanse")),
			OrderID:             strings.TrimSpace(cols.get(rec, "Ordre-ID/Referanse")),
			SettlementNumber:    strings.TrimSpace(cols.get(rec, "Utbetalingsnummer")),
			PayoutAccount:       strings.TrimSpace(cols.get(rec, "Bankkonto for utbetaling")),
		}

		if t := strings.TrimSpace(cols.get(rec, "Tidspunkt")); t != "" {
			if parsed, err := time.Parse("2006-01-02 15:04:05", t); err == nil {
				row.TxAt = parsed
			}
		}
		if d := strings.TrimSpace(cols.get(rec, "Bokføringsdato")); d != "" {
			if parsed, err := time.Parse("2006-01-02", d); err == nil {
				row.BookingDate = parsed
			}
		}
		if d := strings.TrimSpace(cols.get(rec, "Planlagt utbetalingsdato")); d != "" {
			if parsed, err := time.Parse("2006-01-02", d); err == nil {
				row.ScheduledPayoutDate = parsed
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

func classifyVippsType(t string) VippsRowType {
	switch strings.ToLower(t) {
	case "belastning":
		return VippsRowBelastning
	case "gebyrer fratrukket":
		return VippsRowFee
	case "utbetaling planlagt":
		return VippsRowPayout
	default:
		return VippsRowOther
	}
}

// VippsRowHash computes the dedup hash for a Vipps row, scoped per club.
func VippsRowHash(clubID string, row VippsRow) string {
	parts := []string{
		clubID,
		row.TxAt.UTC().Format(time.RFC3339Nano),
		strconv.FormatFloat(row.Amount, 'f', 2, 64),
		row.OrderID,
		row.PSPRef,
		string(row.RowType),
		row.SettlementNumber,
	}
	h := sha256.Sum256([]byte(strings.ToLower(strings.Join(parts, "|"))))
	return hex.EncodeToString(h[:])
}

// VippsImportResult summarizes one Vipps import call.
type VippsImportResult struct {
	Imported   int
	SkippedDup int
}

// ImportVippsRows stores parsed Vipps rows with (club_id, row_hash) dedup.
func (s *Service) ImportVippsRows(ctx context.Context, clubID, importID string, rows []VippsRow) (VippsImportResult, error) {
	var res VippsImportResult

	for _, row := range rows {
		hash := VippsRowHash(clubID, row)

		var txAt, bookingDate, scheduledPayoutDate any
		if !row.TxAt.IsZero() {
			txAt = row.TxAt
		}
		if !row.BookingDate.IsZero() {
			bookingDate = row.BookingDate
		}
		if !row.ScheduledPayoutDate.IsZero() {
			scheduledPayoutDate = row.ScheduledPayoutDate
		}

		var rowID string
		err := s.db.QueryRow(ctx,
			`INSERT INTO vipps_import_rows
			   (vipps_import_id, club_id, row_hash, row_type, tx_at, booking_date,
			    amount, fee, net_amount, customer_name, customer_phone_masked, message,
			    psp_ref, order_id, settlement_number, payout_account, scheduled_payout_date, msn)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
			 ON CONFLICT (club_id, row_hash) DO NOTHING
			 RETURNING id`,
			importID, clubID, hash, string(row.RowType),
			txAt, bookingDate,
			row.Amount, row.Fee, row.NetAmount,
			row.CustomerName, row.CustomerPhoneMasked, row.Message,
			row.PSPRef, row.OrderID, row.SettlementNumber, row.PayoutAccount,
			scheduledPayoutDate, row.MSN,
		).Scan(&rowID)
		if err == pgx.ErrNoRows {
			res.SkippedDup++
			continue
		}
		if err != nil {
			return res, fmt.Errorf("inserting vipps row: %w", err)
		}
		res.Imported++
	}

	return res, nil
}

// parseDotNumber parses Vipps numbers (English form: "1300.00", "-29.75").
// Returns 0 on empty or unparseable.
func parseDotNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, " ", "")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
