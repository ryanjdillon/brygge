package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type InvoiceHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	email  email.Sender
	audit  *audit.Service
	log    zerolog.Logger
}

func NewInvoiceHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	emailClient email.Sender,
	auditService *audit.Service,
	log zerolog.Logger,
) *InvoiceHandler {
	return &InvoiceHandler{
		db:     db,
		config: cfg,
		email:  emailClient,
		audit:  auditService,
		log:    log.With().Str("handler", "invoices").Logger(),
	}
}

type createInvoiceFullRequest struct {
	UserID    string              `json:"user_id"`
	DueDate   string              `json:"due_date"`
	Lines     []invoiceLineInput  `json:"lines"`
	SendEmail bool                `json:"send_email"`
}

type invoiceLineInput struct {
	Description string  `json:"description"`
	Quantity    int     `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

type invoiceResponse struct {
	ID            string    `json:"id"`
	InvoiceNumber int       `json:"invoice_number"`
	KID           string    `json:"kid_number"`
	UserID        string    `json:"user_id"`
	TotalAmount   float64   `json:"total_amount"`
	IssueDate     string    `json:"issue_date"`
	DueDate       string    `json:"due_date"`
	SentAt        *string   `json:"sent_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// HandleCreateInvoice generates a full invoice with KID, PDF, and optional email delivery.
func (h *InvoiceHandler) HandleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createInvoiceFullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.UserID == "" || req.DueDate == "" || len(req.Lines) == 0 {
		Error(w, http.StatusBadRequest, "user_id, due_date, and at least one line item are required")
		return
	}

	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
		return
	}

	// Look up member
	var memberName, memberEmail, memberAddress string
	err = h.db.QueryRow(ctx,
		`SELECT full_name, email, COALESCE(address_line || ', ' || postal_code || ' ' || city, '')
		 FROM users WHERE id = $1 AND club_id = $2`,
		req.UserID, claims.ClubID,
	).Scan(&memberName, &memberEmail, &memberAddress)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up member for invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Calculate total
	var total float64
	pdfLines := make([]finance.InvoiceLine, len(req.Lines))
	for i, line := range req.Lines {
		if line.Quantity < 1 {
			Error(w, http.StatusBadRequest, "quantity must be at least 1")
			return
		}
		pdfLines[i] = finance.InvoiceLine{
			Description: line.Description,
			Quantity:    line.Quantity,
			UnitPrice:   line.UnitPrice,
		}
		total += float64(line.Quantity) * line.UnitPrice
	}

	// Get next invoice sequence for KID
	var invoiceSeq int
	err = h.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1`,
		claims.ClubID,
	).Scan(&invoiceSeq)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get invoice sequence")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Get club settings for PDF
	var clubName, orgNumber, clubAddress, bankAccount string
	err = h.db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''), COALESCE(bank_account, '')
		 FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &orgNumber, &clubAddress, &bankAccount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club details for invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	kid := finance.GenerateKID("000", invoiceSeq, 1)

	// Generate PDF
	inv := finance.Invoice{
		ClubName:      clubName,
		OrgNumber:     orgNumber,
		ClubAddress:   clubAddress,
		MemberName:    memberName,
		MemberAddress: memberAddress,
		InvoiceNumber: invoiceSeq,
		IssueDate:     time.Now(),
		DueDate:       dueDate,
		KID:           kid,
		BankAccount:   bankAccount,
		Lines:         pdfLines,
	}

	pdfData, err := finance.GeneratePDF(inv)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate invoice PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Store invoice
	var invoiceID string
	var createdAt time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, due_date, total_amount, pdf_data)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		claims.ClubID, req.UserID, invoiceSeq, kid, dueDate, total, pdfData,
	).Scan(&invoiceID, &createdAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Store line items
	for _, line := range req.Lines {
		lineTotal := float64(line.Quantity) * line.UnitPrice
		_, err = h.db.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total)
			 VALUES ($1, $2, $3, $4, $5)`,
			invoiceID, line.Description, line.Quantity, line.UnitPrice, lineTotal,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to store invoice line")
		}
	}

	// Send email if requested
	var sentAt *string
	if req.SendEmail && h.email != nil && memberEmail != "" {
		filename := fmt.Sprintf("faktura-%d.pdf", invoiceSeq)
		// Invoice locale follows the requesting admin's UI language for now.
		// DIL-185 (users.preferred_locale) will swap this for per-member.
		locale := email.DetectLocale(r)
		subject := email.InvoiceSubject(locale, clubName, invoiceSeq)
		htmlBody := email.InvoiceBody(locale, memberName, clubName, invoiceSeq, dueDate, total, kid, bankAccount)
		if err := h.email.SendWithAttachment(ctx, memberEmail, subject, htmlBody, filename, pdfData); err != nil {
			h.log.Error().Err(err).Str("email", memberEmail).Msg("failed to send invoice email")
		} else {
			now := time.Now().Format(time.RFC3339)
			sentAt = &now
			h.db.Exec(ctx, `UPDATE invoices SET sent_at = NOW() WHERE id = $1`, invoiceID)
			if h.audit != nil {
				h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
					audit.ActionInvoiceEmailed, "invoice", invoiceID,
					map[string]any{"email": memberEmail, "invoice_number": invoiceSeq})
			}
		}
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", invoiceID,
			map[string]any{"invoice_number": invoiceSeq, "kid": kid, "total": total, "user_id": req.UserID})
	}

	JSON(w, http.StatusCreated, invoiceResponse{
		ID:            invoiceID,
		InvoiceNumber: invoiceSeq,
		KID:           kid,
		UserID:        req.UserID,
		TotalAmount:   total,
		IssueDate:     time.Now().Format("2006-01-02"),
		DueDate:       req.DueDate,
		SentAt:        sentAt,
		CreatedAt:     createdAt,
	})
}

// HandleGetInvoicePDF returns the stored PDF for an invoice.
func (h *InvoiceHandler) HandleGetInvoicePDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	var pdfData []byte
	var invoiceNumber int
	err := h.db.QueryRow(ctx,
		`SELECT pdf_data, invoice_number FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&pdfData, &invoiceNumber)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch invoice PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if pdfData == nil {
		Error(w, http.StatusNotFound, "PDF not available for this invoice")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="faktura-%d.pdf"`, invoiceNumber))
	w.Write(pdfData)
}

type draftInvoiceRow struct {
	ID            string    `json:"id"`
	InvoiceNumber int       `json:"invoice_number"`
	UserID        string    `json:"user_id"`
	MemberName    string    `json:"member_name"`
	MemberEmail   string    `json:"member_email"`
	TotalAmount   float64   `json:"total_amount"`
	IssueDate     string    `json:"issue_date"`
	DueDate       string    `json:"due_date"`
	PriceItemName string    `json:"price_item_name"`
	FiscalYear    *int      `json:"fiscal_year"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}

// HandleListDraftInvoices returns every unsent invoice (sent_at IS
// NULL) for the club, joined to user/price_item/fiscal_period for
// display in the regnskap drafts review page.
func (h *InvoiceHandler) HandleListDraftInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.user_id,
		        COALESCE(u.full_name, u.first_name || ' ' || u.last_name),
		        u.email,
		        i.total_amount, i.issue_date, i.due_date,
		        COALESCE(pi.name, ''), fp.year,
		        COALESCE((SELECT description FROM invoice_lines WHERE invoice_id = i.id LIMIT 1), ''),
		        i.created_at
		   FROM invoices i
		   JOIN users u ON u.id = i.user_id
		   LEFT JOIN price_items pi ON pi.id = i.price_item_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE i.club_id = $1 AND i.sent_at IS NULL
		  ORDER BY i.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("list draft invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := make([]draftInvoiceRow, 0)
	for rows.Next() {
		var d draftInvoiceRow
		var issue, due time.Time
		if err := rows.Scan(&d.ID, &d.InvoiceNumber, &d.UserID,
			&d.MemberName, &d.MemberEmail,
			&d.TotalAmount, &issue, &due,
			&d.PriceItemName, &d.FiscalYear, &d.Description, &d.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("scan draft row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.IssueDate = issue.Format("2006-01-02")
		d.DueDate = due.Format("2006-01-02")
		out = append(out, d)
	}

	JSON(w, http.StatusOK, map[string]any{"items": out})
}

// HandleSendInvoice emails the stored PDF to the member and stamps
// sent_at. Idempotent: 409 if already sent.
func (h *InvoiceHandler) HandleSendInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	var (
		invoiceNumber int
		userID        string
		memberName    string
		memberEmail   string
		total         float64
		dueDate       time.Time
		kid           string
		pdfData       []byte
		alreadySent   *time.Time
	)
	if err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number, i.user_id,
		        COALESCE(u.full_name, u.first_name || ' ' || u.last_name),
		        u.email, i.total_amount, i.due_date, i.kid_number,
		        i.pdf_data, i.sent_at
		   FROM invoices i
		   JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&invoiceNumber, &userID, &memberName, &memberEmail,
		&total, &dueDate, &kid, &pdfData, &alreadySent); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "invoice not found")
			return
		}
		h.log.Error().Err(err).Msg("load invoice for send")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if alreadySent != nil {
		Error(w, http.StatusConflict, "invoice already sent")
		return
	}
	if h.email == nil {
		Error(w, http.StatusServiceUnavailable, "email delivery not configured")
		return
	}
	if memberEmail == "" {
		Error(w, http.StatusBadRequest, "member has no email address on file")
		return
	}
	if pdfData == nil {
		Error(w, http.StatusInternalServerError, "invoice has no stored PDF")
		return
	}

	var clubName, bankAccount string
	_ = h.db.QueryRow(ctx,
		`SELECT name, COALESCE(bank_account, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &bankAccount)

	locale := email.DetectLocale(r)
	subject := email.InvoiceSubject(locale, clubName, invoiceNumber)
	htmlBody := email.InvoiceBody(locale, memberName, clubName, invoiceNumber, dueDate, total, kid, bankAccount)
	filename := fmt.Sprintf("faktura-%d.pdf", invoiceNumber)
	if err := h.email.SendWithAttachment(ctx, memberEmail, subject, htmlBody, filename, pdfData); err != nil {
		h.log.Error().Err(err).Str("email", memberEmail).Msg("send invoice email")
		Error(w, http.StatusBadGateway, "failed to send email")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET sent_at = now() WHERE id = $1`, invoiceID,
	); err != nil {
		h.log.Error().Err(err).Msg("stamp sent_at")
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceEmailed, "invoice", invoiceID,
			map[string]any{"email": memberEmail, "invoice_number": invoiceNumber})
	}

	JSON(w, http.StatusOK, map[string]any{"id": invoiceID, "sent": true})
}

// HandleDeleteDraftInvoice permanently removes an unsent invoice.
// Refuses to touch sent invoices — voiding sent fakturas is a separate
// concern (will arrive with the planned status column).
func (h *InvoiceHandler) HandleDeleteDraftInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM invoices
		  WHERE id = $1 AND club_id = $2 AND sent_at IS NULL`,
		invoiceID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("delete draft invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "draft invoice not found (already sent or wrong club)")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", invoiceID,
			map[string]any{"deleted_draft": true})
	}
	w.WriteHeader(http.StatusNoContent)
}
