package handlers

import (
	"errors"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// HandleStripeSync is POST /accounting/stripe/sync: it pulls the club's
// Stripe balance transactions and payouts into the Stripe review queue. It
// books nothing. While another Stripe ingest for the club runs (the nightly
// job or a payout webhook) it answers 409 rather than queueing behind it.
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

	res, err := h.svc.TrySyncStripeBalance(ctx, h.stripe, claims.ClubID)
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

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionAccountingStripeSynced, "stripe_balance", claims.ClubID,
			map[string]any{
				"fetched":        res.Fetched,
				"inserted":       res.Inserted,
				"needs_review":   res.NeedsReview,
				"payouts_seen":   res.PayoutsSeen,
				"payouts_linked": res.PayoutsLinked,
				"members_linked": res.MembersLinked,
				"errors":         len(res.Errors),
			})
	}
	JSON(w, http.StatusOK, res)
}
