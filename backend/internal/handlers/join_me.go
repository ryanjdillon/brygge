package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// GET /join/me (BRY-632) tells the signed-in user's "Fullfør innmeldinga"
// page where their join stands, without changing anything: the page must not
// issue a faktura just by being opened, which POST /join/bank-invoice would.

// States of a join as GET /join/me reports them.
const (
	joinStateAwaitingPayment = "awaiting_payment"
	joinStateBankInvoice     = "bank_invoice"
	joinStateMember          = "member"
	// joinStateNone is an applicant with nothing left to pay (the join
	// expired, or a payment is settling), or a user who is neither an
	// applicant nor a member.
	joinStateNone = "none"
)

type joinMeResponse struct {
	State string `json:"state"`
	// CheckoutURL is the /betal page of what is left to pay: the pending join
	// order, or the open join faktura's pay order.
	CheckoutURL string                   `json:"checkout_url,omitempty"`
	BankInvoice *joinBankInvoiceResponse `json:"bank_invoice,omitempty"`
}

// HandleJoinMe is GET /api/v1/join/me for a signed-in user, on
// middleware.ApplicantRoutes.
//   - member: the user holds the member role and not the applicant role
//   - bank_invoice: an owed join faktura, with its KID and pay link
//   - awaiting_payment: a pending join order, with its pay link
//   - none: anything else
func (h *InvoiceHandler) HandleJoinMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	resp, err := h.joinMe(ctx, claims.ClubID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Str("user_id", claims.UserID).Msg("join me failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, resp)
}

func (h *InvoiceHandler) joinMe(ctx context.Context, clubID, userID string) (joinMeResponse, error) {
	// Roles are read from the database, not the session: a payment settled
	// since sign-in has already made the applicant a member.
	var applicant, member bool
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(bool_or(role = 'applicant'), false), COALESCE(bool_or(role = 'member'), false)
		   FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	).Scan(&applicant, &member); err != nil {
		return joinMeResponse{}, fmt.Errorf("load roles: %w", err)
	}
	if !applicant {
		if member {
			return joinMeResponse{State: joinStateMember}, nil
		}
		return joinMeResponse{State: joinStateNone}, nil
	}

	var (
		inv    joinBankInvoiceResponse
		due    time.Time
		payTok *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.id, i.invoice_number, i.kid_number, i.total_amount::float8, i.due_date,
		        (SELECT o.token FROM order_sources s JOIN orders o ON o.id = s.order_id
		          WHERE s.invoice_id = i.id AND o.token IS NOT NULL
		          ORDER BY o.created_at DESC LIMIT 1)
		   FROM invoices i WHERE i.id = (`+openJoinInvoiceSQL+`)`,
		clubID, userID,
	).Scan(&inv.InvoiceID, &inv.InvoiceNumber, &inv.KID, &inv.Amount, &due, &payTok)
	switch {
	case err == nil:
		club, err := loadClubInvoiceFields(ctx, h.db, clubID)
		if err != nil {
			return joinMeResponse{}, fmt.Errorf("load club: %w", err)
		}
		inv.AccountNumber = club.BankAccount
		inv.Currency = paymentCurrency
		inv.DueDate = due.Format(time.DateOnly)
		resp := joinMeResponse{State: joinStateBankInvoice, BankInvoice: &inv}
		if payTok != nil {
			resp.CheckoutURL = checkoutURL(h.config, *payTok)
		}
		return resp, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return joinMeResponse{}, fmt.Errorf("load join faktura: %w", err)
	}

	var token string
	err = h.db.QueryRow(ctx,
		`SELECT token FROM orders
		  WHERE club_id = $1 AND user_id = $2 AND purpose = $3 AND status = 'pending' AND token IS NOT NULL
		  ORDER BY created_at DESC
		  LIMIT 1`,
		clubID, userID, membership.Purpose,
	).Scan(&token)
	switch {
	case err == nil:
		return joinMeResponse{State: joinStateAwaitingPayment, CheckoutURL: checkoutURL(h.config, token)}, nil
	case errors.Is(err, pgx.ErrNoRows):
		return joinMeResponse{State: joinStateNone}, nil
	default:
		return joinMeResponse{}, fmt.Errorf("load join order: %w", err)
	}
}
