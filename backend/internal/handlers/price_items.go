package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type PriceItemsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewPriceItemsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *PriceItemsHandler {
	return &PriceItemsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "price_items").Logger(),
	}
}

type priceItem struct {
	ID                    string          `json:"id"`
	Category              string          `json:"category"`
	Name                  string          `json:"name"`
	Description           string          `json:"description"`
	Amount                float64         `json:"amount"`
	Currency              string          `json:"currency"`
	Unit                  string          `json:"unit"`
	InstallmentsAllowed   bool            `json:"installments_allowed"`
	MaxInstallments       int             `json:"max_installments"`
	Metadata              json.RawMessage `json:"metadata"`
	SortOrder             int             `json:"sort_order"`
	IsActive              bool            `json:"is_active"`
	PricingKind           string          `json:"pricing_kind"`
	TierDimension         *string         `json:"tier_dimension"`
	ShowInBatch           bool            `json:"show_in_batch"`
	ShowInSingle          bool            `json:"show_in_single"`
	RequiresBoatSelection bool            `json:"requires_boat_selection"`
	Audience              string          `json:"audience"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

const priceItemColumns = `id, category, name, description, amount, currency, unit,
	installments_allowed, max_installments, metadata,
	sort_order, is_active,
	pricing_kind, tier_dimension, show_in_batch, show_in_single, requires_boat_selection,
	audience,
	created_at, updated_at`

var validCategories = map[string]bool{
	"membership":        true,
	"harbor_membership": true,
	"slip_fee":          true,
	"electricity":       true,
	"seasonal_rental":   true,
	"guest":             true,
	"motorhome":         true,
	"room_hire":         true,
	"service":           true,
	"other":             true,
}

var validAudiences = map[string]bool{
	"all":        true,
	"member":     true,
	"non_member": true,
}

// HandleListPublic returns active price items grouped by category (no auth required).
func (h *PriceItemsHandler) HandleListPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT `+priceItemColumns+`
		 FROM price_items
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $1)
		   AND is_active = true
		 ORDER BY sort_order, category, name`,
		h.config.ClubSlug,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query price items")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items, err := scanPriceItems(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan price items")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"items": items})
}

// HandleListAdmin returns all price items for the club (active and inactive).
func (h *PriceItemsHandler) HandleListAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT `+priceItemColumns+`
		 FROM price_items
		 WHERE club_id = $1
		 ORDER BY sort_order, category, name`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query price items")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items, err := scanPriceItems(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan price items")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"items": items})
}

type createPriceItemRequest struct {
	Category              string          `json:"category"`
	Name                  string          `json:"name"`
	Description           string          `json:"description"`
	Amount                float64         `json:"amount"`
	Currency              string          `json:"currency"`
	Unit                  string          `json:"unit"`
	InstallmentsAllowed   bool            `json:"installments_allowed"`
	MaxInstallments       int             `json:"max_installments"`
	Metadata              json.RawMessage `json:"metadata"`
	SortOrder             int             `json:"sort_order"`
	IsActive              *bool           `json:"is_active"`
	PricingKind           string          `json:"pricing_kind"`
	TierDimension         *string         `json:"tier_dimension"`
	ShowInBatch           *bool           `json:"show_in_batch"`
	ShowInSingle          *bool           `json:"show_in_single"`
	RequiresBoatSelection *bool           `json:"requires_boat_selection"`
	Audience              string          `json:"audience"`
}

// normalize fills in defaults and validates the new pricing-kind /
// applicability fields. Returns a non-empty string when invalid.
func (req *createPriceItemRequest) normalize() string {
	if req.PricingKind == "" {
		req.PricingKind = "flat"
	}
	if req.PricingKind != "flat" && req.PricingKind != "tiered" {
		return "pricing_kind must be 'flat' or 'tiered'"
	}
	if req.PricingKind == "flat" {
		req.TierDimension = nil
	} else {
		if req.TierDimension == nil || (*req.TierDimension != "beam" && *req.TierDimension != "length") {
			return "tier_dimension must be 'beam' or 'length' when pricing_kind='tiered'"
		}
	}
	if req.Audience == "" {
		req.Audience = "all"
	}
	if !validAudiences[req.Audience] {
		return "audience must be 'all', 'member', or 'non_member'"
	}
	return ""
}

func boolDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (h *PriceItemsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createPriceItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validCategories[req.Category] {
		Error(w, http.StatusBadRequest, "invalid category")
		return
	}
	if req.Amount < 0 {
		Error(w, http.StatusBadRequest, "amount must be non-negative")
		return
	}
	if req.Currency == "" {
		req.Currency = "NOK"
	}
	if req.Unit == "" {
		req.Unit = "once"
	}
	if req.MaxInstallments < 1 {
		req.MaxInstallments = 1
	}
	if req.Metadata == nil {
		req.Metadata = json.RawMessage(`{}`)
	}
	if msg := req.normalize(); msg != "" {
		Error(w, http.StatusBadRequest, msg)
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	showInBatch := boolDefault(req.ShowInBatch, false)
	showInSingle := boolDefault(req.ShowInSingle, true)
	requiresBoat := boolDefault(req.RequiresBoatSelection, true)
	if showInBatch && requiresBoat {
		Error(w, http.StatusBadRequest, "show_in_batch=true requires requires_boat_selection=false (batch resolves boat from slip)")
		return
	}

	var item priceItem
	err := h.db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, description, amount, currency, unit,
		                          installments_allowed, max_installments, metadata, sort_order, is_active,
		                          pricing_kind, tier_dimension, show_in_batch, show_in_single, requires_boat_selection,
		                          audience)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		         $13, $14, $15, $16, $17, $18)
		 RETURNING `+priceItemColumns,
		claims.ClubID, req.Category, req.Name, req.Description, req.Amount, req.Currency, req.Unit,
		req.InstallmentsAllowed, req.MaxInstallments, req.Metadata, req.SortOrder, isActive,
		req.PricingKind, req.TierDimension, showInBatch, showInSingle, requiresBoat,
		req.Audience,
	).Scan(&item.ID, &item.Category, &item.Name, &item.Description, &item.Amount, &item.Currency, &item.Unit,
		&item.InstallmentsAllowed, &item.MaxInstallments, &item.Metadata,
		&item.SortOrder, &item.IsActive,
		&item.PricingKind, &item.TierDimension, &item.ShowInBatch, &item.ShowInSingle, &item.RequiresBoatSelection,
		&item.Audience,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create price item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, item)
}

func (h *PriceItemsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		Error(w, http.StatusBadRequest, "item ID is required")
		return
	}

	var req createPriceItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validCategories[req.Category] {
		Error(w, http.StatusBadRequest, "invalid category")
		return
	}
	if req.Metadata == nil {
		req.Metadata = json.RawMessage(`{}`)
	}
	if req.MaxInstallments < 1 {
		req.MaxInstallments = 1
	}
	if msg := req.normalize(); msg != "" {
		Error(w, http.StatusBadRequest, msg)
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	showInBatch := boolDefault(req.ShowInBatch, false)
	showInSingle := boolDefault(req.ShowInSingle, true)
	requiresBoat := boolDefault(req.RequiresBoatSelection, true)
	if showInBatch && requiresBoat {
		Error(w, http.StatusBadRequest, "show_in_batch=true requires requires_boat_selection=false (batch resolves boat from slip)")
		return
	}

	var item priceItem
	err := h.db.QueryRow(ctx,
		`UPDATE price_items
		 SET category = $3, name = $4, description = $5, amount = $6, currency = $7, unit = $8,
		     installments_allowed = $9, max_installments = $10, metadata = $11,
		     sort_order = $12, is_active = $13,
		     pricing_kind = $14, tier_dimension = $15,
		     show_in_batch = $16, show_in_single = $17, requires_boat_selection = $18,
		     audience = $19,
		     updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING `+priceItemColumns,
		itemID, claims.ClubID,
		req.Category, req.Name, req.Description, req.Amount, req.Currency, req.Unit,
		req.InstallmentsAllowed, req.MaxInstallments, req.Metadata,
		req.SortOrder, isActive,
		req.PricingKind, req.TierDimension, showInBatch, showInSingle, requiresBoat,
		req.Audience,
	).Scan(&item.ID, &item.Category, &item.Name, &item.Description, &item.Amount, &item.Currency, &item.Unit,
		&item.InstallmentsAllowed, &item.MaxInstallments, &item.Metadata,
		&item.SortOrder, &item.IsActive,
		&item.PricingKind, &item.TierDimension, &item.ShowInBatch, &item.ShowInSingle, &item.RequiresBoatSelection,
		&item.Audience,
		&item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "price item not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update price item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, item)
}

func (h *PriceItemsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	itemID := chi.URLParam(r, "itemID")
	if itemID == "" {
		Error(w, http.StatusBadRequest, "item ID is required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM price_items WHERE id = $1 AND club_id = $2`,
		itemID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete price item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "price item not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func scanPriceItems(rows pgx.Rows) ([]priceItem, error) {
	items := make([]priceItem, 0)
	for rows.Next() {
		var item priceItem
		if err := rows.Scan(
			&item.ID, &item.Category, &item.Name, &item.Description,
			&item.Amount, &item.Currency, &item.Unit,
			&item.InstallmentsAllowed, &item.MaxInstallments, &item.Metadata,
			&item.SortOrder, &item.IsActive,
			&item.PricingKind, &item.TierDimension, &item.ShowInBatch, &item.ShowInSingle, &item.RequiresBoatSelection,
			&item.Audience,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
