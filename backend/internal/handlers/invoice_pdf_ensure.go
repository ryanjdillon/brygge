package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// ensureInvoicePDF returns a faktura's stored PDF, rendering and storing it
// first when there is none. The delivery of a refund's credit note and "Ny
// faktura" after a refund both use it.
func (h *InvoiceHandler) ensureInvoicePDF(ctx context.Context, clubID, invoiceID string, club *clubInvoiceFields) ([]byte, int, float64, error) {
	var s3Key string
	var stored []byte
	var number int
	var total float64
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(s3_key, ''), pdf_data, invoice_number, total_amount FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&s3Key, &stored, &number, &total)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, 0, errInvoiceNotFound
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("loading document: %w", err)
	}
	if pdf, _ := h.fetchInvoicePDFBytes(ctx, s3Key, stored); pdf != nil {
		return pdf, number, total, nil
	}
	st, err := h.loadStoredInvoicePDF(ctx, clubID, invoiceID, club)
	if err != nil {
		return nil, 0, 0, err
	}
	pdf, err := finance.GeneratePDF(st.inv)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("rendering document: %w", err)
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET pdf_data = $3 WHERE id = $1 AND club_id = $2`, invoiceID, clubID, pdf); err != nil {
		return nil, 0, 0, fmt.Errorf("storing document PDF: %w", err)
	}
	h.uploadInvoicePDFToS3(ctx, invoiceID, clubID, pdf)
	return pdf, number, math.Round(total*100) / 100, nil
}
