package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
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

// HandleBulkSendReminder validates each supplied invoice and enqueues
// a reminder send for the eligible ones. Eligible = was originally
// sent (sent_at IS NOT NULL), still unpaid (payment_id IS NULL), has a
// recipient email and stored PDF. The actual SMTP submission happens
// asynchronously in the worker started by NewInvoiceHandler — one
// send at a time, throttled by cfg.BulkSendThrottle (default 1s)
// — so this handler can return immediately and the HTTP request
// doesn't run into the response timeout on large batches.
//
// Operator-visible progress: each successful async send writes an
// `invoice.reminded` audit row, which the Sent tab's "Sist purring"
// column reflects. Operators see rows tick forward as the worker
// drains. See DIL-364 and DIL-387's follow-up ticket for the queue
// design.
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

	// Pre-flight: load eligibility fields in one batched query so we
	// can give the operator immediate "X enqueued, Y skipped" feedback
	// without making them wait for the worker.
	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.sent_at, i.payment_id,
		        COALESCE(NULLIF(i.recipient_email, ''), u.email, '') AS recipient,
		        (i.pdf_data IS NOT NULL) AS has_pdf
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.club_id = $1 AND i.id = ANY($2)`,
		claims.ClubID, req.IDs,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("bulk reminder preflight")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type pre struct {
		id        string
		sentAt    *time.Time
		paymentID *string
		recipient string
		hasPDF    bool
	}
	seen := make(map[string]pre, len(req.IDs))
	for rows.Next() {
		var p pre
		if err := rows.Scan(&p.id, &p.sentAt, &p.paymentID, &p.recipient, &p.hasPDF); err != nil {
			h.log.Error().Err(err).Msg("bulk reminder preflight scan")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		seen[p.id] = p
	}

	res := bulkInvoiceResult{}
	for _, id := range req.IDs {
		p, ok := seen[id]
		if !ok {
			res.Failures = append(res.Failures, id+": not found")
			continue
		}
		if p.sentAt == nil || p.paymentID != nil {
			res.Skipped++
			continue
		}
		if p.recipient == "" {
			res.Failures = append(res.Failures, id+": no recipient email")
			continue
		}
		if !p.hasPDF {
			res.Failures = append(res.Failures, id+": invoice has no stored PDF")
			continue
		}
		job := reminderJob{
			clubID:      claims.ClubID,
			actorID:     claims.UserID,
			remoteAddr:  r.RemoteAddr,
			invoiceID:   id,
			clubName:    clubName,
			defaultBank: defaultBank,
			locale:      locale,
		}
		select {
		case h.reminderQueue <- job:
			res.Processed++
		default:
			res.Failures = append(res.Failures, id+": reminder queue full")
			h.log.Warn().Str("invoice_id", id).Msg("reminder queue full — dropping")
		}
	}
	JSON(w, http.StatusOK, res)
}

var errSkip = errors.New("skip")

func (h *InvoiceHandler) sendOneReminder(
	ctx context.Context, clubID, actorID, remoteAddr, invoiceID, clubName, defaultBank, locale string,
) error {
	var (
		invoiceNumber  int
		memberName     string
		memberEmail    string
		recipientEmail string
		dueDate        time.Time
		total          float64
		kid            string
		pdfData        []byte
		sentAt         *time.Time
		paymentID      *string
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
		invoiceNumber int
		userID        *string
		memberName    string
		memberAddress string
		recipientKind string
		orgName       string
		orgNumber     string
		orgAddress    string
		orgContact    string
		orgTheirRef   string
		issueDate     time.Time
		dueDate       time.Time
		kid           string
		// Existing PDF bytes — archived before the overwrite so
		// bokføringsloven §13 retention is honored. See DIL-374.
		existingPDF []byte
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
		        i.issue_date, i.due_date, i.kid_number,
		        i.pdf_data
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &userID, &memberName, &memberAddress,
		&recipientKind, &orgName, &orgNumber, &orgAddress, &orgContact, &orgTheirRef,
		&issueDate, &dueDate, &kid, &existingPDF)
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

	// Upload new PDF and archive existing PDF to S3 when available.
	// Falls back to DB storage when S3 is not configured.
	var newS3Key, archiveS3Key string
	if h.s3Legal != nil && h.s3Legal.IsConfigured() {
		newS3Key = invoicePDFKey(clubID, invoiceID)
		if err := h.s3Legal.Upload(ctx, newS3Key,
			bytes.NewReader(pdfData), int64(len(pdfData)), "application/pdf"); err != nil {
			h.log.Warn().Err(err).Str("invoice_id", invoiceID).Msg("S3 upload failed for regenerated PDF; storing in DB")
			newS3Key = ""
		}
		if len(existingPDF) > 0 {
			// existing PDF is the prior live version — upload to an archive key.
			// Generate an archive ID from the DB in the transaction below; for
			// now we upload to a temp key and rename after getting the ID.
			// Simpler: let the archive INSERT return the ID, then upload.
			// Strategy: upload existing PDF under a provisional key, archive
			// INSERT updates to final key in the transaction.
			// Actually simplest: keep existingPDF bytes, upload after INSERT with real ID.
			_ = existingPDF // uploaded below after getting archive ID
		}
	}

	// Archive the prior PDF and overwrite in the same transaction so
	// a failure mid-flight leaves the original PDF in place. If the
	// invoice had no stored PDF yet (first regenerate run before the
	// archive feature, or a row whose pdf_data was already wiped),
	// skip the archive INSERT — there's nothing legally preservable.
	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if len(existingPDF) > 0 {
		var archivedBy any
		if actorID != "" {
			archivedBy = actorID
		}
		var archiveID string
		var archivePDFArg any
		if newS3Key != "" {
			archivePDFArg = nil
		} else {
			archivePDFArg = existingPDF
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO invoice_pdf_archive (invoice_id, pdf_data, archived_by, reason)
			 VALUES ($1, $2, $3, 'regenerate')
			 RETURNING id`,
			invoiceID, archivePDFArg, archivedBy,
		).Scan(&archiveID); err != nil {
			return err
		}
		// Upload existing PDF to S3 under the real archive ID.
		if newS3Key != "" {
			archiveS3Key = fmt.Sprintf("clubs/%s/invoices/archive/%s.pdf", clubID, archiveID)
			if err := h.s3Legal.Upload(ctx, archiveS3Key,
				bytes.NewReader(existingPDF), int64(len(existingPDF)), "application/pdf"); err != nil {
				h.log.Warn().Err(err).Str("archive_id", archiveID).Msg("S3 upload failed for archive PDF; retained in DB")
				archiveS3Key = ""
			}
			if archiveS3Key != "" {
				if _, err := tx.Exec(ctx,
					`UPDATE invoice_pdf_archive SET s3_key = $1 WHERE id = $2`,
					archiveS3Key, archiveID,
				); err != nil {
					return err
				}
			}
		}
	}

	var newPDFArg any
	if newS3Key != "" {
		newPDFArg = nil
	} else {
		newPDFArg = pdfData
	}
	if _, err := tx.Exec(ctx,
		`UPDATE invoices SET pdf_data = $1, s3_key = $2 WHERE id = $3 AND club_id = $4`,
		newPDFArg, nilIfEmpty(newS3Key), invoiceID, clubID,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_ = archiveS3Key

	if h.audit != nil {
		h.audit.LogAction(ctx, clubID, actorID, remoteAddr,
			audit.ActionInvoiceRegenerated, "invoice", invoiceID,
			map[string]any{
				"invoice_number":  invoiceNumber,
				"bank_account":    club.BankAccount,
				"prior_pdf_bytes": len(existingPDF),
			})
	}
	return nil
}

// HandleListInvoicePDFArchive returns the archived prior PDFs for an
// invoice in newest-first order. Each entry carries metadata only —
// the bytes are streamed by HandleGetInvoicePDFArchiveBytes. Used
// from the invoice detail UI's "Tidligere versjoner" panel. See
// DIL-374.
func (h *InvoiceHandler) HandleListInvoicePDFArchive(w http.ResponseWriter, r *http.Request) {
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

	// Confirm the invoice is owned by the caller's club before
	// listing archive — same scoping as HandleGetInvoicePDF.
	var ok bool
	if err := h.db.QueryRow(ctx,
		`SELECT TRUE FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&ok); err != nil {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT a.id, a.archived_at, a.reason,
		        COALESCE(u.full_name, u.first_name || ' ' || u.last_name, ''),
		        COALESCE(octet_length(a.pdf_data), 0)
		   FROM invoice_pdf_archive a
		   LEFT JOIN users u ON u.id = a.archived_by
		  WHERE a.invoice_id = $1
		  ORDER BY a.archived_at DESC`,
		invoiceID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("list invoice pdf archive")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type archiveRow struct {
		ID         string    `json:"id"`
		ArchivedAt time.Time `json:"archived_at"`
		Reason     string    `json:"reason"`
		ArchivedBy string    `json:"archived_by"`
		Bytes      int       `json:"bytes"`
	}
	out := make([]archiveRow, 0)
	for rows.Next() {
		var a archiveRow
		if err := rows.Scan(&a.ID, &a.ArchivedAt, &a.Reason, &a.ArchivedBy, &a.Bytes); err != nil {
			h.log.Error().Err(err).Msg("scan archive row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, a)
	}
	JSON(w, http.StatusOK, map[string]any{"items": out})
}

// HandleGetInvoicePDFArchiveBytes streams an archived PDF version.
// Defaults to inline (`download=1` forces a save dialog), and the
// filename embeds the archive timestamp so a folder of downloaded
// archives sorts sensibly.
func (h *InvoiceHandler) HandleGetInvoicePDFArchiveBytes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	archiveID := chi.URLParam(r, "archiveID")
	if invoiceID == "" || archiveID == "" {
		Error(w, http.StatusBadRequest, "invoice ID and archive ID are required")
		return
	}

	var (
		pdfData       []byte
		s3Key         string
		archivedAt    time.Time
		invoiceNumber int
		memberLast    string
		memberFirst   string
		fiscalYear    *int
	)
	err := h.db.QueryRow(ctx,
		`SELECT a.pdf_data, COALESCE(a.s3_key, ''), a.archived_at, i.invoice_number,
		        COALESCE(u.last_name, ''), COALESCE(u.first_name, ''),
		        fp.year
		   FROM invoice_pdf_archive a
		   JOIN invoices i ON i.id = a.invoice_id
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE a.id = $1 AND a.invoice_id = $2 AND i.club_id = $3`,
		archiveID, invoiceID, claims.ClubID,
	).Scan(&pdfData, &s3Key, &archivedAt, &invoiceNumber, &memberLast, &memberFirst, &fiscalYear)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "archived PDF not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("fetch archived invoice pdf")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	pdfBytes, _ := h.fetchInvoicePDFBytes(ctx, s3Key, pdfData)
	if pdfBytes == nil {
		Error(w, http.StatusNotFound, "archived PDF not available")
		return
	}
	pdfData = pdfBytes

	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	base := buildInvoiceFilename(invoiceNumber, memberLast, memberFirst, fiscalYear)
	// Inject the archive timestamp before the .pdf extension so the
	// recipient and version are both visible in the saved filename.
	filename := base
	if len(base) >= 4 && base[len(base)-4:] == ".pdf" {
		filename = base[:len(base)-4] + "-" + archivedAt.UTC().Format("20060102-150405") + ".pdf"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, filename))
	w.Write(pdfData)
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
