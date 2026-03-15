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
	ID                  string          `json:"id"`
	Category            string          `json:"category"`
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	Amount              float64         `json:"amount"`
	Currency            string          `json:"currency"`
	Unit                string          `json:"unit"`
	InstallmentsAllowed bool            `json:"installments_allowed"`
	MaxInstallments     int             `json:"max_installments"`
	Metadata            json.RawMessage `json:"metadata"`
	SortOrder           int             `json:"sort_order"`
	IsActive            bool            `json:"is_active"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

var validCategories = map[string]bool{
	"harbor_membership": true,
	"slip_fee":          true,
	"seasonal_rental":   true,
	"guest":             true,
	"motorhome":         true,
	"room_hire":         true,
	"service":           true,
	"other":             true,
}

// HandleListPublic returns active price items grouped by category (no auth required).
func (h *PriceItemsHandler) HandleListPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id, category, name, description, amount, currency, unit,
		        installments_allowed, max_installments, metadata,
		        sort_order, is_active, created_at, updated_at
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
		`SELECT id, category, name, description, amount, currency, unit,
		        installments_allowed, max_installments, metadata,
		        sort_order, is_active, created_at, updated_at
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
	Category            string          `json:"category"`
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	Amount              float64         `json:"amount"`
	Currency            string          `json:"currency"`
	Unit                string          `json:"unit"`
	InstallmentsAllowed bool            `json:"installments_allowed"`
	MaxInstallments     int             `json:"max_installments"`
	Metadata            json.RawMessage `json:"metadata"`
	SortOrder           int             `json:"sort_order"`
	IsActive            *bool           `json:"is_active"`
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
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var item priceItem
	err := h.db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, description, amount, currency, unit,
		                          installments_allowed, max_installments, metadata, sort_order, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, category, name, description, amount, currency, unit,
		           installments_allowed, max_installments, metadata,
		           sort_order, is_active, created_at, updated_at`,
		claims.ClubID, req.Category, req.Name, req.Description, req.Amount, req.Currency, req.Unit,
		req.InstallmentsAllowed, req.MaxInstallments, req.Metadata, req.SortOrder, isActive,
	).Scan(&item.ID, &item.Category, &item.Name, &item.Description, &item.Amount, &item.Currency, &item.Unit,
		&item.InstallmentsAllowed, &item.MaxInstallments, &item.Metadata,
		&item.SortOrder, &item.IsActive, &item.CreatedAt, &item.UpdatedAt)
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
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var item priceItem
	err := h.db.QueryRow(ctx,
		`UPDATE price_items
		 SET category = $3, name = $4, description = $5, amount = $6, currency = $7, unit = $8,
		     installments_allowed = $9, max_installments = $10, metadata = $11,
		     sort_order = $12, is_active = $13, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, category, name, description, amount, currency, unit,
		           installments_allowed, max_installments, metadata,
		           sort_order, is_active, created_at, updated_at`,
		itemID, claims.ClubID,
		req.Category, req.Name, req.Description, req.Amount, req.Currency, req.Unit,
		req.InstallmentsAllowed, req.MaxInstallments, req.Metadata,
		req.SortOrder, isActive,
	).Scan(&item.ID, &item.Category, &item.Name, &item.Description, &item.Amount, &item.Currency, &item.Unit,
		&item.InstallmentsAllowed, &item.MaxInstallments, &item.Metadata,
		&item.SortOrder, &item.IsActive, &item.CreatedAt, &item.UpdatedAt)
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
			&item.SortOrder, &item.IsActive, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
