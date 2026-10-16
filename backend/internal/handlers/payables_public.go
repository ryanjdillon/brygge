package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Per-member payables link (BRY-275). A durable, non-expiring token identifies a
// member so they can see and pay their own open invoices on a public page
// (/betal/bruker/{token}) without logging in — for members who don't cope with
// magic-link auth. It extends the per-invoice pay link (BRY-274): the payables
// page lists each open invoice with that invoice's own durable checkout token,
// and paying one reuses the existing /betal/{token} mint-on-click flow, so no
// new payment or webhook logic is introduced.

// payableInvoice mirrors the member-portal MemberInvoice shape (so the frontend
// reuses the same invoice-list presentation) plus pay_token — the invoice's
// durable checkout token used to start its Vipps payment.
type payableInvoice struct {
	ID              string  `json:"id"`
	InvoiceNumber   int     `json:"invoice_number"`
	KID             string  `json:"kid_number"`
	TotalAmount     float64 `json:"total_amount"`
	IssueDate       string  `json:"issue_date"`
	DueDate         string  `json:"due_date"`
	SentAt          *string `json:"sent_at,omitempty"`
	Paid            bool    `json:"paid"`
	PaymentReceived bool    `json:"payment_received"`
	PriceItemName   string  `json:"price_item_name"`
	Description     string  `json:"description"`
	HasPDF          bool    `json:"has_pdf"`
	PayToken        string  `json:"pay_token"`
}

type payablesResponse struct {
	ClubName   string `json:"club_name"`
	MemberName string `json:"member_name"`
	// BankAccount is the club's default faktura account, shown so a member can
	// pay by bank transfer — needed for invoices over the club's Vipps cap,
	// which carry an empty pay_token (BRY-283).
	BankAccount string           `json:"bank_account"`
	Invoices    []payableInvoice `json:"invoices"`
}

// ensureUserPayablesToken returns the member's durable payables-link token,
// minting and persisting one on first use (create=true) and reusing it forever
// after so a member's personal link is stable. Returns "" on error or when not
// found with create=false.
func (h *InvoiceHandler) ensureUserPayablesToken(ctx context.Context, clubID, userID string, create bool) string {
	var tok *string
	err := h.db.QueryRow(ctx,
		`SELECT payables_token FROM users WHERE id = $1 AND club_id = $2`,
		userID, clubID,
	).Scan(&tok)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("payables: load token failed")
		return ""
	}
	if tok != nil && *tok != "" {
		return *tok
	}
	if !create {
		return ""
	}

	token := genCheckoutToken()
	if _, err := h.db.Exec(ctx,
		`UPDATE users SET payables_token = $1, updated_at = now() WHERE id = $2 AND club_id = $3`,
		token, userID, clubID,
	); err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("payables: persist token failed")
		return ""
	}
	return token
}

// userPayablesURL builds the public payables page link for a member token.
func (h *InvoiceHandler) userPayablesURL(token string) string {
	if token == "" {
		return ""
	}
	return h.frontendBase() + "/betal/bruker/" + token
}

// HandleGetUserPayLink returns the durable per-member payables link so staff can
// hand a member their personal "pay my fakturas" link (SMS, chat, read it out).
// It mirrors HandleGetInvoicePayLink: no fresh-TOTP gate (a benign read that only
// ensures the token exists), and 409 "vipps_unavailable" when Vipps is off so the
// UI can hide the affordance gracefully.
func (h *InvoiceHandler) HandleGetUserPayLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	userID := chi.URLParam(r, "userID")
	if userID == "" {
		Error(w, http.StatusBadRequest, "user ID is required")
		return
	}
	if h.vipps == nil {
		Error(w, http.StatusConflict, "vipps_unavailable")
		return
	}

	// Club-scope the member before minting a token for them.
	var exists bool
	err := h.db.QueryRow(ctx,
		`SELECT true FROM users WHERE id = $1 AND club_id = $2`,
		userID, claims.ClubID,
	).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("user pay-link: load user failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	url := h.userPayablesURL(h.ensureUserPayablesToken(ctx, claims.ClubID, userID, true))
	if url == "" {
		Error(w, http.StatusConflict, "vipps_unavailable")
		return
	}
	JSON(w, http.StatusOK, invoicePayLinkResponse{PayURL: url})
}

// HandleGetPayables is the public GET /api/v1/betal/bruker/{token} endpoint: it
// resolves the member from their durable token and returns their open, unpaid
// invoices (each with its own durable pay token) for the payables page. No auth:
// the token is the capability. Already-paid and vipps-received invoices are
// excluded so the member is never prompted to pay what is already settled or
// pending settlement (BRY-276).
func (h *InvoiceHandler) HandleGetPayables(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	if token == "" {
		Error(w, http.StatusBadRequest, "token is required")
		return
	}

	var (
		userID, clubID, memberName, clubName string
	)
	err := h.db.QueryRow(ctx,
		`SELECT u.id, u.club_id, u.full_name, c.name
		   FROM users u
		   JOIN clubs c ON c.id = u.club_id
		  WHERE u.payables_token = $1`,
		token,
	).Scan(&userID, &clubID, &memberName, &clubName)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "payables link not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("payables: resolve token failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.kid_number, i.total_amount,
		        i.issue_date, i.due_date, i.sent_at,
		        COALESCE(pi.name, ''),
		        COALESCE((SELECT description FROM invoice_lines WHERE invoice_id = i.id LIMIT 1), '')
		   FROM invoices i
		   LEFT JOIN price_items pi ON pi.id = i.price_item_id
		  WHERE i.user_id = $1 AND i.club_id = $2
		    AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)
		    AND NOT `+finance.SettledForMemberSQL("i")+`
		  ORDER BY i.due_date ASC, i.issue_date DESC`,
		userID, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("payables: list invoices failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	resp := payablesResponse{ClubName: clubName, MemberName: memberName, Invoices: []payableInvoice{}}
	if club, err := loadClubInvoiceFields(ctx, h.db, clubID); err == nil {
		resp.BankAccount = club.BankAccount
	}
	for rows.Next() {
		var (
			inv        payableInvoice
			issue, due time.Time
			sentAt     *time.Time
		)
		if err := rows.Scan(&inv.ID, &inv.InvoiceNumber, &inv.KID, &inv.TotalAmount,
			&issue, &due, &sentAt, &inv.PriceItemName, &inv.Description); err != nil {
			h.log.Error().Err(err).Msg("payables: scan invoice row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		inv.IssueDate = issue.Format("2006-01-02")
		inv.DueDate = due.Format("2006-01-02")
		if sentAt != nil {
			s := sentAt.Format(time.RFC3339)
			inv.SentAt = &s
		}
		resp.Invoices = append(resp.Invoices, inv)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("payables: iterate invoice rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Mint each invoice's durable checkout token so the page's "Pay" action can
	// reuse the existing /betal/{token} redirect. Done after the rows are read so
	// the per-invoice transactions don't run while this query's rows are open.
	for i := range resp.Invoices {
		resp.Invoices[i].PayToken = h.ensureFakturaOrderToken(ctx, clubID, resp.Invoices[i].ID, true)
	}

	JSON(w, http.StatusOK, resp)
}
