package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type FinancialsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	audit  *audit.Service
	log    zerolog.Logger
}

func NewFinancialsHandler(db *pgxpool.Pool, cfg *config.Config, auditService *audit.Service, log zerolog.Logger) *FinancialsHandler {
	return &FinancialsHandler{
		db:     db,
		config: cfg,
		audit:  auditService,
		log:    log.With().Str("handler", "financials").Logger(),
	}
}

type financialSummary struct {
	TotalDuesReceived  float64 `json:"total_dues_received"`
	TotalOutstanding   float64 `json:"total_outstanding"`
	TotalOverdue       float64 `json:"total_overdue"`
	TotalHarborMembershipCollected float64 `json:"total_harbor_membership_collected"`
	TotalBookingRevenue float64 `json:"total_booking_revenue"`
	Year               *int    `json:"year,omitempty"`
}

type priceItemSummaryRow struct {
	PriceItemID  string  `json:"price_item_id"`
	Description  string  `json:"description"`
	Category     string  `json:"category"`
	Billed       float64 `json:"billed"`
	Received     float64 `json:"received"`
	Overdue      float64 `json:"overdue"`
	Outstanding  float64 `json:"outstanding"`
	InvoiceCount int     `json:"invoice_count"`
	PaidCount    int     `json:"paid_count"`
	OverdueCount int     `json:"overdue_count"`
}

type priceItemSummaryResponse struct {
	Year  *int                   `json:"year,omitempty"`
	Items []priceItemSummaryRow  `json:"items"`
	// Totals across every price item in the response — convenient for
	// the dashboard's headline numbers without re-summing client-side.
	Totals struct {
		Billed      float64 `json:"billed"`
		Received    float64 `json:"received"`
		Overdue     float64 `json:"overdue"`
		Outstanding float64 `json:"outstanding"`
	} `json:"totals"`
}

// HandleGetPriceItemSummary aggregates invoice_lines by price_item for a
// fiscal year. Unlike HandleGetFinancialSummary (which reads the
// payments table — primarily Vipps), this reads from the faktura side
// so clubs that only send manual invoices see real numbers.
//
// "Received" is currently a coarse proxy: an invoice counts as received
// when invoices.payment_id is non-NULL (i.e. linked to a payment row,
// regardless of that payment's status). When/if we add bank-reconciled
// receipts, this should narrow to payments.status='completed'.
func (h *FinancialsHandler) HandleGetPriceItemSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var yearFilter *int
	if y := r.URL.Query().Get("year"); y != "" {
		yi, err := strconv.Atoi(y)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid year parameter")
			return
		}
		yearFilter = &yi
	}

	args := []any{claims.ClubID}
	periodClause := ""
	if yearFilter != nil {
		periodClause = ` AND i.fiscal_period_id IN (
			SELECT id FROM fiscal_periods WHERE club_id = $1 AND year = $2
		)`
		args = append(args, *yearFilter)
	}

	rows, err := h.db.Query(ctx, `
		SELECT pi.id,
		       COALESCE(NULLIF(pi.description, ''), pi.name) AS description,
		       pi.category,
		       COALESCE(SUM(il.line_total), 0) AS billed,
		       COALESCE(SUM(CASE WHEN i.payment_id IS NOT NULL THEN il.line_total ELSE 0 END), 0) AS received,
		       COALESCE(SUM(CASE WHEN i.payment_id IS NULL AND i.due_date < CURRENT_DATE THEN il.line_total ELSE 0 END), 0) AS overdue,
		       COUNT(DISTINCT i.id) AS invoice_count,
		       COUNT(DISTINCT CASE WHEN i.payment_id IS NOT NULL THEN i.id END) AS paid_count,
		       COUNT(DISTINCT CASE WHEN i.payment_id IS NULL AND i.due_date < CURRENT_DATE THEN i.id END) AS overdue_count
		  FROM price_items pi
		  LEFT JOIN invoice_lines il ON il.price_item_id = pi.id
		  LEFT JOIN invoices i ON i.id = il.invoice_id
		   AND i.club_id = $1
		   AND i.status <> 'voided'`+periodClause+`
		 WHERE pi.club_id = $1
		 GROUP BY pi.id, pi.description, pi.name, pi.category, pi.sort_order
		HAVING COALESCE(SUM(il.line_total), 0) > 0
		 ORDER BY pi.category, pi.sort_order, description`,
		args...,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("price-item summary query")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	resp := priceItemSummaryResponse{Year: yearFilter, Items: []priceItemSummaryRow{}}
	for rows.Next() {
		var row priceItemSummaryRow
		if err := rows.Scan(
			&row.PriceItemID, &row.Description, &row.Category,
			&row.Billed, &row.Received, &row.Overdue,
			&row.InvoiceCount, &row.PaidCount, &row.OverdueCount,
		); err != nil {
			h.log.Error().Err(err).Msg("price-item summary scan")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		row.Outstanding = row.Billed - row.Received
		resp.Items = append(resp.Items, row)
		resp.Totals.Billed += row.Billed
		resp.Totals.Received += row.Received
		resp.Totals.Overdue += row.Overdue
		resp.Totals.Outstanding += row.Outstanding
	}

	JSON(w, http.StatusOK, resp)
}

type paymentRow struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	UserName    string     `json:"user_name"`
	UserEmail   string     `json:"user_email"`
	Type        string     `json:"type"`
	Amount      float64    `json:"amount"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"`
	Description string     `json:"description"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	Reference   string     `json:"vipps_reference"`
	CreatedAt   time.Time  `json:"created_at"`
}

type paymentsListResponse struct {
	Payments []paymentRow `json:"payments"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PerPage  int          `json:"per_page"`
}

type overduePayment struct {
	ID          string  `json:"id"`
	UserID      string  `json:"user_id"`
	UserName    string  `json:"user_name"`
	UserEmail   string  `json:"user_email"`
	UserPhone   string  `json:"user_phone"`
	Type        string  `json:"type"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	DueDate     string  `json:"due_date"`
	DaysOverdue int     `json:"days_overdue"`
}

type createInvoiceRequest struct {
	UserID      string  `json:"user_id"`
	Type        string  `json:"type"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
	DueDate     string  `json:"due_date"`
}

func (h *FinancialsHandler) HandleGetFinancialSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	yearStr := r.URL.Query().Get("year")

	var yearFilter *int
	if yearStr != "" {
		y, err := strconv.Atoi(yearStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid year parameter")
			return
		}
		yearFilter = &y
	}

	yearClause := ""
	args := []any{claims.ClubID}
	if yearFilter != nil {
		yearClause = " AND EXTRACT(YEAR FROM p.created_at) = $2"
		args = append(args, *yearFilter)
	}

	query := fmt.Sprintf(`
		SELECT
			COALESCE(SUM(CASE WHEN p.type = 'dues' AND p.status = 'completed' THEN p.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN p.status = 'pending' THEN p.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN p.status = 'pending' AND p.due_date < now() THEN p.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN p.type = 'harbor_membership' AND p.status = 'completed' THEN p.amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN p.type = 'booking' AND p.status = 'completed' THEN p.amount ELSE 0 END), 0)
		FROM payments p
		WHERE p.club_id = $1%s
	`, yearClause)

	var s financialSummary
	err := h.db.QueryRow(ctx, query, args...).Scan(
		&s.TotalDuesReceived,
		&s.TotalOutstanding,
		&s.TotalOverdue,
		&s.TotalHarborMembershipCollected,
		&s.TotalBookingRevenue,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query financial summary")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.Year = yearFilter
	JSON(w, http.StatusOK, s)
}

func (h *FinancialsHandler) HandleListPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")
	yearStr := r.URL.Query().Get("year")
	pageStr := r.URL.Query().Get("page")
	perPageStr := r.URL.Query().Get("per_page")

	page := 1
	perPage := 50
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if perPageStr != "" {
		if pp, err := strconv.Atoi(perPageStr); err == nil && pp > 0 && pp <= 100 {
			perPage = pp
		}
	}

	whereClause := "WHERE p.club_id = $1"
	args := []any{claims.ClubID}
	argIdx := 2

	if typeFilter != "" {
		whereClause += fmt.Sprintf(" AND p.type = $%d", argIdx)
		args = append(args, typeFilter)
		argIdx++
	}
	if statusFilter != "" {
		whereClause += fmt.Sprintf(" AND p.status = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if yearStr != "" {
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid year parameter")
			return
		}
		whereClause += fmt.Sprintf(" AND EXTRACT(YEAR FROM p.created_at) = $%d", argIdx)
		args = append(args, year)
		argIdx++
	}

	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM payments p %s", whereClause)
	if err := h.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		h.log.Error().Err(err).Msg("failed to count payments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	offset := (page - 1) * perPage
	args = append(args, perPage, offset)

	query := fmt.Sprintf(`
		SELECT p.id, p.user_id, u.full_name, u.email, p.type, p.amount, p.currency,
		       p.status, p.description, p.due_date, p.paid_at, p.vipps_reference, p.created_at
		FROM payments p
		JOIN users u ON u.id = p.user_id
		%s
		ORDER BY p.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list payments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	payments := make([]paymentRow, 0)
	for rows.Next() {
		var p paymentRow
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.UserName, &p.UserEmail,
			&p.Type, &p.Amount, &p.Currency, &p.Status,
			&p.Description, &p.DueDate, &p.PaidAt, &p.Reference, &p.CreatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan payment row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		payments = append(payments, p)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating payment rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, paymentsListResponse{
		Payments: payments,
		Total:    total,
		Page:     page,
		PerPage:  perPage,
	})
}

func (h *FinancialsHandler) HandleGetPaymentDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	paymentID := chi.URLParam(r, "paymentID")
	if paymentID == "" {
		Error(w, http.StatusBadRequest, "missing payment ID")
		return
	}

	var p paymentRow
	err := h.db.QueryRow(ctx,
		`SELECT p.id, p.user_id, u.full_name, u.email, p.type, p.amount, p.currency,
		        p.status, p.description, p.due_date, p.paid_at, p.vipps_reference, p.created_at
		 FROM payments p
		 JOIN users u ON u.id = p.user_id
		 WHERE p.id = $1 AND p.club_id = $2`,
		paymentID, claims.ClubID,
	).Scan(
		&p.ID, &p.UserID, &p.UserName, &p.UserEmail,
		&p.Type, &p.Amount, &p.Currency, &p.Status,
		&p.Description, &p.DueDate, &p.PaidAt, &p.Reference, &p.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "payment not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch payment details")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, p)
}

func (h *FinancialsHandler) HandleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")
	yearStr := r.URL.Query().Get("year")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	whereClause := "WHERE p.club_id = $1"
	args := []any{claims.ClubID}
	argIdx := 2

	if typeFilter != "" {
		whereClause += fmt.Sprintf(" AND p.type = $%d", argIdx)
		args = append(args, typeFilter)
		argIdx++
	}
	if statusFilter != "" {
		whereClause += fmt.Sprintf(" AND p.status = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if yearStr != "" {
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid year parameter")
			return
		}
		whereClause += fmt.Sprintf(" AND EXTRACT(YEAR FROM p.created_at) = $%d", argIdx)
		args = append(args, year)
		argIdx++
	}
	if startStr != "" {
		startDate, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid start date format, use YYYY-MM-DD")
			return
		}
		whereClause += fmt.Sprintf(" AND p.created_at >= $%d", argIdx)
		args = append(args, startDate)
		argIdx++
	}
	if endStr != "" {
		endDate, err := time.Parse("2006-01-02", endStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid end date format, use YYYY-MM-DD")
			return
		}
		whereClause += fmt.Sprintf(" AND p.created_at < $%d", argIdx)
		args = append(args, endDate.AddDate(0, 0, 1))
	}

	query := fmt.Sprintf(`
		SELECT p.created_at, u.full_name, u.email, p.type, p.amount, p.currency,
		       p.status, p.vipps_reference
		FROM payments p
		JOIN users u ON u.id = p.user_id
		%s
		ORDER BY p.created_at DESC
	`, whereClause)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query payments for CSV export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=payments_export.csv")
	w.WriteHeader(http.StatusOK)

	csvWriter := csv.NewWriter(w)
	csvWriter.Write([]string{"date", "member_name", "member_email", "type", "amount", "currency", "status", "reference"})

	for rows.Next() {
		var (
			createdAt time.Time
			name      string
			email     string
			pType     string
			amount    float64
			currency  string
			status    string
			reference string
		)
		if err := rows.Scan(&createdAt, &name, &email, &pType, &amount, &currency, &status, &reference); err != nil {
			h.log.Error().Err(err).Msg("failed to scan CSV row")
			return
		}
		csvWriter.Write([]string{
			createdAt.Format("2006-01-02"),
			name,
			email,
			pType,
			fmt.Sprintf("%.2f", amount),
			currency,
			status,
			reference,
		})
	}
	csvWriter.Flush()

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionFinanceCSVExported, "export", "",
			map[string]any{"type": typeFilter, "status": statusFilter, "year": yearStr})
	}
}

func (h *FinancialsHandler) HandleGenerateInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.UserID == "" || req.Type == "" || req.Amount <= 0 || req.DueDate == "" {
		Error(w, http.StatusBadRequest, "user_id, type, amount, and due_date are required")
		return
	}

	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
		return
	}

	var userExists bool
	err = h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND club_id = $2)`,
		req.UserID, claims.ClubID,
	).Scan(&userExists)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to verify user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !userExists {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	var p paymentRow
	err = h.db.QueryRow(ctx,
		`INSERT INTO payments (club_id, user_id, type, amount, currency, status, description, due_date)
		 VALUES ($1, $2, $3, $4, 'NOK', 'pending', $5, $6)
		 RETURNING id, user_id, type, amount, 'NOK', status, description, due_date, paid_at, vipps_reference, created_at`,
		claims.ClubID, req.UserID, req.Type, req.Amount, req.Description, dueDate,
	).Scan(
		&p.ID, &p.UserID, &p.Type, &p.Amount, &p.Currency, &p.Status,
		&p.Description, &p.DueDate, &p.PaidAt, &p.Reference, &p.CreatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create invoice payment")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userName, userEmail string
	_ = h.db.QueryRow(ctx,
		`SELECT full_name, email FROM users WHERE id = $1`,
		req.UserID,
	).Scan(&userName, &userEmail)
	p.UserName = userName
	p.UserEmail = userEmail

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionPaymentCreated, "payment", p.ID,
			map[string]any{"type": req.Type, "amount": req.Amount, "user_id": req.UserID, "due_date": req.DueDate})
	}

	JSON(w, http.StatusCreated, p)
}

func (h *FinancialsHandler) HandleListOverdue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT p.id, p.user_id, u.full_name, u.email, u.phone, p.type, p.amount, p.currency,
		        p.description, p.due_date, EXTRACT(DAY FROM now() - p.due_date)::int
		 FROM payments p
		 JOIN users u ON u.id = p.user_id
		 WHERE p.club_id = $1 AND p.status = 'pending' AND p.due_date < now()
		 ORDER BY p.due_date ASC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query overdue payments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	overdue := make([]overduePayment, 0)
	for rows.Next() {
		var o overduePayment
		var dueDate time.Time
		if err := rows.Scan(
			&o.ID, &o.UserID, &o.UserName, &o.UserEmail, &o.UserPhone,
			&o.Type, &o.Amount, &o.Currency, &o.Description, &dueDate, &o.DaysOverdue,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan overdue payment")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		o.DueDate = dueDate.Format("2006-01-02")
		overdue = append(overdue, o)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating overdue rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, overdue)
}
