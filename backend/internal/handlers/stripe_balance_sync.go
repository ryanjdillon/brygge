package handlers

import (
	"errors"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// stripeSyncResponse is the ingest result with the booking pass and the
// payout sweep that followed it.
type stripeSyncResponse struct {
	accounting.StripeSyncResult
	Booking *accounting.StripeBookResult        `json:"booking"`
	Payouts *accounting.StripePayoutSweepResult `json:"payouts"`
}

// HandleStripeSync is POST /accounting/stripe/sync: it pulls the club's
// Stripe balance transactions and payouts, then books the charges and fees
// that are ready and clears the payouts that are ready, as the calling user
// (accounting.TrySyncAndBookStripeBalance). A failed ingest is 502; a
// booking pass or payout sweep that fails after it is still 200, with the
// failure in booking.error or payouts.error, and audited.
// While another Stripe sync or booking for the club runs (the nightly job,
// a payout webhook, a bank sync's booking pass) it answers 409 rather than
// queueing behind it.
func (h *AccountingHandler) HandleStripeSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.stripe == nil {
		Error(w, http.StatusServiceUnavailable, "Stripe is not configured")
		return
	}

	res, booked, payouts, err := h.svc.TrySyncAndBookStripeBalance(ctx, h.stripe, claims.ClubID, claims.UserID)
	if errors.Is(err, accounting.ErrStripeSyncBusy) {
		Error(w, http.StatusConflict, "A Stripe sync is already running; try again shortly")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("stripe balance sync failed")
		Error(w, http.StatusBadGateway, "Stripe balance sync failed")
		return
	}
	if len(res.Errors) > 0 {
		h.log.Warn().Strs("errors", res.Errors).Msg("stripe balance sync: some rows failed")
	}
	if len(booked.Errors) > 0 {
		h.log.Warn().Strs("errors", booked.Errors).Msg("stripe booking: some rows failed")
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionAccountingStripeSynced, "stripe_balance", claims.ClubID,
			map[string]any{
				"fetched":              res.Fetched,
				"inserted":             res.Inserted,
				"needs_review":         res.NeedsReview,
				"payouts_seen":         res.PayoutsSeen,
				"payouts_linked":       res.PayoutsLinked,
				"members_linked":       res.MembersLinked,
				"errors":               len(res.Errors),
				"charges_booked":       booked.ChargesBooked,
				"fees_booked":          booked.FeesBooked,
				"booking_needs_review": booked.NeedsReview,
				"booking_deferred":     len(booked.Deferred),
				"booking_errors":       len(booked.Errors),
				"booking_failed":       booked.Error != "",
				"payouts_cleared":      len(payouts.Cleared),
				"payouts_waiting":      len(payouts.Waiting),
				"payouts_skipped":      len(payouts.Skipped),
				"payouts_failed":       len(payouts.Failed),
				"payout_sweep_failed":  payouts.Error != "",
			})
	}
	JSON(w, http.StatusOK, stripeSyncResponse{StripeSyncResult: res, Booking: booked, Payouts: payouts})
}

// HandleListStripeReview is GET /accounting/stripe/review: the club's
// Stripe balance transactions held for review, with their reason. Read
// only, so open to every accounting role like the other review lists.
func (h *AccountingHandler) HandleListStripeReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	items, err := h.svc.ListStripeReview(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("listing stripe review failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items})
}
