package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

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
}

func loadCreditInvoice(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, clubID, invoiceID string, forUpdate bool) (*creditInvoiceRow, error) {
	sql := `SELECT i.id, i.invoice_number, i.total_amount, i.status, i.user_id, i.recipient_id,
	               (i.sent_at IS NOT NULL), (i.import_source IS NOT NULL), i.credits_invoice_id,
	               ` + finance.SettledForMemberSQL("i") + `,
	               (i.payment_id IS NOT NULL OR i.vipps_paid_at IS NOT NULL),
	               EXISTS (SELECT 1 FROM journal_entries je
	                        WHERE je.club_id = i.club_id AND je.source = 'invoice_sync'
	                          AND je.source_id = i.id::text AND je.status = 'posted'),
	               ` + finance.CreditedSQL("i") + `
	          FROM invoices i
	         WHERE i.id = $1 AND i.club_id = $2`
	if forUpdate {
		sql += " FOR UPDATE OF i"
	}
	var r creditInvoiceRow
	err := q.QueryRow(ctx, sql, invoiceID, clubID).Scan(
		&r.id, &r.number, &r.total, &r.status, &r.userID, &r.recipientID,
		&r.sent, &r.imported, &r.creditsID, &r.settled, &r.paid, &r.booked, &r.credited,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// samePayer is true when both invoices bill the same member, or the same
// billing recipient (organisation).
func samePayer(a, b *creditInvoiceRow) bool {
	if a.userID != nil && b.userID != nil && *a.userID == *b.userID {
		return true
	}
	return a.recipientID != nil && b.recipientID != nil && *a.recipientID == *b.recipientID
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
	Imported      bool      `json:"imported"`
}

// HandleListCreditCandidates lists the fakturas a credit note can be linked
// to: same payer, a positive total equal to the credit, live, and not
// already credited. Partial credits are not linkable (see HandleLinkCreditNote).
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

	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, i.issue_date,
		        (i.payment_id IS NOT NULL OR i.vipps_paid_at IS NOT NULL),
		        (i.import_source IS NOT NULL)
		   FROM invoices i
		  WHERE i.club_id = $1
		    AND i.status = 'open'
		    AND i.total_amount > 0
		    AND abs(i.total_amount + $2) < 0.005
		    AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)
		    AND ((i.user_id IS NOT NULL AND i.user_id = $3)
		         OR (i.recipient_id IS NOT NULL AND i.recipient_id = $4))
		    AND NOT `+finance.CreditedSQL("i")+`
		  ORDER BY i.issue_date, i.invoice_number`,
		claims.ClubID, cn.total, cn.userID, cn.recipientID,
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
		if err := rows.Scan(&c.ID, &c.InvoiceNumber, &c.TotalAmount, &c.IssueDate, &c.Paid, &c.Imported); err != nil {
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
	JSON(w, http.StatusOK, map[string]any{"items": out})
}

type linkCreditRequest struct {
	InvoiceID string `json:"invoice_id"`
}

func creditConflict(w http.ResponseWriter, code, msg string) {
	JSON(w, http.StatusConflict, map[string]string{"error": msg, "code": code})
}

// HandleLinkCreditNote links a credit note to the faktura it credits. Only a
// full credit is accepted: a partial credit would leave a remaining balance
// that every "amount owed" (reminders, payables, pay-links) would have to
// net, which nothing supports yet, so it is refused with a reason instead.
func (h *InvoiceHandler) HandleLinkCreditNote(w http.ResponseWriter, r *http.Request) {
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

	switch {
	case cn.total >= 0:
		Error(w, http.StatusBadRequest, "invoice is not a credit note")
		return
	case target.total <= 0:
		Error(w, http.StatusBadRequest, "only a faktura with a positive total can be credited")
		return
	case cn.status == "voided" || target.status == "voided":
		creditConflict(w, "voided", "a voided invoice can't be linked")
		return
	case !(cn.sent || cn.imported) || !(target.sent || target.imported):
		creditConflict(w, "not_issued", "only sent or imported invoices can be linked")
		return
	case cn.creditsID != nil:
		creditConflict(w, "already_linked", "the credit note is already linked to a faktura")
		return
	case target.credited:
		creditConflict(w, "already_credited", "the faktura is already credited by another credit note")
		return
	case !samePayer(cn, target):
		creditConflict(w, "different_payer", "the credit note and the faktura are for different payers")
		return
	case !amountsCancel(cn.total, target.total):
		creditConflict(w, "partial_credit",
			"the credit note doesn't cover the whole faktura; partial credits aren't supported, so settle the remainder separately")
		return
	case cn.booked != target.booked:
		// One side in the GL and the other not would leave the receivable
		// (or a stray revenue reversal) standing after the member is settled.
		creditConflict(w, "booking_mismatch",
			"one of the two is booked in the accounts and the other isn't; book or correct them first")
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
		h.log.Error().Err(err).Msg("link credit: update")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
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
