package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Credit-note links (BRY-342). A credit note is an invoice with a negative
// total; linking it to the faktura it credits settles that faktura for the
// member (finance.CreditedSQL). The link only records the settlement — it
// never posts to the GL: a booked credit note already carries its own
// DR revenue / CR 1500 entry that offsets the faktura's receivable, and
// imported history has no bookings on either side.

// creditOutcome says what a link means for the member.
const (
	// creditSettledPair: the faktura was still owed, so the credit note
	// cancels it and neither is a debt any more.
	creditSettledPair = "settled_pair"
	// creditOwed: the faktura was already paid, so the club owes the member
	// the credit (refund or carry-forward).
	creditOwed = "credit_owed"
)

type creditInvoiceRow struct {
	id          string
	number      int
	total       float64
	status      string
	userID      *string
	recipientID *string
	sent        bool
	imported    bool
	creditsID   *string
	settled     bool
	paid        bool
	booked      bool
	credited    bool
	// refundDocument: a credit note (or debit faktura) a card refund
	// issued, linked to its faktura through refund_documents and never
	// linked, unlinked or voided by hand (ACC-9).
	refundDocument bool
	// refundCredited: card refunds issued credit notes for part of it, so a
	// manual credit of its whole total would credit it twice.
	refundCredited bool
}

func loadCreditInvoice(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, clubID, invoiceID string, forUpdate bool) (*creditInvoiceRow, error) {
	sql := `SELECT i.id, i.invoice_number, i.total_amount, i.status, i.user_id, i.recipient_id,
	               (i.sent_at IS NOT NULL), (i.import_source IS NOT NULL), i.credits_invoice_id,
	               ` + finance.SettledForMemberSQL("i") + `,
	               (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL),
	               EXISTS (SELECT 1 FROM journal_entries je
	                        WHERE je.club_id = i.club_id AND je.source = 'invoice_sync'
	                          AND je.source_id = i.id::text AND je.status = 'posted'),
	               ` + finance.CreditedSQL("i") + `,
	               ` + refundDocumentSQL("i") + `,
	               ` + refundCreditedSQL("i") + `
	          FROM invoices i
	         WHERE i.id = $1 AND i.club_id = $2`
	if forUpdate {
		sql += " FOR UPDATE OF i"
	}
	var r creditInvoiceRow
	err := q.QueryRow(ctx, sql, invoiceID, clubID).Scan(
		&r.id, &r.number, &r.total, &r.status, &r.userID, &r.recipientID,
		&r.sent, &r.imported, &r.creditsID, &r.settled, &r.paid, &r.booked, &r.credited, &r.refundDocument, &r.refundCredited,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// refundDocumentSQL is true for an invoice a card refund issued: a credit
// note for the refund, or the debit faktura after a failed one.
func refundDocumentSQL(alias string) string {
	return `EXISTS (SELECT 1 FROM refund_documents rdoc WHERE rdoc.credit_invoice_id = ` + alias + `.id)`
}

// refundCreditNoteSQL is true for a credit note a card refund issued, as
// opposed to the debit faktura a failed refund issues in its place.
func refundCreditNoteSQL(alias string) string {
	return `EXISTS (SELECT 1 FROM refund_documents rcn WHERE rcn.credit_invoice_id = ` + alias + `.id AND rcn.kind = 'credit')`
}

// refundCreditedSQL is true for a faktura card refunds issued credit notes
// for (refund_documents, kind credit), live or not: a manual credit of the
// whole faktura on top of them would credit it twice.
func refundCreditedSQL(alias string) string {
	return `EXISTS (SELECT 1 FROM refund_documents rcr WHERE rcr.invoice_id = ` + alias + `.id AND rcr.kind = 'credit')`
}

// refuseRefundDocument is the one refusal for linking, unlinking or voiding
// a document a card refund issued.
func refuseRefundDocument(w http.ResponseWriter) {
	creditConflict(w, "refund_document",
		"this was issued by a card refund and is linked to its faktura through the refund; it can't be linked, unlinked, voided or deleted by hand")
}

// hasPayer is true when the invoice bills a member or a billing recipient.
// Imported credit notes can have neither.
func (r *creditInvoiceRow) hasPayer() bool {
	return r.userID != nil || r.recipientID != nil
}

// samePayer is true when both invoices bill the same member, or the same
// billing recipient (organisation).
func samePayer(a, b *creditInvoiceRow) bool {
	if a.userID != nil && b.userID != nil && *a.userID == *b.userID {
		return true
	}
	return a.recipientID != nil && b.recipientID != nil && *a.recipientID == *b.recipientID
}

// payerConfirmMissing reports a cross-payer link the treasurer hasn't
// confirmed. Crossing payers is allowed when the credit note has none
// (imported history) or with confirmation. That is safe because the link
// only sets credits_invoice_id, posts nothing, and unlinking reverses it.
func payerConfirmMissing(cn *creditInvoiceRow, memberMismatch, confirmed bool) bool {
	return memberMismatch && cn.hasPayer() && !confirmed
}

// isDefaultCandidate is true when the faktura is one the same-payer,
// same-amount candidate list offers, whatever the client says it picked.
func isDefaultCandidate(cn, target *creditInvoiceRow) bool {
	return samePayer(cn, target) && amountsCancel(cn.total, target.total)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func amountsCancel(credit, invoice float64) bool {
	return math.Abs(invoice+credit) < 0.005
}

type creditCandidate struct {
	ID            string    `json:"id"`
	InvoiceNumber int       `json:"invoice_number"`
	TotalAmount   float64   `json:"total_amount"`
	IssueDate     time.Time `json:"issue_date"`
	Paid          bool      `json:"paid"`
	// Settled predicts the link's outcome: a settled faktura makes the
	// credit note credit owed to the member, an open one a settled pair.
	Settled    bool   `json:"settled"`
	Imported   bool   `json:"imported"`
	PayerName  string `json:"payer_name"`
	SameAmount bool   `json:"same_amount"`
	SamePayer  bool   `json:"same_payer"`
}

// creditSearchLimit caps the list: the treasurer narrows the manual search
// by number or name rather than scrolling every faktura of the club.
const creditSearchLimit = 25

// HandleListCreditCandidates lists the fakturas a credit note can be linked
// to: same payer, a positive total equal to the credit, live, and not
// already credited. Partial credits are not linkable (see HandleLinkCreditNote).
//
// With ?any=1 it is the manual search (BRY-393): the payer and amount
// predicates are dropped, so a credit note without a member, or one billed
// to someone else, can still be linked. Each row says whether its amount and
// payer match, so a mismatch is visible before the treasurer confirms; the
// link itself still refuses a partial credit and needs confirmation for a
// different payer. ?q= narrows either list by faktura number or payer name.
func (h *InvoiceHandler) HandleListCreditCandidates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	cn, err := loadCreditInvoice(ctx, h.db, claims.ClubID, chi.URLParam(r, "invoiceID"), false)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("credit candidates: load credit note")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cn.total >= 0 {
		Error(w, http.StatusBadRequest, "invoice is not a credit note")
		return
	}
	if cn.refundDocument {
		refuseRefundDocument(w)
		return
	}

	anyPayer := r.URL.Query().Get("any") == "1"
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	// The payer name as the faktura list shows it.
	const payerName = `COALESCE(NULLIF(i.recipient_org_name, ''), NULLIF(i.member_name, ''),
	                            u.full_name, u.first_name || ' ' || u.last_name, '')`
	const sameAmount = `abs(i.total_amount + $2) < 0.005`
	const samePayer = `COALESCE((i.user_id IS NOT NULL AND i.user_id = $3)
	                            OR (i.recipient_id IS NOT NULL AND i.recipient_id = $4), false)`
	filter := " AND " + sameAmount + " AND " + samePayer
	order := " ORDER BY i.issue_date, i.invoice_number"
	if anyPayer {
		filter = ""
		order = " ORDER BY i.issue_date DESC, i.invoice_number DESC"
	}
	args := []any{claims.ClubID, cn.total, cn.userID, cn.recipientID}
	if q != "" {
		filter += ` AND (i.invoice_number::text ILIKE $5 ESCAPE '\' OR ` + payerName + ` ILIKE $5 ESCAPE '\')`
		args = append(args, "%"+likeEscaper.Replace(q)+"%")
	}

	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, i.issue_date,
		        (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL),
		        `+finance.SettledForMemberSQL("i")+`,
		        (i.import_source IS NOT NULL),
		        `+payerName+`, `+sameAmount+`, `+samePayer+`
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.club_id = $1
		    AND i.status = 'open'
		    AND i.total_amount > 0
		    AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)
		    AND NOT `+finance.CreditedSQL("i")+`
		    AND NOT `+refundDocumentSQL("i")+`
		    AND NOT `+refundCreditedSQL("i")+filter+order+`
		  LIMIT `+strconv.Itoa(creditSearchLimit+1),
		args...,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("credit candidates: query")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := make([]creditCandidate, 0)
	for rows.Next() {
		var c creditCandidate
		if err := rows.Scan(&c.ID, &c.InvoiceNumber, &c.TotalAmount, &c.IssueDate, &c.Paid, &c.Settled,
			&c.Imported, &c.PayerName, &c.SameAmount, &c.SamePayer); err != nil {
			h.log.Error().Err(err).Msg("credit candidates: scan")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("credit candidates: iterate")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	// One row past the cap tells the UI the list was cut, so the treasurer
	// knows to narrow the search instead of assuming the faktura is missing.
	truncated := len(out) > creditSearchLimit
	if truncated {
		out = out[:creditSearchLimit]
	}
	// The UI asks before linking across payers only when the credit note
	// has one; a credit note without a payer has nobody to mismatch.
	JSON(w, http.StatusOK, map[string]any{
		"items":                 out,
		"truncated":             truncated,
		"credit_note_has_payer": cn.hasPayer(),
	})
}

// likeEscaper makes user input literal inside a LIKE pattern that uses
// ESCAPE '\'.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type linkCreditRequest struct {
	InvoiceID string `json:"invoice_id"`
	// ConfirmOtherMember is the treasurer's explicit go-ahead to settle one
	// payer's faktura with a credit note billed to another.
	ConfirmOtherMember bool `json:"confirm_other_member"`
	// ManualPick marks a faktura chosen in the manual search rather than
	// from the same-payer candidates. It is only recorded in the audit.
	ManualPick bool `json:"manual_pick"`
}

func creditConflict(w http.ResponseWriter, code, msg string) {
	JSON(w, http.StatusConflict, map[string]string{"error": msg, "code": code})
}

// HandleLinkCreditNote links a credit note to the faktura it credits. Only a
// full credit is accepted: a partial credit would leave a remaining balance
// that every "amount owed" (reminders, payables, pay-links) would have to
// net, which nothing supports yet, so it is refused with a reason instead.
func (h *InvoiceHandler) HandleLinkCreditNote(w http.ResponseWriter, r *http.Request) { //nolint:cyclop // one guard per credit-link rule, each with its own error code; split tracked in BRY-424
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	creditID := chi.URLParam(r, "invoiceID")
	var req linkCreditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice_id is required")
		return
	}
	if req.InvoiceID == creditID {
		Error(w, http.StatusBadRequest, "a credit note can't credit itself")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("link credit: begin")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock both rows so a concurrent link, void or payment can't slip in
	// between the checks and the update.
	cn, err := loadCreditInvoice(ctx, tx, claims.ClubID, creditID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "credit note not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("link credit: load credit note")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	target, err := loadCreditInvoice(ctx, tx, claims.ClubID, req.InvoiceID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "faktura not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("link credit: load faktura")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// A booked credit note may settle an imported, never-booked faktura only
	// when that faktura's receivable exists in Brygge's GL, i.e. an opening
	// balance carried over at the cutover put it on 1500. Otherwise the
	// credit note's CR 1500 would reduce a receivable that was never there.
	// The rule is the one payments obey (finance.ImportedReceivableOpenSQL),
	// with the credit note's issue date as the booking date. Nothing writes
	// source 'opening_balance' yet; until the cutover import does (BRY-365)
	// this exception never applies.
	importedException := cn.booked && !target.booked && target.imported
	openingReceivable := false
	if importedException {
		if err := tx.QueryRow(ctx,
			`SELECT `+finance.ImportedReceivableOpenSQL("t", "SELECT issue_date FROM invoices WHERE id = $2")+`
			   FROM invoices t
			  WHERE t.id = $1 AND t.club_id = $3`,
			target.id, cn.id, claims.ClubID,
		).Scan(&openingReceivable); err != nil {
			h.log.Error().Err(err).Msg("link credit: opening balance check")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	memberMismatch := !samePayer(cn, target)
	switch {
	case cn.refundDocument || target.refundDocument:
		refuseRefundDocument(w)
		return
	case target.refundCredited:
		creditConflict(w, "refund_credited",
			"card refunds have issued credit notes for this faktura; a manual credit would credit it twice")
		return
	case cn.total >= 0:
		Error(w, http.StatusBadRequest, "invoice is not a credit note")
		return
	case target.total <= 0:
		Error(w, http.StatusBadRequest, "only a faktura with a positive total can be credited")
		return
	case cn.status == "voided" || target.status == "voided":
		creditConflict(w, "voided", "a voided invoice can't be linked")
		return
	case !cn.sent && !cn.imported, !target.sent && !target.imported:
		creditConflict(w, "not_issued", "only sent or imported invoices can be linked")
		return
	case cn.creditsID != nil:
		creditConflict(w, "already_linked", "the credit note is already linked to a faktura")
		return
	case target.credited:
		creditConflict(w, "already_credited", "the faktura is already credited by another credit note")
		return
	case payerConfirmMissing(cn, memberMismatch, req.ConfirmOtherMember):
		creditConflict(w, "different_payer",
			"the credit note and the faktura are for different payers; send confirm_other_member to link them anyway")
		return
	case !amountsCancel(cn.total, target.total):
		creditConflict(w, "partial_credit",
			"the credit note doesn't cover the whole faktura; partial credits aren't supported, so settle the remainder separately")
		return
	case importedException && !openingReceivable:
		creditConflict(w, "booking_mismatch",
			"the faktura was imported from the previous system and Brygge has no posted opening balance on 1500 "+
				"dated between the faktura and the credit note; the opening balance is not imported yet, "+
				"so this pair can't be linked until it is, otherwise the credit note would clear a receivable that was never booked")
		return
	case cn.booked != target.booked && !importedException:
		// One side in the GL and the other not would leave the receivable
		// (or a stray revenue reversal) standing after the member is settled.
		// The only exception is the imported-faktura case above, where the
		// opening balance holds the receivable the credit note clears.
		creditConflict(w, "booking_mismatch",
			"one of the two is booked in the accounts and the other isn't; book or correct them first")
		return
	}

	// The credited check above was evaluated in the snapshot taken before
	// this transaction waited for the row locks. Re-read it now that the
	// faktura is locked, so a concurrent link of another credit note that
	// committed while we waited is seen.
	var nowCredited bool
	if err := tx.QueryRow(ctx,
		`SELECT `+finance.CreditedSQL("i")+` FROM invoices i WHERE i.id = $1 AND i.club_id = $2`,
		target.id, claims.ClubID,
	).Scan(&nowCredited); err != nil {
		h.log.Error().Err(err).Msg("link credit: re-check credited")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if nowCredited {
		creditConflict(w, "already_credited", "the faktura is already credited by another credit note")
		return
	}

	outcome := creditSettledPair
	if target.settled {
		outcome = creditOwed
	}

	if _, err := tx.Exec(ctx,
		`UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2 AND club_id = $3`,
		target.id, cn.id, claims.ClubID,
	); err != nil {
		// idx_invoices_credits_once is the last line of defence against two
		// credit notes linking the same faktura at once.
		if isUniqueViolation(err) {
			creditConflict(w, "already_credited", "the faktura is already credited by another credit note")
			return
		}
		h.log.Error().Err(err).Msg("link credit: update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			creditConflict(w, "already_credited", "the faktura is already credited by another credit note")
			return
		}
		h.log.Error().Err(err).Msg("link credit: commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreditLinked, "invoice", cn.id,
			map[string]any{
				"credit_note_number": cn.number,
				"invoice_number":     target.number,
				"amount":             target.total,
				"outcome":            outcome,
				// A manual pick can cross payers, so record whether this one
				// did and whether the treasurer confirmed it.
				"manual_pick":           req.ManualPick,
				"default_candidate":     isDefaultCandidate(cn, target),
				"member_mismatch":       memberMismatch,
				"confirm_other_member":  req.ConfirmOtherMember,
				"credit_note_has_payer": cn.hasPayer(),
			})
	}
	JSON(w, http.StatusOK, map[string]any{
		"outcome":            outcome,
		"credit_note_number": cn.number,
		"invoice_number":     target.number,
	})
}

// HandleUnlinkCreditNote removes a credit note's link, reopening the faktura
// it credited. Nothing was posted by the link, so nothing is reversed.
func (h *InvoiceHandler) HandleUnlinkCreditNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	creditID := chi.URLParam(r, "invoiceID")
	var number int
	var targetID string
	err := h.db.QueryRow(ctx,
		`UPDATE invoices cn SET credits_invoice_id = NULL
		   FROM invoices orig
		  WHERE cn.id = $1 AND cn.club_id = $2
		    AND orig.id = cn.credits_invoice_id
		 RETURNING cn.invoice_number, orig.id`,
		creditID, claims.ClubID,
	).Scan(&number, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "linked credit note not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("unlink credit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreditUnlinked, "invoice", creditID,
			map[string]any{"credit_note_number": number, "invoice_id": targetID})
	}
	w.WriteHeader(http.StatusNoContent)
}
