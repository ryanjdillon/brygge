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

type ProductsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewProductsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *ProductsHandler {
	return &ProductsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "products").Logger(),
	}
}

type productVariant struct {
	ID            string   `json:"id"`
	Size          string   `json:"size"`
	Color         string   `json:"color"`
	Stock         int      `json:"stock"`
	PriceOverride *float64 `json:"price_override"`
	ImageURL      string   `json:"image_url"`
	SortOrder     int      `json:"sort_order"`
}

type product struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Price       float64          `json:"price"`
	Currency    string           `json:"currency"`
	ImageURL    string           `json:"image_url"`
	Stock       int              `json:"stock"`
	IsActive    bool             `json:"is_active"`
	SortOrder   int              `json:"sort_order"`
	Variants    []productVariant `json:"variants"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func (h *ProductsHandler) loadVariantsForProducts(r *http.Request, products []product) []product {
	ids := make([]string, len(products))
	for i, p := range products {
		ids[i] = p.ID
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, product_id, size, color, stock, price_override, image_url, sort_order
		 FROM product_variants
		 WHERE product_id = ANY($1)
		 ORDER BY sort_order, size, color`,
		ids,
	)
	if err != nil {
		h.log.Warn().Err(err).Msg("failed to query variants, returning products without variants")
		for i := range products {
			products[i].Variants = []productVariant{}
		}
		return products
	}
	defer rows.Close()

	variantMap := make(map[string][]productVariant)
	for rows.Next() {
		var v productVariant
		var productID string
		if err := rows.Scan(&v.ID, &productID, &v.Size, &v.Color, &v.Stock, &v.PriceOverride, &v.ImageURL, &v.SortOrder); err != nil {
			h.log.Warn().Err(err).Msg("failed to scan variant row")
			continue
		}
		variantMap[productID] = append(variantMap[productID], v)
	}

	for i := range products {
		if variants, ok := variantMap[products[i].ID]; ok {
			products[i].Variants = variants
		} else {
			products[i].Variants = []productVariant{}
		}
	}
	return products
}

func (h *ProductsHandler) HandleListPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id, name, description, price, currency, image_url, stock,
		        is_active, sort_order, created_at, updated_at
		 FROM products
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $1)
		   AND is_active = true
		 ORDER BY sort_order, name`,
		h.config.ClubSlug,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query products")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	products, err := scanProducts(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan products")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	products = h.loadVariantsForProducts(r, products)

	JSON(w, http.StatusOK, map[string]any{"products": products})
}

func (h *ProductsHandler) HandleListAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, name, description, price, currency, image_url, stock,
		        is_active, sort_order, created_at, updated_at
		 FROM products
		 WHERE club_id = $1
		 ORDER BY sort_order, name`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query products")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	products, err := scanProducts(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan products")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	products = h.loadVariantsForProducts(r, products)

	JSON(w, http.StatusOK, map[string]any{"products": products})
}

type productRequest struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	ImageURL    string  `json:"image_url"`
	Stock       int     `json:"stock"`
	IsActive    *bool   `json:"is_active"`
	SortOrder   int     `json:"sort_order"`
}

func (h *ProductsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req productRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Currency == "" {
		req.Currency = "NOK"
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var p product
	err := h.db.QueryRow(ctx,
		`INSERT INTO products (club_id, name, description, price, currency, image_url, stock, is_active, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, name, description, price, currency, image_url, stock, is_active, sort_order, created_at, updated_at`,
		claims.ClubID, req.Name, req.Description, req.Price, req.Currency, req.ImageURL, req.Stock, isActive, req.SortOrder,
	).Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Currency, &p.ImageURL, &p.Stock, &p.IsActive, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create product")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	p.Variants = []productVariant{}

	JSON(w, http.StatusCreated, p)
}

func (h *ProductsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	productID := chi.URLParam(r, "productID")

	var req productRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var p product
	err := h.db.QueryRow(ctx,
		`UPDATE products
		 SET name = $3, description = $4, price = $5, currency = $6, image_url = $7,
		     stock = $8, is_active = $9, sort_order = $10, updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, name, description, price, currency, image_url, stock, is_active, sort_order, created_at, updated_at`,
		productID, claims.ClubID,
		req.Name, req.Description, req.Price, req.Currency, req.ImageURL, req.Stock, isActive, req.SortOrder,
	).Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Currency, &p.ImageURL, &p.Stock, &p.IsActive, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update product")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	p.Variants = []productVariant{}

	JSON(w, http.StatusOK, p)
}

func (h *ProductsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	productID := chi.URLParam(r, "productID")

	tag, err := h.db.Exec(ctx,
		`DELETE FROM products WHERE id = $1 AND club_id = $2`,
		productID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete product")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "product not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type variantRequest struct {
	Size          string   `json:"size"`
	Color         string   `json:"color"`
	Stock         int      `json:"stock"`
	PriceOverride *float64 `json:"price_override"`
	ImageURL      string   `json:"image_url"`
	SortOrder     int      `json:"sort_order"`
}

func (h *ProductsHandler) HandleCreateVariant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	productID := chi.URLParam(r, "productID")

	// Verify product belongs to club
	var exists bool
	_ = h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM products WHERE id = $1 AND club_id = $2)`,
		productID, claims.ClubID,
	).Scan(&exists)
	if !exists {
		Error(w, http.StatusNotFound, "product not found")
		return
	}

	var req variantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var v productVariant
	err := h.db.QueryRow(ctx,
		`INSERT INTO product_variants (product_id, size, color, stock, price_override, image_url, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (product_id, size, color) DO UPDATE SET stock = EXCLUDED.stock, price_override = EXCLUDED.price_override, image_url = EXCLUDED.image_url, sort_order = EXCLUDED.sort_order
		 RETURNING id, size, color, stock, price_override, image_url, sort_order`,
		productID, req.Size, req.Color, req.Stock, req.PriceOverride, req.ImageURL, req.SortOrder,
	).Scan(&v.ID, &v.Size, &v.Color, &v.Stock, &v.PriceOverride, &v.ImageURL, &v.SortOrder)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create variant")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, v)
}

func (h *ProductsHandler) HandleDeleteVariant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	variantID := chi.URLParam(r, "variantID")

	tag, err := h.db.Exec(ctx,
		`DELETE FROM product_variants
		 WHERE id = $1
		   AND product_id IN (SELECT id FROM products WHERE club_id = $2)`,
		variantID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete variant")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "variant not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func scanProducts(rows pgx.Rows) ([]product, error) {
	products := make([]product, 0)
	for rows.Next() {
		var p product
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.Price, &p.Currency, &p.ImageURL,
			&p.Stock, &p.IsActive, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}
