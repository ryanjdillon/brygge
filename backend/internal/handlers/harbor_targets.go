package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	ShortCode   string  `json:"short_code"`
	ShortURL    string  `json:"short_url"`
}

func (h *HarborHandler) clubID(ctx context.Context) (string, error) {
	var id string
	err := h.db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug).Scan(&id)
	return id, err
}

func (h *HarborHandler) publicBaseURL() string {
	base := h.config.FrontendURL
	if base == "" {
		base = "https://" + h.config.Domain
	}
	return strings.TrimRight(base, "/")
}

func (h *HarborHandler) qrPublicURL(token string) string {
	return h.publicBaseURL() + "/qr/" + token
}

// shortPublicURL is the typed fallback printed under the QR; it resolves via
// HandleShortCodeRedirect.
func (h *HarborHandler) shortPublicURL(code string) string {
	return h.publicBaseURL() + "/q/" + code
}

// Short codes are typed by hand from a printed card, so the generated ones
// leave out characters that read alike (0/o, 1/l/i). Admin-chosen codes may
// use any of [a-z0-9-]; the DB CHECK enforces the same pattern.
const (
	shortCodeAlphabet  = "abcdefghjkmnpqrstuvwxyz23456789"
	shortCodeGenLength = 5
	// shortCodeGenAttempts bounds the retry on a clash; at 31^5 codes a
	// club would need millions of targets before this is reached.
	shortCodeGenAttempts = 8

	shortCodeInvalidCode = "short_code_invalid"
	shortCodeTakenCode   = "short_code_taken"
)

var shortCodePattern = regexp.MustCompile(`^[a-z0-9-]{2,24}$`)

func genShortCode() (string, error) {
	size := big.NewInt(int64(len(shortCodeAlphabet)))
	b := make([]byte, shortCodeGenLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", err
		}
		b[i] = shortCodeAlphabet[n.Int64()]
	}
	return string(b), nil
}

// normaliseShortCode trims and lowercases an admin-entered code and reports
// whether it is a valid code.
func normaliseShortCode(raw string) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(raw))
	return code, shortCodePattern.MatchString(code)
}

func shortCodeError(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, map[string]any{"error": msg, "code": code})
}

// isShortCodeClash reports a unique violation on the per-club short code
// index, as opposed to any other unique constraint on the table.
func isShortCodeClash(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "idx_harbor_targets_club_short_code"
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
		        t.min_units, t.max_units, t.fixed_units, t.archived_at IS NOT NULL,
		        t.short_code
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
			&t.MinUnits, &t.MaxUnits, &t.FixedUnits, &t.Archived, &t.ShortCode); err != nil {
			h.log.Error().Err(err).Msg("targets: scan failed")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		t.PublicURL = h.qrPublicURL(t.Token)
		t.ShortURL = h.shortPublicURL(t.ShortCode)
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
	// ShortCode is optional: blank on create gets a generated code, and
	// blank or absent on update keeps the current one.
	ShortCode *string `json:"short_code"`
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

	custom := ""
	if req.ShortCode != nil && strings.TrimSpace(*req.ShortCode) != "" {
		code, ok := normaliseShortCode(*req.ShortCode)
		if !ok {
			shortCodeError(w, http.StatusBadRequest, shortCodeInvalidCode, "invalid short code")
			return
		}
		custom = code
	}

	token := genCheckoutToken()
	id, shortCode, err := insertWithShortCode(custom, func(code string) (string, error) {
		var id string
		err := h.db.QueryRow(ctx,
			`INSERT INTO harbor_payment_targets
			   (club_id, token, label, location, kind, price_item_id, unit_label, min_units, max_units, fixed_units, short_code)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			clubID, token, req.Label, req.Location, kind, req.PriceItemID, unitLabel, minU, maxU, req.FixedUnits, code,
		).Scan(&id)
		return id, err
	})
	if isShortCodeClash(err) {
		shortCodeError(w, http.StatusConflict, shortCodeTakenCode, "short code is already in use")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("targets: create failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"token":      token,
		"public_url": h.qrPublicURL(token),
		"short_code": shortCode,
		"short_url":  h.shortPublicURL(shortCode),
	})
}

// insertWithShortCode runs insert with the admin's chosen short code, or with a
// generated one when custom is blank. A chosen code is tried once and a clash
// is the admin's to resolve; a generated one is retried with a fresh code.
func insertWithShortCode(custom string, insert func(code string) (string, error)) (id, code string, err error) {
	if custom != "" {
		id, err = insert(custom)
		return id, custom, err
	}
	for attempt := 0; attempt < shortCodeGenAttempts; attempt++ {
		if code, err = genShortCode(); err != nil {
			return "", "", fmt.Errorf("generate short code: %w", err)
		}
		if id, err = insert(code); !isShortCodeClash(err) {
			return id, code, err
		}
	}
	// Wrapped so the caller doesn't report a code the admin never typed as
	// "already in use".
	return "", "", fmt.Errorf("no free short code after %d attempts: %v", shortCodeGenAttempts, err)
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
	// NULL keeps the stored code (COALESCE below).
	var shortCode *string
	if req.ShortCode != nil && strings.TrimSpace(*req.ShortCode) != "" {
		code, ok := normaliseShortCode(*req.ShortCode)
		if !ok {
			shortCodeError(w, http.StatusBadRequest, shortCodeInvalidCode, "invalid short code")
			return
		}
		shortCode = &code
	}
	tag, err := h.db.Exec(ctx,
		`UPDATE harbor_payment_targets
		    SET label=$1, location=$2, kind=$3, price_item_id=$4, unit_label=$5,
		        min_units=$6, max_units=$7, fixed_units=$8,
		        short_code=COALESCE($11, short_code), updated_at=now()
		  WHERE id=$9 AND club_id=$10 AND archived_at IS NULL`,
		req.Label, req.Location, req.Kind, req.PriceItemID, unitLabel,
		minU, maxU, req.FixedUnits, id, clubID, shortCode,
	)
	if isShortCodeClash(err) {
		shortCodeError(w, http.StatusConflict, shortCodeTakenCode, "short code is already in use")
		return
	}
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
func (h *HarborHandler) loadTargetForPoster(ctx context.Context, clubID, id string) (label, location, unitLabel, currency, token, shortCode string, amount float64, fixed bool, ok bool) {
	err := h.db.QueryRow(ctx,
		`SELECT t.label, t.location, t.unit_label, pi.currency, t.token, t.short_code, pi.amount, t.fixed_units
		   FROM harbor_payment_targets t
		   JOIN price_items pi ON pi.id = t.price_item_id
		  WHERE t.id = $1 AND t.club_id = $2`,
		id, clubID,
	).Scan(&label, &location, &unitLabel, &currency, &token, &shortCode, &amount, &fixed)
	if err != nil {
		return "", "", "", "", "", "", 0, false, false
	}
	return label, location, unitLabel, currency, token, shortCode, amount, fixed, true
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
	label, location, unitLabel, _, token, shortCode, amount, fixed, ok := h.loadTargetForPoster(ctx, clubID, chi.URLParam(r, "id"))
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
		URL:      h.qrPublicURL(token),
		ShortURL: strings.TrimPrefix(strings.TrimPrefix(h.shortPublicURL(shortCode), "https://"), "http://"),
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

// HandleShortCodeRedirect resolves the typed short address printed under a QR
// to the target's checkout page. It is served by the Go router ahead of the
// SPA fallback so it works without JavaScript. Anything that would not load
// on /qr/{token} (unknown, archived, inactive price item) goes to a token the
// checkout page reports as not found, so a guest never learns which.
func (h *HarborHandler) HandleShortCodeRedirect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "code")))
	dest := "/qr/not-found"
	if shortCodePattern.MatchString(code) {
		var token string
		err := h.db.QueryRow(ctx,
			`SELECT t.token
			   FROM harbor_payment_targets t
			   JOIN clubs c ON c.id = t.club_id
			   JOIN price_items pi ON pi.id = t.price_item_id
			  WHERE c.slug = $1 AND t.short_code = $2
			    AND t.archived_at IS NULL AND pi.is_active`,
			h.config.ClubSlug, code,
		).Scan(&token)
		switch {
		case err == nil:
			dest = "/qr/" + token
		case !errors.Is(err, pgx.ErrNoRows):
			h.log.Error().Err(err).Msg("targets: short code lookup failed")
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, dest, http.StatusFound)
}

// trimAmount formats a NOK amount without trailing ",00" but keeping øre when present.
func trimAmount(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return strings.Replace(fmt.Sprintf("%.2f", v), ".", ",", 1)
}
