package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// defaultFakturaDueDays is how long a faktura issued without a chosen due
// date gives the payer. It is the default of the faktura forms.
const defaultFakturaDueDays = 21

type reissueResponse struct {
	ID            string  `json:"id"`
	InvoiceNumber int     `json:"invoice_number"`
	KID           string  `json:"kid_number"`
	TotalAmount   float64 `json:"total_amount"`
	IssueDate     string  `json:"issue_date"`
	DueDate       string  `json:"due_date"`
	// OriginalInvoiceNumber is the credited faktura this one replaces.
	OriginalInvoiceNumber int `json:"original_invoice_number"`
	// SentAt is set once the faktura was emailed to the payer. It is null
	// when the payer has no address or the mail failed; the faktura is then
	// an ordinary unsent draft in the faktura list.
	SentAt    *string    `json:"sent_at"`
	GLBooking *glBooking `json:"gl_booking,omitempty"`
	// Warnings are problems that changed the outcome without failing it.
	Warnings []string `json:"warnings,omitempty"`
}

// HandleReissueAfterRefund is POST
// /admin/financials/invoices/{invoiceID}/reissue-after-refund: "Ny faktura"
// on a faktura that card refunds credited in full (ACC-8). Treasurer or
// admin with a fresh TOTP; audited invoice.reissued_after_refund.
//
// It issues a new faktura with the credited one's lines (net of exemptions)
// to the same payer, dated today and due in defaultFakturaDueDays, then
// sends and books it as any other faktura is (emailNewInvoice).
//   - 201 the new faktura
//   - 404 no such faktura
//   - 409 not_refunded (not credited in full by card refunds) or
//     already_reissued (a new faktura was issued before)
func (h *InvoiceHandler) HandleReissueAfterRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if _, err := uuid.Parse(invoiceID); err != nil {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if h.ledger == nil {
		Error(w, http.StatusServiceUnavailable, "accounting is not enabled")
		return
	}

	issued := time.Now()
	due := issued.AddDate(0, 0, defaultFakturaDueDays)
	res, err := h.ledger.ReissueAfterRefund(ctx, claims.ClubID, invoiceID, issued, due)
	var refusal *accounting.ReissueRefusal
	switch {
	case err == nil:
	case errors.As(err, &refusal):
		JSON(w, http.StatusConflict, map[string]string{"error": refusal.Message, "code": refusal.Code})
		return
	case errors.Is(err, accounting.ErrReissueNotFound):
		Error(w, http.StatusNotFound, "invoice not found")
		return
	default:
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("reissue after refund failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := reissueResponse{
		ID: res.ID, InvoiceNumber: res.Number, KID: res.KID, TotalAmount: res.Total,
		IssueDate: res.IssueDate.Format("2006-01-02"), DueDate: res.DueDate.Format("2006-01-02"),
		OriginalInvoiceNumber: res.OriginalNumber,
	}
	// The faktura exists from here on. A step after the commit that fails is
	// reported, not undone: the faktura stays a draft the treasurer can send.
	club, err := loadClubInvoiceFields(ctx, h.db, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", res.ID).Msg("reissue: loading club failed")
		out.Warnings = append(out.Warnings, "club_details_unavailable")
	} else if pdf, _, _, err := h.ensureInvoicePDF(ctx, claims.ClubID, res.ID, club); err != nil {
		h.log.Error().Err(err).Str("invoice_id", res.ID).Msg("reissue: rendering the PDF failed")
		out.Warnings = append(out.Warnings, "pdf_unavailable")
	} else {
		out.SentAt, out.GLBooking = h.emailNewInvoice(ctx, newInvoiceMail{
			clubID: claims.ClubID, actorID: claims.UserID, remoteAddr: r.RemoteAddr,
			invoiceID: res.ID, number: res.Number, kid: res.KID, total: res.Total, dueDate: res.DueDate,
			deliverTo: res.DeliverTo, deliverName: res.DisplayName,
			clubName: club.Name, bankAccount: club.BankAccount, locale: email.DetectLocale(r), pdf: pdf,
		})
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceReissuedAfterRefund, "invoice", invoiceID,
			map[string]any{
				"invoice_number":     res.OriginalNumber,
				"new_invoice_id":     res.ID,
				"new_invoice_number": res.Number,
				"total":              res.Total,
				"sent":               out.SentAt != nil,
			})
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", res.ID,
			map[string]any{"invoice_number": res.Number, "kid": res.KID, "total": res.Total,
				"reissue_of_invoice_number": res.OriginalNumber})
	}
	JSON(w, http.StatusCreated, out)
}
