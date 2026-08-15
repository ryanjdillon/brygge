package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// vippsPaymentValidity is how long after the due date the emailed Vipps link
// stays payable (BRY-8: "due date + 14 days"). Surfaced in the email and used
// to decide whether a re-issue must mint a fresh payment.
const vippsPaymentValidity = 14 * 24 * time.Hour

// ensureVippsPayment returns a payable Vipps ePayment URL for the invoice,
// creating one if the instance has Vipps configured and no still-live link
// already exists. It is idempotent: a link that has not yet expired is reused
// rather than duplicated, so calling it from both invoice creation and reminder
// sending is safe.
//
// It is deliberately non-fatal: on any problem (Vipps unconfigured, API error,
// store failure) it returns an empty URL and the caller sends the faktura with
// the KID-only fallback. Money is never at stake here — the link is a
// convenience, and the invoice remains payable by bank transfer regardless.
func (h *InvoiceHandler) ensureVippsPayment(ctx context.Context, clubID, invoiceID string) (url string, expiresAt time.Time) {
	if h.vipps == nil {
		return "", time.Time{}
	}

	var (
		total         float64
		invoiceNumber int
		dueDate       time.Time
		existingURL   *string
		existingExp   *time.Time
	)
	err := h.db.QueryRow(ctx,
		`SELECT total_amount, invoice_number, due_date,
		        vipps_payment_url, vipps_payment_expires_at
		 FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&total, &invoiceNumber, &dueDate, &existingURL, &existingExp)
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("vipps: load invoice for payment failed")
		return "", time.Time{}
	}

	// Reuse a link that is still live.
	if existingURL != nil && *existingURL != "" && existingExp != nil && existingExp.After(time.Now()) {
		return *existingURL, *existingExp
	}

	ref := genVippsReference()
	res, err := h.vipps.CreatePayment(ctx, ref, vipps.CreatePaymentRequest{
		Amount:             vipps.Amount{Currency: "NOK", Value: int64(math.Round(total * 100))},
		Reference:          ref,
		ReturnURL:          h.vippsReturnURL(),
		PaymentDescription: fmt.Sprintf("Faktura #%d", invoiceNumber),
		UserFlow:           "WEB_REDIRECT",
	})
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("vipps: create payment failed")
		return "", time.Time{}
	}

	exp := dueDate.Add(vippsPaymentValidity)
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices
		 SET vipps_payment_reference = $1, vipps_payment_url = $2, vipps_payment_expires_at = $3
		 WHERE id = $4`,
		ref, res.RedirectURL, exp, invoiceID,
	); err != nil {
		// Payment exists at Vipps but we failed to persist the link. Log and
		// still return the URL so the outgoing email can use it; the webhook
		// can't match it back until stored, so surface this loudly.
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Str("vipps_reference", ref).
			Msg("vipps: created payment but failed to store reference — webhook match will miss")
	}
	return res.RedirectURL, exp
}

// vippsReturnURL is where Vipps sends the payer's browser after they complete
// (or abort) the payment. The frontend base is preferred; fall back to the
// public domain root.
func (h *InvoiceHandler) vippsReturnURL() string {
	if h.config.FrontendURL != "" {
		return h.config.FrontendURL
	}
	return "https://" + h.config.Domain
}

// genVippsReference returns a globally unique, Vipps-safe payment reference
// ([a-zA-Z0-9-], 8–50 chars). Random rather than derived from the invoice id so
// a re-issue after expiry can mint a fresh reference (Vipps rejects reuse).
func genVippsReference() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "inv-" + hex.EncodeToString(b[:])
}
