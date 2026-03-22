package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type OrdersHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewOrdersHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *OrdersHandler {
	return &OrdersHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "orders").Logger(),
	}
}

type orderLine struct {
	ProductID   *string `json:"product_id,omitempty"`
	PriceItemID *string `json:"price_item_id,omitempty"`
	Name        string  `json:"name"`
	Quantity    int     `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

type createOrderRequest struct {
	Lines      []orderLine `json:"lines"`
	GuestEmail string      `json:"guest_email"`
	GuestName  string      `json:"guest_name"`
}

type orderResponse struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	TotalAmount    float64         `json:"total_amount"`
	Currency       string          `json:"currency"`
	VippsReference string          `json:"vipps_reference"`
	CheckoutURL    string          `json:"checkout_url"`
	Lines          []orderLineResp `json:"lines"`
	CreatedAt      time.Time       `json:"created_at"`
}

type orderLineResp struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Quantity   int     `json:"quantity"`
	UnitPrice  float64 `json:"unit_price"`
	TotalPrice float64 `json:"total_price"`
}

type resolvedLine struct {
	productID   *string
	priceItemID *string
	name        string
	quantity    int
	unitPrice   float64
	totalPrice  float64
}

type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return e.message }

func (h *OrdersHandler) resolveOrderLine(ctx context.Context, tx pgx.Tx, line orderLine, clubID string) (resolvedLine, error) {
	if line.Quantity < 1 {
		return resolvedLine{}, &httpError{http.StatusBadRequest, "quantity must be at least 1"}
	}

	var unitPrice float64
	var name string

	if line.ProductID != nil && *line.ProductID != "" {
		var stock int
		err := tx.QueryRow(ctx,
			`SELECT name, price, stock FROM products
			 WHERE id = $1 AND club_id = $2 AND is_active = true
			 FOR UPDATE`,
			*line.ProductID, clubID,
		).Scan(&name, &unitPrice, &stock)
		if err == pgx.ErrNoRows {
			return resolvedLine{}, &httpError{http.StatusBadRequest, fmt.Sprintf("product %s not found or inactive", *line.ProductID)}
		}
		if err != nil {
			return resolvedLine{}, fmt.Errorf("query product: %w", err)
		}
		if stock < line.Quantity {
			return resolvedLine{}, &httpError{http.StatusConflict, fmt.Sprintf("insufficient stock for %s (available: %d)", name, stock)}
		}

		_, err = tx.Exec(ctx,
			`UPDATE products SET stock = stock - $1, updated_at = now() WHERE id = $2`,
			line.Quantity, *line.ProductID,
		)
		if err != nil {
			return resolvedLine{}, fmt.Errorf("decrement stock: %w", err)
		}
	} else if line.PriceItemID != nil && *line.PriceItemID != "" {
		err := tx.QueryRow(ctx,
			`SELECT name, amount FROM price_items
			 WHERE id = $1 AND club_id = $2 AND is_active = true`,
			*line.PriceItemID, clubID,
		).Scan(&name, &unitPrice)
		if err == pgx.ErrNoRows {
			return resolvedLine{}, &httpError{http.StatusBadRequest, fmt.Sprintf("price item %s not found", *line.PriceItemID)}
		}
		if err != nil {
			return resolvedLine{}, fmt.Errorf("query price item: %w", err)
		}
	} else {
		return resolvedLine{}, &httpError{http.StatusBadRequest, "each line must reference a product_id or price_item_id"}
	}

	lineTotal := unitPrice * float64(line.Quantity)
	return resolvedLine{
		productID:   line.ProductID,
		priceItemID: line.PriceItemID,
		name:        name,
		quantity:    line.Quantity,
		unitPrice:   unitPrice,
		totalPrice:  lineTotal,
	}, nil
}

// HandleCreateOrder creates an order from a cart and returns a (stubbed) Vipps checkout URL.
func (h *OrdersHandler) HandleCreateOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetClaims(ctx)

	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Lines) == 0 {
		Error(w, http.StatusBadRequest, "at least one line item is required")
		return
	}

	var clubID string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug,
	).Scan(&clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to resolve club")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var totalAmount float64
	var resolved []resolvedLine

	for _, line := range req.Lines {
		rl, err := h.resolveOrderLine(ctx, tx, line, clubID)
		if err != nil {
			if he, ok := err.(*httpError); ok {
				Error(w, he.status, he.message)
				return
			}
			h.log.Error().Err(err).Msg("failed to resolve order line")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		totalAmount += rl.totalPrice
		resolved = append(resolved, rl)
	}

	var userID *string
	guestEmail := req.GuestEmail
	guestName := req.GuestName
	if claims != nil {
		userID = &claims.UserID
	}

	vippsRef := fmt.Sprintf("brygge-%d", time.Now().UnixMilli())

	var orderID string
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, guest_email, guest_name, total_amount, vipps_reference)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		clubID, userID, guestEmail, guestName, totalAmount, vippsRef,
	).Scan(&orderID, &createdAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create order")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var lineResponses []orderLineResp
	for _, rl := range resolved {
		var lineID string
		err = tx.QueryRow(ctx,
			`INSERT INTO order_lines (order_id, product_id, price_item_id, name, quantity, unit_price, total_price)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id`,
			orderID, rl.productID, rl.priceItemID, rl.name, rl.quantity, rl.unitPrice, rl.totalPrice,
		).Scan(&lineID)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to create order line")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		lineResponses = append(lineResponses, orderLineResp{
			ID:         lineID,
			Name:       rl.name,
			Quantity:   rl.quantity,
			UnitPrice:  rl.unitPrice,
			TotalPrice: rl.totalPrice,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit order")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().
		Str("order_id", orderID).
		Str("vipps_ref", vippsRef).
		Float64("total", totalAmount).
		Msg("order created (Vipps payment stub)")

	// Stub: In production, this would call Vipps ePayment API to create a payment session
	// and return the actual Vipps checkout URL. For now, we return a stub URL.
	checkoutURL := fmt.Sprintf("/checkout/confirm?order=%s&ref=%s", orderID, vippsRef)

	JSON(w, http.StatusCreated, orderResponse{
		ID:             orderID,
		Status:         "pending",
		TotalAmount:    totalAmount,
		Currency:       "NOK",
		VippsReference: vippsRef,
		CheckoutURL:    checkoutURL,
		Lines:          lineResponses,
		CreatedAt:      createdAt,
	})
}

// HandleConfirmOrder is a stub for the Vipps callback -- marks order as paid.
func (h *OrdersHandler) HandleConfirmOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID := chi.URLParam(r, "orderID")

	var status string
	err := h.db.QueryRow(ctx,
		`UPDATE orders SET status = 'paid', paid_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'pending'
		 RETURNING status`,
		orderID,
	).Scan(&status)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "order not found or already processed")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to confirm order")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("order_id", orderID).Msg("order confirmed (stub)")

	JSON(w, http.StatusOK, map[string]string{
		"order_id": orderID,
		"status":   "paid",
	})
}

// HandleGetOrder returns order details.
func (h *OrdersHandler) HandleGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID := chi.URLParam(r, "orderID")

	var o orderResponse
	var paidAt *time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, status, total_amount, currency, vipps_reference, created_at, paid_at
		 FROM orders WHERE id = $1`,
		orderID,
	).Scan(&o.ID, &o.Status, &o.TotalAmount, &o.Currency, &o.VippsReference, &o.CreatedAt, &paidAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query order")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, name, quantity, unit_price, total_price
		 FROM order_lines WHERE order_id = $1`,
		orderID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query order lines")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	o.Lines = make([]orderLineResp, 0)
	for rows.Next() {
		var l orderLineResp
		if err := rows.Scan(&l.ID, &l.Name, &l.Quantity, &l.UnitPrice, &l.TotalPrice); err != nil {
			h.log.Error().Err(err).Msg("failed to scan order line")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		o.Lines = append(o.Lines, l)
	}

	JSON(w, http.StatusOK, o)
}
