package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type bulkInvoiceActionRequest struct {
	IDs []string `json:"ids"`
}

type bulkInvoiceResult struct {
	Processed int      `json:"processed"`
	Skipped   int      `json:"skipped"`
	Failures  []string `json:"failures"`
}

// HandleBulkSendReminder emails a Norwegian purring (reminder) for
// each supplied invoice that is still unpaid (payment_id IS NULL) and
// has been sent at least once (sent_at IS NOT NULL). The reminder
// reuses the stored PDF attachment. Already-paid or never-sent rows
// are silently skipped — counted toward `skipped` so the UI can
// report "32 sent, 6 skipped (already paid)". See DIL-364.
func (h *InvoiceHandler) HandleBulkSendReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.email == nil {
		Error(w, http.StatusServiceUnavailable, "email delivery not configured")
		return
	}
	var req bulkInvoiceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		Error(w, http.StatusBadRequest, "ids is required")
		return
	}

	var clubName, defaultBank string
	_ = h.db.QueryRow(ctx,
		`SELECT name, COALESCE(bank_account, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &defaultBank)
	locale := email.DetectLocale(r)

	res := bulkInvoiceResult{}
	for _, id := range req.IDs {
		err := h.sendOneReminder(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, id, clubName, defaultBank, locale)
		switch {
		case err == nil:
			res.Processed++
		case errors.Is(err, errSkip):
			res.Skipped++
		default:
			res.Failures = append(res.Failures, id+": "+err.Error())
			h.log.Warn().Err(err).Str("invoice_id", id).Msg("bulk reminder failed for invoice")
		}
	}
	JSON(w, http.StatusOK, res)
}

var errSkip = errors.New("skip")

func (h *InvoiceHandler) sendOneReminder(
	ctx context.Context, clubID, actorID, remoteAddr, invoiceID, clubName, defaultBank, locale string,
) error {
	var (
		invoiceNumber int
		memberName    string
		memberEmail   string
		recipientEmail string
		dueDate       time.Time
		total         float64
		kid           string
		pdfData       []byte
		sentAt        *time.Time
		paymentID     *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number,
		        COALESCE(NULLIF(i.recipient_org_name, ''),
		                 u.full_name,
		                 u.first_name || ' ' || u.last_name,
		                 ''),
		        COALESCE(u.email, ''), COALESCE(i.recipient_email, ''),
		        i.due_date, i.total_amount, i.kid_number,
		        i.pdf_data, i.sent_at, i.payment_id
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &memberName, &memberEmail, &recipientEmail,
		&dueDate, &total, &kid, &pdfData, &sentAt, &paymentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("not found")
		}
		return err
	}
	if sentAt == nil || paymentID != nil {
		return errSkip
	}
	deliverTo := recipientEmail
	if deliverTo == "" {
		deliverTo = memberEmail
	}
	if deliverTo == "" {
		return errors.New("no recipient email")
	}
	if pdfData == nil {
		return errors.New("invoice has no stored PDF")
	}

	subject := email.InvoiceReminderSubject(locale, clubName, invoiceNumber)
	body := email.InvoiceReminderBody(locale, memberName, clubName, invoiceNumber, dueDate, total, kid, defaultBank)
	filename := buildSimpleReminderFilename(invoiceNumber)
	if err := h.email.SendWithAttachment(ctx, deliverTo, subject, body, filename, pdfData); err != nil {
		return err
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, clubID, actorID, remoteAddr,
			audit.ActionInvoiceReminded, "invoice", invoiceID,
			map[string]any{"email": deliverTo, "invoice_number": invoiceNumber})
	}
	return nil
}

func buildSimpleReminderFilename(invoiceNumber int) string {
	// Same name as the original PDF so e-mail clients thread the
	// reminder with the original delivery: "Faktura-42.pdf".
	return fmt.Sprintf("Faktura-%d.pdf", invoiceNumber)
}

// HandleBulkRegeneratePDF rebuilds invoices.pdf_data for each supplied
// ID using the **current** club bank-account default. Invoice number,
// KID, dates, recipient, and line items are unchanged. No email is
// sent — the operator follows up with the purring button when ready.
// See DIL-364.
func (h *InvoiceHandler) HandleBulkRegeneratePDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req bulkInvoiceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		Error(w, http.StatusBadRequest, "ids is required")
		return
	}

	clubFields, err := loadClubInvoiceFields(ctx, h.db, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("load club fields for regen")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	res := bulkInvoiceResult{}
	for _, id := range req.IDs {
		err := h.regenerateOnePDF(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, id, clubFields)
		switch {
		case err == nil:
			res.Processed++
		case errors.Is(err, errSkip):
			res.Skipped++
		default:
			res.Failures = append(res.Failures, id+": "+err.Error())
			h.log.Warn().Err(err).Str("invoice_id", id).Msg("bulk regenerate failed for invoice")
		}
	}
	JSON(w, http.StatusOK, res)
}

type clubInvoiceFields struct {
	Name           string
	OrgNumber      string
	Address        string
	Website        string
	TreasurerEmail string
	LogoData       []byte
	LogoMIME       string
	BankAccount    string
}

// loadClubInvoiceFields resolves the same seller-side fields the
// create-invoice handler uses, including the bank account (which now
// prefers club_bank_accounts default-for-invoices, falling back to the
// legacy clubs.bank_account column for one release).
func loadClubInvoiceFields(ctx context.Context, db pgxQuerier, clubID string) (*clubInvoiceFields, error) {
	var f clubInvoiceFields
	err := db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''),
		        COALESCE(
		          (SELECT account_number FROM club_bank_accounts
		            WHERE club_id = clubs.id AND is_default_for_invoices AND archived_at IS NULL
		            LIMIT 1),
		          bank_account, ''),
		        COALESCE(website_url, ''), COALESCE(treasurer_email, ''),
		        faktura_logo_data, COALESCE(faktura_logo_mime, '')
		   FROM clubs WHERE id = $1`,
		clubID,
	).Scan(&f.Name, &f.OrgNumber, &f.Address, &f.BankAccount, &f.Website, &f.TreasurerEmail, &f.LogoData, &f.LogoMIME)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// pgxQuerier is a tiny subset of pgxpool.Pool / pgx.Tx used by helpers
// that want to be tx-agnostic.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (h *InvoiceHandler) regenerateOnePDF(
	ctx context.Context, clubID, actorID, remoteAddr, invoiceID string, club *clubInvoiceFields,
) error {
	var (
		invoiceNumber  int
		userID         *string
		memberName     string
		memberAddress  string
		recipientKind  string
		orgName        string
		orgNumber      string
		orgAddress     string
		orgContact     string
		orgTheirRef    string
		issueDate      time.Time
		dueDate        time.Time
		kid            string
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number, i.user_id,
		        COALESCE(u.full_name, ''),
		        COALESCE(u.address_line || ', ' || u.postal_code || ' ' || u.city, ''),
		        i.recipient_kind,
		        COALESCE(i.recipient_org_name, ''),
		        COALESCE(i.recipient_org_number, ''),
		        COALESCE(i.recipient_org_address, ''),
		        COALESCE(i.recipient_contact_person, ''),
		        COALESCE(i.recipient_their_ref, ''),
		        i.issue_date, i.due_date, i.kid_number
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &userID, &memberName, &memberAddress,
		&recipientKind, &orgName, &orgNumber, &orgAddress, &orgContact, &orgTheirRef,
		&issueDate, &dueDate, &kid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("not found")
		}
		return err
	}

	// Build the buyer/recipient block exactly as it was on issue: an
	// orgName non-empty implies org-recipient; otherwise the linked
	// user's name + address (or the previously-stored override on the
	// invoice row — which we don't currently keep, so the user link is
	// the source of truth here).
	pdfMemberName := memberName
	pdfMemberAddress := memberAddress
	var orgRecipient *finance.OrgRecipient
	if recipientKind == "organization" && orgName != "" {
		orgRecipient = &finance.OrgRecipient{
			Name:          orgName,
			OrgNumber:     orgNumber,
			Address:       orgAddress,
			ContactPerson: orgContact,
			TheirRef:      orgTheirRef,
		}
		if orgRecipient.Address != "" {
			pdfMemberAddress = orgRecipient.Address
		}
	}

	lines, err := h.loadInvoiceLinesForPDF(ctx, invoiceID)
	if err != nil {
		return err
	}

	inv := finance.Invoice{
		ClubName:       club.Name,
		OrgNumber:      club.OrgNumber,
		ClubAddress:    club.Address,
		Website:        club.Website,
		TreasurerEmail: club.TreasurerEmail,
		LogoData:       club.LogoData,
		LogoMIME:       club.LogoMIME,
		MemberName:     pdfMemberName,
		MemberAddress:  pdfMemberAddress,
		OrgRecipient:   orgRecipient,
		InvoiceNumber:  invoiceNumber,
		IssueDate:      issueDate,
		DueDate:        dueDate,
		KID:            kid,
		BankAccount:    club.BankAccount,
		Lines:          lines,
	}
	pdfData, err := finance.GeneratePDF(inv)
	if err != nil {
		return err
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET pdf_data = $1 WHERE id = $2 AND club_id = $3`,
		pdfData, invoiceID, clubID,
	); err != nil {
		return err
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, clubID, actorID, remoteAddr,
			audit.ActionInvoiceRegenerated, "invoice", invoiceID,
			map[string]any{
				"invoice_number": invoiceNumber,
				"bank_account":   club.BankAccount,
			})
	}
	return nil
}

func (h *InvoiceHandler) loadInvoiceLinesForPDF(ctx context.Context, invoiceID string) ([]finance.InvoiceLine, error) {
	rows, err := h.db.Query(ctx,
		`SELECT description, COALESCE(sub_description, ''), quantity, unit_price
		   FROM invoice_lines
		  WHERE invoice_id = $1
		  ORDER BY id`,
		invoiceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []finance.InvoiceLine
	for rows.Next() {
		var l finance.InvoiceLine
		if err := rows.Scan(&l.Description, &l.SubDescription, &l.Quantity, &l.UnitPrice); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

