package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Per-line GL split of a settled Vipps charge (BRY-264). When a settled charge
// carries our merchant reference (chk-…, delivered by the Report API), we can
// book it exactly instead of guessing by amount/name: each faktura the order
// settles credits the receivables-clearing account (and back-links the invoice
// payment), and each ad-hoc / QR catalog line credits its category's revenue
// account. This is the reusable core; wiring it into the reconcile posting rides
// on the BRY-256 ePayment-primary cutover.

// orderChargeSource is one faktura settled by a checkout order.
type orderChargeSource struct {
	InvoiceID     string
	InvoiceNumber int
	Amount        float64
}

// orderChargeLine is one ad-hoc / QR catalog line on a checkout order.
type orderChargeLine struct {
	Name     string
	Category string
	Total    float64
}

// buildOrderChargeSplit turns an order's faktura sources + catalog lines into
// the credit side of its journal split: each faktura → receivables clearing
// (with the invoice back-link so confirm can record the payment), each catalog
// line → its category revenue account (fallback 3900 when the category is
// unmapped). Pure — no DB, fully testable. The caller supplies the matching DR
// bank/clearing line.
func buildOrderChargeSplit(sources []orderChargeSource, lines []orderChargeLine) []VippsReconcileLine {
	out := make([]VippsReconcileLine, 0, len(sources)+len(lines))
	for _, src := range sources {
		out = append(out, VippsReconcileLine{
			Kind:            "receivable",
			AccountCode:     receivablesAccountCode,
			Credit:          src.Amount,
			Description:     fmt.Sprintf("Vipps innbetaling faktura #%d", src.InvoiceNumber),
			LinkedInvoiceID: src.InvoiceID,
			Resolved:        true,
		})
	}
	for _, l := range lines {
		code, ok := vippsCategoryAccountMap[l.Category]
		if !ok {
			code = vippsFallbackRevenueCode
		}
		desc := "Vipps innbetaling"
		if l.Name != "" {
			desc = "Vipps innbetaling: " + l.Name
		}
		out = append(out, VippsReconcileLine{
			Kind:        "revenue",
			AccountCode: code,
			Credit:      l.Total,
			Description: desc,
			Resolved:    true,
		})
	}
	return out
}

// resolveOrderChargeSplit finds the checkout order behind a settled charge's
// merchant reference (chk-…) and returns its per-line credit split. found=false
// when no order matches the reference (the caller falls back to the existing
// amount/member classification cascade).
func (s *Service) resolveOrderChargeSplit(ctx context.Context, clubID, reference string) (lines []VippsReconcileLine, found bool, err error) {
	if reference == "" {
		return nil, false, nil
	}
	var orderID string
	err = s.db.QueryRow(ctx,
		`SELECT id FROM orders WHERE club_id = $1 AND vipps_reference = $2`,
		clubID, reference,
	).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load order by reference: %w", err)
	}

	sources, err := s.loadOrderChargeSources(ctx, orderID)
	if err != nil {
		return nil, false, err
	}
	catLines, err := s.loadOrderChargeLines(ctx, orderID)
	if err != nil {
		return nil, false, err
	}
	return buildOrderChargeSplit(sources, catLines), true, nil
}

func (s *Service) loadOrderChargeSources(ctx context.Context, orderID string) ([]orderChargeSource, error) {
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount
		   FROM order_sources os
		   JOIN invoices i ON i.id = os.invoice_id
		  WHERE os.order_id = $1
		  ORDER BY i.invoice_number`,
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("load order sources: %w", err)
	}
	defer rows.Close()
	var out []orderChargeSource
	for rows.Next() {
		var src orderChargeSource
		if err := rows.Scan(&src.InvoiceID, &src.InvoiceNumber, &src.Amount); err != nil {
			return nil, fmt.Errorf("scan order source: %w", err)
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *Service) loadOrderChargeLines(ctx context.Context, orderID string) ([]orderChargeLine, error) {
	rows, err := s.db.Query(ctx,
		`SELECT ol.name, COALESCE(pi.category, ''), ol.total_price
		   FROM order_lines ol
		   LEFT JOIN price_items pi ON pi.id = ol.price_item_id
		  WHERE ol.order_id = $1`,
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("load order lines: %w", err)
	}
	defer rows.Close()
	var out []orderChargeLine
	for rows.Next() {
		var l orderChargeLine
		if err := rows.Scan(&l.Name, &l.Category, &l.Total); err != nil {
			return nil, fmt.Errorf("scan order line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
