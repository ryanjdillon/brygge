package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/qrposter"
)

// Admin management of harbor QR payment targets (BRY-6). A target ties a
// printable poster (label/location) to a price_item (unit price + category) and
// a public token; its QR encodes the /qr/{token} checkout page. All endpoints
// are club-scoped via config.ClubSlug (single-club deployment) and gated to
// board/harbor_master/admin at the router.

type harborTargetResp struct {
	ID          string  `json:"id"`
	Token       string  `json:"token"`
	Label       string  `json:"label"`
	Location    string  `json:"location"`
	Kind        string  `json:"kind"`
	PriceItemID string  `json:"price_item_id"`
	ItemName    string  `json:"item_name"`
	UnitAmount  float64 `json:"unit_amount"`
	Currency    string  `json:"currency"`
	UnitLabel   string  `json:"unit_label"`
	MinUnits    int     `json:"min_units"`
	MaxUnits    int     `json:"max_units"`
	FixedUnits  bool    `json:"fixed_units"`
	Archived    bool    `json:"archived"`
	PublicURL   string  `json:"public_url"`
}

func (h *HarborHandler) clubID(ctx context.Context) (string, error) {
	var id string
	err := h.db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug).Scan(&id)
	return id, err
}

func (h *HarborHandler) qrPublicURL(token string) string {
	base := h.config.FrontendURL
	if base == "" {
		base = "https://" + h.config.Domain
	}
	return strings.TrimRight(base, "/") + "/qr/" + token
}

// HandleListTargets returns the club's payment targets. Archived targets are
// included only when ?include_archived=true.
func (h *HarborHandler) HandleListTargets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("targets: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	where := "t.club_id = $1"
	if r.URL.Query().Get("include_archived") != "true" {
		where += " AND t.archived_at IS NULL"
	}
	rows, err := h.db.Query(ctx,
		`SELECT t.id, t.token, t.label, t.location, t.kind, t.price_item_id,
		        pi.name, pi.amount, pi.currency, t.unit_label,
		        t.min_units, t.max_units, t.fixed_units, t.archived_at IS NOT NULL
		   FROM harbor_payment_targets t
		   JOIN price_items pi ON pi.id = t.price_item_id
		  WHERE `+where+`
		  ORDER BY t.archived_at IS NOT NULL, t.created_at DESC`,
		clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("targets: list failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []harborTargetResp{}
	for rows.Next() {
		var t harborTargetResp
		if err := rows.Scan(&t.ID, &t.Token, &t.Label, &t.Location, &t.Kind, &t.PriceItemID,
			&t.ItemName, &t.UnitAmount, &t.Currency, &t.UnitLabel,
			&t.MinUnits, &t.MaxUnits, &t.FixedUnits, &t.Archived); err != nil {
			h.log.Error().Err(err).Msg("targets: scan failed")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		t.PublicURL = h.qrPublicURL(t.Token)
		out = append(out, t)
	}
	JSON(w, http.StatusOK, out)
}

type targetWriteRequest struct {
	Label       string `json:"label"`
	Location    string `json:"location"`
	Kind        string `json:"kind"`
	PriceItemID string `json:"price_item_id"`
	UnitLabel   string `json:"unit_label"`
	MinUnits    *int   `json:"min_units"`
	MaxUnits    *int   `json:"max_units"`
	FixedUnits  bool   `json:"fixed_units"`
}

// priceItemBelongsToClub reports whether the price_item exists for the club.
func (h *HarborHandler) priceItemBelongsToClub(ctx context.Context, clubID, priceItemID string) bool {
	var ok bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM price_items WHERE id = $1 AND club_id = $2)`,
		priceItemID, clubID,
	).Scan(&ok)
	return err == nil && ok
}

// HandleCreateTarget mints a new payment target + its public token.
func (h *HarborHandler) HandleCreateTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	var req targetWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		Error(w, http.StatusBadRequest, "label is required")
		return
	}
	if !h.priceItemBelongsToClub(ctx, clubID, req.PriceItemID) {
		Error(w, http.StatusBadRequest, "unknown price item")
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "guest_slip"
	}
	unitLabel := req.UnitLabel
	if unitLabel == "" {
		unitLabel = "natt"
	}
	minU, maxU := 1, 14
	if req.MinUnits != nil && *req.MinUnits >= 1 {
		minU = *req.MinUnits
	}
	if req.MaxUnits != nil && *req.MaxUnits >= minU {
		maxU = *req.MaxUnits
	}

	token := genCheckoutToken()
	var id string
	if err := h.db.QueryRow(ctx,
		`INSERT INTO harbor_payment_targets
		   (club_id, token, label, location, kind, price_item_id, unit_label, min_units, max_units, fixed_units)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		clubID, token, req.Label, req.Location, kind, req.PriceItemID, unitLabel, minU, maxU, req.FixedUnits,
	).Scan(&id); err != nil {
		h.log.Error().Err(err).Msg("targets: create failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusCreated, map[string]any{"id": id, "token": token, "public_url": h.qrPublicURL(token)})
}

// HandleUpdateTarget edits an existing target's presentation and pricing link.
func (h *HarborHandler) HandleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := chi.URLParam(r, "id")
	var req targetWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		Error(w, http.StatusBadRequest, "label is required")
		return
	}
	if !h.priceItemBelongsToClub(ctx, clubID, req.PriceItemID) {
		Error(w, http.StatusBadRequest, "unknown price item")
		return
	}
	minU, maxU := 1, 14
	if req.MinUnits != nil && *req.MinUnits >= 1 {
		minU = *req.MinUnits
	}
	if req.MaxUnits != nil && *req.MaxUnits >= minU {
		maxU = *req.MaxUnits
	}
	unitLabel := req.UnitLabel
	if unitLabel == "" {
		unitLabel = "natt"
	}
	tag, err := h.db.Exec(ctx,
		`UPDATE harbor_payment_targets
		    SET label=$1, location=$2, kind=$3, price_item_id=$4, unit_label=$5,
		        min_units=$6, max_units=$7, fixed_units=$8, updated_at=now()
		  WHERE id=$9 AND club_id=$10 AND archived_at IS NULL`,
		req.Label, req.Location, req.Kind, req.PriceItemID, unitLabel,
		minU, maxU, req.FixedUnits, id, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("targets: update failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "target not found")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"id": id})
}

// HandleArchiveTarget soft-deletes a target: it stops resolving publicly and
// drops out of selectors, but its history (paid orders) is untouched.
func (h *HarborHandler) HandleArchiveTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := chi.URLParam(r, "id")
	tag, err := h.db.Exec(ctx,
		`UPDATE harbor_payment_targets SET archived_at = now(), updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND archived_at IS NULL`,
		id, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("targets: archive failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "target not found")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"id": id, "archived": true})
}

// loadTargetForPoster loads the copy needed to render a QR/poster for a target.
func (h *HarborHandler) loadTargetForPoster(ctx context.Context, clubID, id string) (label, location, unitLabel, currency, token string, amount float64, fixed bool, ok bool) {
	err := h.db.QueryRow(ctx,
		`SELECT t.label, t.location, t.unit_label, pi.currency, t.token, pi.amount, t.fixed_units
		   FROM harbor_payment_targets t
		   JOIN price_items pi ON pi.id = t.price_item_id
		  WHERE t.id = $1 AND t.club_id = $2`,
		id, clubID,
	).Scan(&label, &location, &unitLabel, &currency, &token, &amount, &fixed)
	if err != nil {
		return "", "", "", "", "", 0, false, false
	}
	return label, location, unitLabel, currency, token, amount, fixed, true
}

// HandleTargetQR streams the target's QR code as a PNG (for admin preview).
func (h *HarborHandler) HandleTargetQR(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	var token string
	if err := h.db.QueryRow(ctx,
		`SELECT token FROM harbor_payment_targets WHERE id = $1 AND club_id = $2`,
		chi.URLParam(r, "id"), clubID,
	).Scan(&token); err != nil {
		Error(w, http.StatusNotFound, "target not found")
		return
	}
	png, err := qrposter.QR(h.qrPublicURL(token), 512)
	if err != nil {
		h.log.Error().Err(err).Msg("targets: qr render failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// HandleTargetPoster streams a print-ready A4 poster PDF.
func (h *HarborHandler) HandleTargetPoster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := h.clubID(ctx)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	label, location, unitLabel, _, token, amount, fixed, ok := h.loadTargetForPoster(ctx, clubID, chi.URLParam(r, "id"))
	if !ok {
		Error(w, http.StatusNotFound, "target not found")
		return
	}
	var clubName string
	_ = h.db.QueryRow(ctx, `SELECT name FROM clubs WHERE id = $1`, clubID).Scan(&clubName)

	priceLine := fmt.Sprintf("%s kr", trimAmount(amount))
	if !fixed {
		priceLine = fmt.Sprintf("%s kr / %s", trimAmount(amount), unitLabel)
	}
	pdf, err := qrposter.Poster(qrposter.PosterData{
		ClubName:  clubName,
		Label:     label,
		Location:  location,
		PriceLine: priceLine,
		Instructions: []string{
			"Skann QR-koden med mobilkameraet.",
			"Velg antall og betal med Vipps.",
		},
		URL: h.qrPublicURL(token),
	})
	if err != nil {
		h.log.Error().Err(err).Msg("targets: poster render failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="qr-%s.pdf"`, token[:8]))
	_, _ = w.Write(pdf)
}

// trimAmount formats a NOK amount without trailing ",00" but keeping øre when present.
func trimAmount(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return strings.Replace(fmt.Sprintf("%.2f", v), ".", ",", 1)
}
