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
		subject := fmt.Sprintf("Faktura #%d fra %s", invoiceSeq, clubName)
		htmlBody := fmt.Sprintf(
			`<p>Hei %s,</p><p>Vedlagt finner du faktura #%d.</p><p>Forfallsdato: %s<br>Beløp: kr %.2f<br>KID: %s<br>Kontonummer: %s</p><p>Med vennlig hilsen,<br>%s</p>`,
			memberName, invoiceSeq, dueDate.Format("02.01.2006"), total, kid, bankAccount, clubName,
		)
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
