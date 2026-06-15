package handlers

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

const importSourceUni24 = "uni24"

type uni24ImportRow struct {
	Row        int    `json:"row"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

// HandleImportUni24Invoices accepts a Uni24.no fakturajournal CSV export and
// creates historical invoice records. Columns (in any order):
//
//	fakturanr, kunde_nr, kunde_navn, dato, forfall, status, nettosum
//
// Query parameters:
//
//	date_from  YYYY-MM-DD  inclusive lower bound on dato (issue date)
//	date_to    YYYY-MM-DD  inclusive upper bound on dato (issue date)
func (h *InvoiceHandler) HandleImportUni24Invoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	dateFrom, dateTo, ok := parseDateRange(w, r)
	if !ok {
		return
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, `missing "file" field`)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		Error(w, http.StatusBadRequest, "empty CSV or unreadable header")
		return
	}
	idx := map[string]int{}
	for i, col := range header {
		idx[strings.ToLower(strings.TrimSpace(col))] = i
	}
	required := []string{"fakturanr", "kunde_navn", "dato", "forfall", "status", "nettosum"}
	for _, col := range required {
		if _, ok := idx[col]; !ok {
			Error(w, http.StatusBadRequest, fmt.Sprintf("missing required column %q", col))
			return
		}
	}

	get := func(record []string, col string) string {
		i, ok := idx[col]
		if !ok || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}

	parseNODate := func(s string) (time.Time, error) {
		return time.Parse("02.01.2006", s)
	}

	parseAmount := func(s string) (float64, error) {
		s = strings.ReplaceAll(s, " ", "")
		s = strings.ReplaceAll(s, ",", ".")
		return strconv.ParseFloat(s, 64)
	}

	results := []uni24ImportRow{}
	imported := 0
	skipped := 0
	rowNum := 1

	for {
		rowNum++
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			results = append(results, uni24ImportRow{
				Row: rowNum, Status: "error",
				Error: fmt.Sprintf("parse: %v", err),
			})
			continue
		}

		externalID := get(record, "fakturanr")
		custName := get(record, "kunde_navn")
		datoStr := get(record, "dato")
		forfallStr := get(record, "forfall")
		statusStr := get(record, "status")
		nettosumStr := get(record, "nettosum")

		issueDate, err := parseNODate(datoStr)
		if err != nil {
			results = append(results, uni24ImportRow{
				Row: rowNum, ExternalID: externalID, Name: custName,
				Status: "error", Error: fmt.Sprintf("invalid dato %q", datoStr),
			})
			continue
		}

		if !dateFrom.IsZero() && issueDate.Before(dateFrom) {
			skipped++
			continue
		}
		if !dateTo.IsZero() && issueDate.After(dateTo) {
			skipped++
			continue
		}

		dueDate, err := parseNODate(forfallStr)
		if err != nil {
			results = append(results, uni24ImportRow{
				Row: rowNum, ExternalID: externalID, Name: custName,
				Status: "error", Error: fmt.Sprintf("invalid forfall %q", forfallStr),
			})
			continue
		}

		amount, err := parseAmount(nettosumStr)
		if err != nil {
			results = append(results, uni24ImportRow{
				Row: rowNum, ExternalID: externalID, Name: custName,
				Status: "error", Error: fmt.Sprintf("invalid nettosum %q", nettosumStr),
			})
			continue
		}

		invoiceStatus := "open"
		if strings.EqualFold(statusStr, "Kreditert") {
			invoiceStatus = "voided"
		}

		// Resolve optional user link by full name (case-insensitive).
		var userID *string
		if custName != "" {
			var uid string
			err := h.db.QueryRow(ctx,
				`SELECT id FROM users
				  WHERE club_id = $1
				    AND LOWER(full_name) = LOWER($2)
				  LIMIT 1`,
				claims.ClubID, custName,
			).Scan(&uid)
			if err == nil {
				userID = &uid
			}
		}

		// Allocate the next invoice number for this club.
		var invoiceSeq int
		err = h.db.QueryRow(ctx,
			`SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1`,
			claims.ClubID,
		).Scan(&invoiceSeq)
		if err != nil {
			results = append(results, uni24ImportRow{
				Row: rowNum, ExternalID: externalID, Name: custName,
				Status: "error", Error: "could not allocate invoice number",
			})
			continue
		}

		// For unmatched recipients, store their name in recipient_org_name
		// so the invoice list COALESCE falls back to it correctly.
		var invoiceID string
		err = h.db.QueryRow(ctx,
			`INSERT INTO invoices
			    (club_id, user_id, invoice_number, kid_number,
			     issue_date, due_date, total_amount,
			     status, import_source, external_id,
			     recipient_kind, recipient_org_name)
			 VALUES
			    ($1, $2, $3, NULL,
			     $4, $5, $6,
			     $7, $8, $9,
			     'private', $10)
			 ON CONFLICT (club_id, import_source, external_id)
			     WHERE import_source IS NOT NULL AND external_id IS NOT NULL
			     DO NOTHING
			 RETURNING id`,
			claims.ClubID, userID, invoiceSeq,
			issueDate, dueDate, amount,
			invoiceStatus, importSourceUni24, externalID,
			custName,
		).Scan(&invoiceID)

		row := uni24ImportRow{Row: rowNum, ExternalID: externalID, Name: custName}
		switch {
		case err == nil && invoiceID != "":
			// Insert succeeded — add a single line item.
			_, lineErr := h.db.Exec(ctx,
				`INSERT INTO invoice_lines
				    (invoice_id, description, quantity, unit_price, line_total)
				 VALUES ($1, $2, 1, $3, $3)`,
				invoiceID, fmt.Sprintf("Faktura %s (Uni24)", externalID), amount,
			)
			if lineErr != nil {
				h.log.Warn().Err(lineErr).Str("invoice_id", invoiceID).Msg("could not insert import line")
			}
			row.Status = "imported"
			imported++
		case err == nil:
			// ON CONFLICT DO NOTHING → no row returned → already exists.
			row.Status = "skipped"
			skipped++
		default:
			h.log.Error().Err(err).Int("row", rowNum).Str("external_id", externalID).
				Msg("uni24 import row failed")
			row.Status = "error"
			row.Error = "internal error"
		}
		results = append(results, row)
	}

	h.log.Info().
		Str("actor", claims.UserID).
		Int("imported", imported).
		Int("skipped", skipped).
		Int("total", len(results)+skipped).
		Msg("uni24 invoice import complete")

	JSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"total":    len(results) + skipped,
		"rows":     results,
	})
}

func parseDateRange(w http.ResponseWriter, r *http.Request) (dateFrom, dateTo time.Time, ok bool) {
	q := r.URL.Query()
	fromStr := strings.TrimSpace(q.Get("date_from"))
	toStr := strings.TrimSpace(q.Get("date_to"))

	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "date_from must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
		dateFrom = t
	}
	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "date_to must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
		dateTo = t
	}
	return dateFrom, dateTo, true
}
