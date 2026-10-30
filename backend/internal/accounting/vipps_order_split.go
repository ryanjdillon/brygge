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
// account. Stripe charges book from their order through the same split
// (resolveOrderChargeSplitByOrderID), labelled as card payments.

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

// vippsSplitLabel prefixes the line descriptions of a Vipps order split.
const vippsSplitLabel = "Vipps innbetaling"

// buildOrderChargeSplit is the Vipps form of buildOrderChargeSplitAs.
func buildOrderChargeSplit(sources []orderChargeSource, lines []orderChargeLine) []VippsReconcileLine {
	return buildOrderChargeSplitAs(vippsSplitLabel, sources, lines)
}

// buildOrderChargeSplitAs turns an order's faktura sources + catalog lines
// into the credit side of its journal split: each faktura → receivables
// clearing (with the invoice back-link so confirm can record the payment),
// each catalog line → its category revenue account (fallback 3900 when the
// category is unmapped). label names the payment method in each line
// description. Pure — no DB, fully testable. The caller supplies the
// matching DR bank/clearing line.
func buildOrderChargeSplitAs(label string, sources []orderChargeSource, lines []orderChargeLine) []VippsReconcileLine {
	out := make([]VippsReconcileLine, 0, len(sources)+len(lines))
	for _, src := range sources {
		out = append(out, VippsReconcileLine{
			Kind:            "receivable",
			AccountCode:     receivablesAccountCode,
			Credit:          src.Amount,
			Description:     fmt.Sprintf("%s faktura #%d", label, src.InvoiceNumber),
			LinkedInvoiceID: src.InvoiceID,
			Resolved:        true,
		})
	}
	for _, l := range lines {
		code := RevenueAccountOrFallback(l.Category)
		desc := label
		if l.Name != "" {
			desc = label + ": " + l.Name
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
// amount/member classification cascade). It reads through q, so a booking
// that holds the fakturas locked resolves on its own transaction.
func resolveOrderChargeSplit(ctx context.Context, q dbtx, clubID, reference string) (lines []VippsReconcileLine, found bool, err error) {
	if reference == "" {
		return nil, false, nil
	}
	var orderID string
	err = q.QueryRow(ctx,
		`SELECT id FROM orders WHERE club_id = $1 AND vipps_reference = $2`,
		clubID, reference,
	).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load order by reference: %w", err)
	}
	lines, err = orderChargeSplit(ctx, q, orderID, vippsSplitLabel)
	if err != nil {
		return nil, false, err
	}
	return lines, true, nil
}

// resolveOrderChargeSplitByOrderID is resolveOrderChargeSplit for a charge
// that names its order by id (a Stripe charge, through its payment
// attempt). found=false when the club has no such order.
func resolveOrderChargeSplitByOrderID(ctx context.Context, q dbtx, clubID, orderID, label string) (lines []VippsReconcileLine, found bool, err error) {
	var exists bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1 AND club_id = $2)`,
		orderID, clubID,
	).Scan(&exists); err != nil {
		return nil, false, fmt.Errorf("load order: %w", err)
	}
	if !exists {
		return nil, false, nil
	}
	lines, err = orderChargeSplit(ctx, q, orderID, label)
	if err != nil {
		return nil, false, err
	}
	return lines, true, nil
}

// orderChargeSplit loads an order's fakturas and catalog lines and builds
// its credit split.
func orderChargeSplit(ctx context.Context, q dbtx, orderID, label string) ([]VippsReconcileLine, error) {
	sources, err := loadOrderChargeSources(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	catLines, err := loadOrderChargeLines(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	return buildOrderChargeSplitAs(label, sources, catLines), nil
}

func loadOrderChargeSources(ctx context.Context, q dbtx, orderID string) ([]orderChargeSource, error) {
	rows, err := q.Query(ctx,
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

func loadOrderChargeLines(ctx context.Context, q dbtx, orderID string) ([]orderChargeLine, error) {
	rows, err := q.Query(ctx,
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
