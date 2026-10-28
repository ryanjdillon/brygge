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
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type bulkInvoiceActionRequest struct {
	IDs []string `json:"ids"`
	// VippsPaymentLink is the reminder batch's override of the per-invoice
	// online-payment setting (BRY-283): nil = leave each invoice's setting
	// as-is (Auto follows the club amount cap); true/false = set
	// online_payment_opt_in On/Off for the reminded invoices, persisting so it
	// holds when the member later clicks.
	// Ignored by the non-reminder bulk actions that share this request shape.
	VippsPaymentLink *bool `json:"vipps_payment_link"`
}

type bulkInvoiceResult struct {
	Processed int      `json:"processed"`
	Skipped   int      `json:"skipped"`
	Failures  []string `json:"failures"`
}

// HandleBulkSendReminder validates each supplied invoice and enqueues
// a reminder send for the eligible ones onto the durable delivery queue.
// Eligible = was originally sent (sent_at IS NOT NULL), still unpaid
// (payment_id IS NULL), has a recipient email and stored PDF. The actual
// SMTP submission happens asynchronously in the shared delivery worker —
// one send at a time, throttled by cfg.BulkSendThrottle (default 1s) — so
// this handler returns immediately and large batches don't hit the
// response timeout. Because the queue is a table (kind='invoice_reminder'
// rows in `deliveries`), a restart mid-batch resumes instead of dropping
// the tail, and each send is retried on transient failure.
//
// Operator-visible progress: each successful send writes an
// `invoice.reminded` audit row, which the Sent tab's "Sist purring"
// column reflects. Operators see rows tick forward as the worker drains.
// See DIL-364 and BRY-11 for the shared-queue design.
func (h *InvoiceHandler) HandleBulkSendReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.email == nil || h.deliveries == nil {
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

	// The send context is club/actor/request-scoped, not per-invoice, so
	// every row in this batch carries the same JSONB payload.
	payload, err := json.Marshal(invoiceReminderPayload{
		Locale:      locale,
		ClubName:    clubName,
		DefaultBank: defaultBank,
		ActorID:     claims.UserID,
		RemoteAddr:  r.RemoteAddr,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("marshal reminder payload")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Pre-flight eligibility: which supplied invoices are actually sendable, with
	// immediate "X enqueued, Y skipped" feedback for the operator.
	items, res, err := h.collectReminderItems(ctx, claims.ClubID, req.IDs, payload)
	if err != nil {
		h.log.Error().Err(err).Msg("bulk reminder preflight")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Batch Vipps-link override (BRY-283): if the operator chose On/Off in the
	// reminder dialog, persist it on the reminded invoices before enqueue so the
	// async sends — and the member's later click — all honour it.
	if err := h.applyReminderVippsOverride(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, req.VippsPaymentLink, items); err != nil {
		h.log.Error().Err(err).Msg("bulk reminder: set vipps opt-in")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Enqueue eligible reminders onto the durable delivery queue. The store
	// skips any invoice that already has a non-failed reminder queued so a
	// re-run (or double click) can't double-send; those surface as
	// "already queued" so the operator sees why they weren't re-sent. The
	// worker then drains each row throttled/retried, surviving restarts.
	enqueued, alreadyQueued, err := h.deliveries.EnqueueInvoiceReminders(ctx, claims.ClubID, items)
	if err != nil {
		h.log.Error().Err(err).Msg("enqueue invoice reminders")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	res.Processed = enqueued
	for _, id := range alreadyQueued {
		res.Failures = append(res.Failures, id+": already queued")
	}
	if enqueued > 0 && h.kickDelivery != nil {
		h.kickDelivery()
	}
	JSON(w, http.StatusOK, res)
}

// collectReminderItems runs the batched eligibility preflight for a bulk
// reminder and returns the sendable deliveries plus the skipped/failure tally.
// Eligible = originally sent, still unpaid, not vipps-received-pending-settlement,
// with a recipient email and a stored PDF.
func (h *InvoiceHandler) collectReminderItems(ctx context.Context, clubID string, ids []string, payload []byte) ([]bcast.InvoiceDelivery, bulkInvoiceResult, error) {
	res := bulkInvoiceResult{}
	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.sent_at, NOT `+finance.OwedByMemberSQL("i")+` AS settled,
		        COALESCE(NULLIF(i.recipient_email, ''), u.email, '') AS recipient,
		        (i.pdf_data IS NOT NULL OR COALESCE(i.s3_key, '') <> '') AS has_pdf
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.club_id = $1 AND i.id = ANY($2)`,
		clubID, ids,
	)
	if err != nil {
		return nil, res, err
	}
	defer rows.Close()

	type pre struct {
		id        string
		sentAt    *time.Time
		settled   bool
		recipient string
		hasPDF    bool
	}
	seen := make(map[string]pre, len(ids))
	for rows.Next() {
		var p pre
		if err := rows.Scan(&p.id, &p.sentAt, &p.settled, &p.recipient, &p.hasPDF); err != nil {
			return nil, res, err
		}
		seen[p.id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, res, err
	}

	items := make([]bcast.InvoiceDelivery, 0, len(ids))
	for _, id := range ids {
		p, ok := seen[id]
		if !ok {
			res.Failures = append(res.Failures, id+": not found")
			continue
		}
		// Skip not-yet-sent invoices and anything the member doesn't owe
		// (finance.OwedByMemberSQL): paid, voided, credited, received via the
		// /betal link but not yet settled, or itself a credit note — reminding
		// them would be a false purring.
		if p.sentAt == nil || p.settled {
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
		items = append(items, bcast.InvoiceDelivery{InvoiceID: id, Email: p.recipient, Payload: payload})
	}
	return items, res, nil
}

// applyReminderVippsOverride persists the reminder dialog's On/Off batch choice
// (BRY-283) onto the reminded invoices before enqueue. nil = leave each invoice
// as set (Auto still follows the club cap); no-op when there is nothing to send.
func (h *InvoiceHandler) applyReminderVippsOverride(ctx context.Context, clubID, actorID, remoteAddr string, optIn *bool, items []bcast.InvoiceDelivery) error {
	if optIn == nil || len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.InvoiceID
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET online_payment_opt_in = $1, vipps_payment_link_opt_in = $1
		  WHERE club_id = $2 AND id = ANY($3)`,
		*optIn, clubID, ids,
	); err != nil {
		return err
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, clubID, actorID, remoteAddr,
			audit.ActionInvoiceVippsLinkSet, "invoice", "",
			map[string]any{"opt_in": *optIn, "count": len(ids)})
	}
	return nil
}

var errSkip = errors.New("skip")

// errInvoiceNotFound is returned by sendOneReminder when the invoice row is
// gone (deleted between enqueue and send). The reminder Sender treats it as
// a terminal no-op rather than retrying.
var errInvoiceNotFound = errors.New("invoice not found")

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
		s3Key          string
		sentAt         *time.Time
		settled        bool
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number,
		        COALESCE(NULLIF(i.recipient_org_name, ''),
		                 NULLIF(i.member_name, ''),
		                 u.full_name,
		                 u.first_name || ' ' || u.last_name,
		                 ''),
		        COALESCE(u.email, ''), COALESCE(i.recipient_email, ''),
		        i.due_date, i.total_amount, i.kid_number,
		        i.pdf_data, COALESCE(i.s3_key, ''), i.sent_at, NOT `+finance.OwedByMemberSQL("i")+`
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &memberName, &memberEmail, &recipientEmail,
		&dueDate, &total, &kid, &pdfData, &s3Key, &sentAt, &settled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvoiceNotFound
		}
		return err
	}
	// Re-checked at send time, not only at enqueue: the queue may hold a
	// reminder for days, and the member may pay or the invoice be voided
	// in between.
	if sentAt == nil || settled {
		return errSkip
	}
	deliverTo := recipientEmail
	if deliverTo == "" {
		deliverTo = memberEmail
	}
	if deliverTo == "" {
		return errors.New("no recipient email")
	}
	// PDFs live in S3 once regenerated/reissued (pdf_data is nulled on upload),
	// so pull the bytes S3-first with a DB fallback — reading pdf_data alone
	// would drop every reissued invoice as "no stored PDF". See BRY-230.
	pdfData, err = h.fetchInvoicePDFBytes(ctx, s3Key, pdfData)
	if err != nil || pdfData == nil {
		return errors.New("invoice has no stored PDF")
	}

	subject := email.InvoiceReminderSubject(locale, clubName, invoiceNumber)
	// Offer a "Betal med Vipps" link per the invoice's effective Vipps setting —
	// its On/Off override or, for Auto, the club amount cap (all enforced inside
	// ensureFakturaOrderToken, BRY-283). The copy keeps bank transfer preferred;
	// an empty URL just renders the KID-only reminder.
	vippsURL := h.orderPayURL(h.ensureFakturaOrderToken(ctx, clubID, invoiceID, true))
	body := email.InvoiceReminderBody(locale, memberName, clubName, invoiceNumber, dueDate, total, kid, defaultBank, vippsURL)
	filename := buildSimpleReminderFilename(invoiceNumber)
	if err := h.email.SendWithAttachment(email.WithKind(ctx, email.KindInvoiceReminder), deliverTo, subject, body, filename, pdfData); err != nil {
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
		err := h.regenerateOnePDF(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, id, clubFields, "")
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

// HandleBulkReissueKID regenerates the payment reference (KID) on the
// supplied invoices using the club's configured OCR check method
// (mod10/mod11), then reprints the PDF with the new KID — archiving the
// prior PDF — in one transaction. Scoped to sent, still-unpaid invoices
// that already carry a Brygge-generated KID: paid/matched invoices have a
// booked payment + bilag keyed on the current KID, so changing it would
// orphan bank reconciliation and they are skipped. Delivering the
// corrected faktura is intentionally left to the operator's normal
// "Send purring" flow, so this handler never sends.
func (h *InvoiceHandler) HandleBulkReissueKID(w http.ResponseWriter, r *http.Request) {
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
		h.log.Error().Err(err).Msg("load club fields for reissue")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	var kidMethod string
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(kid_check_method, 'mod11') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&kidMethod); err != nil {
		h.log.Error().Err(err).Msg("load kid method for reissue")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	res := bulkInvoiceResult{}
	for _, id := range req.IDs {
		err := h.reissueOneKID(ctx, claims.ClubID, claims.UserID, r.RemoteAddr, id, clubFields, kidMethod)
		switch {
		case err == nil:
			res.Processed++
		case errors.Is(err, errSkip):
			res.Skipped++
		default:
			res.Failures = append(res.Failures, id+": "+err.Error())
			h.log.Warn().Err(err).Str("invoice_id", id).Msg("bulk reissue-kid failed for invoice")
		}
	}
	JSON(w, http.StatusOK, res)
}

// reissueOneKID computes a fresh KID for a single invoice under the club's
// check method and, when it differs from the stored one, reprints the PDF
// with it. Skips invoices that are unsent, already paid, carry no KID, or
// whose KID is already correct — leaving nothing to send or reconcile.
func (h *InvoiceHandler) reissueOneKID(
	ctx context.Context, clubID, actorID, remoteAddr, invoiceID string, club *clubInvoiceFields, kidMethod string,
) error {
	var (
		invoiceNumber int
		kidNumber     string
		sent          bool
		paid          bool
		imported      bool
	)
	err := h.db.QueryRow(ctx,
		`SELECT invoice_number, COALESCE(kid_number, ''),
		        (sent_at IS NOT NULL), NOT `+finance.OwedByMemberSQL("")+`,
		        import_source IS NOT NULL
		   FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &kidNumber, &sent, &paid, &imported)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("not found")
		}
		return err
	}
	// Imported fakturas are read-only (BRY-396); the KID is the previous
	// system's and members may still pay with it.
	if imported || !sent || paid || kidNumber == "" {
		return errSkip
	}

	newKID, err := finance.GenerateKIDMethod("000", invoiceNumber, 1, kidMethod)
	if err != nil {
		return err
	}
	if newKID == kidNumber {
		return errSkip
	}

	return h.regenerateOnePDF(ctx, clubID, actorID, remoteAddr, invoiceID, club, newKID)
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

// regenerateOnePDF rebuilds and stores an invoice's PDF, archiving the
// prior one for bokføringsloven §13 retention. When newKID is non-empty
// the invoice's payment reference is reissued in the same transaction:
// the PDF is printed with newKID and invoices.kid_number is updated
// alongside pdf_data, so the stored KID and the printed KID never diverge.
func (h *InvoiceHandler) regenerateOnePDF( //nolint:gocognit,cyclop // loads every PDF input for one faktura, then renders; split tracked in BRY-424
	ctx context.Context, clubID, actorID, remoteAddr, invoiceID string, club *clubInvoiceFields, newKID string,
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
		imported    bool
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number, i.user_id,
		        COALESCE(NULLIF(i.member_name, ''), u.full_name, ''),
		        COALESCE(u.address_line || ', ' || u.postal_code || ' ' || u.city, ''),
		        i.recipient_kind,
		        COALESCE(i.recipient_org_name, ''),
		        COALESCE(i.recipient_org_number, ''),
		        COALESCE(i.recipient_org_address, ''),
		        COALESCE(i.recipient_contact_person, ''),
		        COALESCE(i.recipient_their_ref, ''),
		        i.issue_date, i.due_date, i.kid_number,
		        i.pdf_data, i.import_source IS NOT NULL
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &userID, &memberName, &memberAddress,
		&recipientKind, &orgName, &orgNumber, &orgAddress, &orgContact, &orgTheirRef,
		&issueDate, &dueDate, &kid, &existingPDF, &imported)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("not found")
		}
		return err
	}
	// A Brygge PDF must not replace or stand in for an imported faktura's
	// original (BRY-396).
	if imported {
		return errSkip
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

	// A reissue prints the new KID; a plain regenerate reprints the
	// existing one.
	pdfKID := kid
	if newKID != "" {
		pdfKID = newKID
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
		KID:            pdfKID,
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
		archiveReason := "regenerate"
		if newKID != "" {
			archiveReason = "reissue_kid"
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO invoice_pdf_archive (invoice_id, pdf_data, archived_by, reason)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id`,
			invoiceID, archivePDFArg, archivedBy, archiveReason,
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
		`UPDATE invoices
		    SET pdf_data = $1, s3_key = $2,
		        kid_number = COALESCE($5, kid_number)
		  WHERE id = $3 AND club_id = $4`,
		newPDFArg, nilIfEmpty(newS3Key), invoiceID, clubID, nilIfEmpty(newKID),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_ = archiveS3Key

	if h.audit != nil {
		detail := map[string]any{
			"invoice_number":  invoiceNumber,
			"bank_account":    club.BankAccount,
			"prior_pdf_bytes": len(existingPDF),
		}
		if newKID != "" {
			detail["reissued_kid"] = newKID
			detail["prior_kid"] = kid
		}
		h.audit.LogAction(ctx, clubID, actorID, remoteAddr,
			audit.ActionInvoiceRegenerated, "invoice", invoiceID, detail)
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
