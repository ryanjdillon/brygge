package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// "Betal med bank" (BRY-631). An applicant who would rather pay by bank
// transfer turns their pending join order into a KID faktura: one faktura
// with purpose membership_join and the order's one line. It is issued
// (sent_at) as it is created, because the KID shown in the answer is the
// issue; it is booked as the club's system actor and emailed after the
// commit, and the email is only a convenience. The order itself becomes that
// faktura's pay order: its line is dropped, its purpose cleared and an
// order_sources row links it to the faktura, exactly the shape
// ensureFakturaOrderToken gives a faktura's pay link. The applicant's
// existing /betal link and the faktura's emailed pay link are then the same
// order, and paying it or the KID settles the one faktura; there is never a
// second thing to pay. Settling the faktura grants membership (BRY-630) by
// either route.

// Error codes the join pay page matches on.
const (
	codeJoinNotApplicant     = "not_applicant"
	codeJoinNoPending        = "no_pending_join"
	codeJoinPaymentInProcess = "payment_in_progress"
)

// joinPaymentInFlight is how long a started online payment on the join order
// may still be completed: the longest provider session, Stripe Checkout's
// (a Vipps payment page lives minutes). Until it has passed, the order is not
// turned into a faktura, or one payment could land on each.
const joinPaymentInFlight = stripe.SessionLifetime

// openJoinInvoiceSQL selects the applicant's join faktura still owed ($1
// club, $2 user), newest first. Owed excludes a paid, voided or credited one.
var openJoinInvoiceSQL = `SELECT i.id FROM invoices i
	  WHERE i.club_id = $1 AND i.user_id = $2 AND i.purpose = '` + membership.Purpose + `'
	    AND ` + finance.OwedByMemberSQL("i") + `
	  ORDER BY i.created_at DESC, i.invoice_number DESC
	  LIMIT 1`

// openJoinInvoiceOrder returns the pay order (id and token) of the user's
// join faktura still owed, or empty strings when there is none.
func openJoinInvoiceOrder(ctx context.Context, q pgxQuerier, clubID, userID string) (orderID, token string, err error) {
	err = q.QueryRow(ctx,
		`SELECT o.id, o.token FROM order_sources s JOIN orders o ON o.id = s.order_id
		  WHERE s.invoice_id = (`+openJoinInvoiceSQL+`) AND o.token IS NOT NULL`,
		clubID, userID,
	).Scan(&orderID, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("load join faktura pay order: %w", err)
	}
	return orderID, token, nil
}

type joinBankInvoiceResponse struct {
	InvoiceID     string  `json:"invoice_id"`
	InvoiceNumber int     `json:"invoice_number"`
	KID           string  `json:"kid_number"`
	AccountNumber string  `json:"account_number"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	DueDate       string  `json:"due_date"`
	// Emailed is whether this call emailed the faktura; a repeat call never
	// does, and a failed or impossible send leaves it false.
	Emailed bool `json:"emailed"`
}

// joinBankInvoice is the faktura the handler answers with.
type joinBankInvoice struct {
	id, kid  string
	number   int
	total    float64
	due      time.Time
	issuedTo string
	name     string
	created  bool
	orderID  string
}

// HandleJoinBankInvoice is POST /api/v1/join/bank-invoice for the signed-in
// applicant: it issues the KID faktura for their pending join order, or
// returns the one issued before.
//   - 201 the new faktura; 200 the one issued before
//   - 409 not_applicant (the user holds no applicant role),
//     no_pending_join (no pending join order to bill, e.g. it is paid) or
//     payment_in_progress (an online payment of the order is reserved,
//     captured, or started within joinPaymentInFlight)
func (h *InvoiceHandler) HandleJoinBankInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	inv, code, err := h.issueJoinBankInvoice(ctx, claims.ClubID, claims.UserID)
	switch {
	case err != nil:
		h.log.Error().Err(err).Str("user_id", claims.UserID).Msg("join bank faktura failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	case code == codeJoinNotApplicant:
		JSON(w, http.StatusConflict, map[string]string{"error": "only an applicant can choose bank payment", "code": code})
		return
	case code == codeJoinNoPending:
		JSON(w, http.StatusConflict, map[string]string{"error": "there is no unpaid join to bill", "code": code})
		return
	case code == codeJoinPaymentInProcess:
		JSON(w, http.StatusConflict, map[string]string{"error": "an online payment of the join is in progress", "code": code})
		return
	}

	club, err := loadClubInvoiceFields(ctx, h.db, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", inv.id).Msg("join bank faktura: loading club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	status, emailed := http.StatusOK, false
	if inv.created {
		status = http.StatusCreated
		if h.audit != nil {
			h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
				audit.ActionInvoiceCreated, "invoice", inv.id,
				map[string]any{"invoice_number": inv.number, "kid": inv.kid, "total": inv.total,
					"user_id": claims.UserID, "purpose": membership.Purpose, "order_id": inv.orderID})
		}
		// The faktura is issued; the steps after the commit only report.
		// Booking is the system actor's and SyncInvoices books what it misses.
		h.bookAsSystem(ctx, claims.ClubID, inv.id)
		emailed = h.mailJoinInvoice(ctx, r, claims, inv, club)
	}
	JSON(w, status, joinBankInvoiceResponse{
		InvoiceID: inv.id, InvoiceNumber: inv.number, KID: inv.kid, AccountNumber: club.BankAccount,
		Amount: inv.total, Currency: paymentCurrency, DueDate: inv.due.Format(time.DateOnly), Emailed: emailed,
	})
}

// systemActor is the club's system actor (ACC-13), the author of every bilag
// no treasurer made. ok is false without a ledger or when it cannot be
// resolved; the caller then books nothing and leaves it to SyncInvoices,
// never booking as a member or applicant.
func (h *InvoiceHandler) systemActor(ctx context.Context, clubID string) (string, bool) {
	if h.ledger == nil {
		return "", false
	}
	id, err := h.ledger.SystemActor(ctx, clubID)
	if err != nil {
		h.log.Error().Err(err).Str("club_id", clubID).Msg("system actor unavailable; booking left to the repair sync")
		return "", false
	}
	return id, true
}

// bookAsSystem books a sent faktura as the system actor, best effort.
func (h *InvoiceHandler) bookAsSystem(ctx context.Context, clubID, invoiceID string) {
	actor, ok := h.systemActor(ctx, clubID)
	if !ok {
		return
	}
	if _, err := h.ledger.BookInvoice(ctx, clubID, invoiceID, actor); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("faktura issued but GL booking failed; the repair sync books it")
	}
}

// mailJoinInvoice emails the issued join faktura with its PDF, as any faktura
// is emailed (invoiceMessage), and reports whether it went out.
func (h *InvoiceHandler) mailJoinInvoice(ctx context.Context, r *http.Request, claims *auth.Claims,
	inv *joinBankInvoice, club *clubInvoiceFields) bool {
	if h.email == nil || inv.issuedTo == "" {
		return false
	}
	pdf, _, _, err := h.ensureInvoicePDF(ctx, claims.ClubID, inv.id, club)
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", inv.id).Msg("join bank faktura: rendering the PDF failed; not emailed")
		return false
	}
	subject, body := h.invoiceMessage(ctx, email.DetectLocale(r), claims.ClubID, inv.id, inv.name, club.Name,
		inv.number, inv.due, inv.total, inv.kid, club.BankAccount, false)
	mailCtx := h.invoiceMailContext(ctx, email.KindInvoice, claims.ClubID, inv.id, claims.UserID, inv.total)
	if err := h.email.SendWithAttachment(mailCtx, inv.issuedTo, subject, body,
		fmt.Sprintf("faktura-%d.pdf", inv.number), pdf); err != nil {
		h.log.Error().Err(err).Str("invoice_id", inv.id).Msg("join bank faktura issued but not emailed; the KID was shown")
		return false
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceEmailed, "invoice", inv.id,
			map[string]any{"invoice_number": inv.number})
	}
	return true
}

// joinOrderLine is the pending join order's line as the faktura's, or a code
// when the order cannot be billed: an online payment of it is in flight, or it
// has no line.
func joinOrderLine(ctx context.Context, tx pgx.Tx, orderID string) (accounting.InvoiceDraftLine, string, error) {
	var line accounting.InvoiceDraftLine
	var inFlight bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM payment_attempts
		                 WHERE order_id = $1
		                   AND (status IN ('authorized', 'captured')
		                        OR (status = 'created' AND created_at > now() - make_interval(secs => $2))))`,
		orderID, joinPaymentInFlight.Seconds(),
	).Scan(&inFlight); err != nil {
		return line, "", fmt.Errorf("check payment in flight: %w", err)
	}
	if inFlight {
		return line, codeJoinPaymentInProcess, nil
	}
	var category string
	err := tx.QueryRow(ctx,
		`SELECT l.name, l.quantity, l.unit_price::float8, l.price_item_id::text, COALESCE(p.category, '')
		   FROM order_lines l
		   LEFT JOIN price_items p ON p.id = l.price_item_id
		  WHERE l.order_id = $1
		  ORDER BY l.created_at
		  LIMIT 1`,
		orderID,
	).Scan(&line.Description, &line.Quantity, &line.UnitPrice, &line.PriceItemID, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return line, codeJoinNoPending, nil
	}
	if err != nil {
		return line, "", fmt.Errorf("load join order line: %w", err)
	}
	if line.PriceItemID != nil && category != "" {
		line.Category = &category
	}
	return line, "", nil
}

// issueJoinBankInvoice returns the user's open join faktura, issuing it from
// their pending join order first when there is none. It locks the join order,
// then the user, the order in which the capture settlement and the expiry take
// them (fakturas, orders, user), so a second click waits for the first and
// then finds its faktura. A join confirm locks the user before the order, so
// one racing a click on the same applicant can deadlock; Postgres aborts one
// of the two and the request can be retried. code is set, with nothing
// written, when the user may not have one.
func (h *InvoiceHandler) issueJoinBankInvoice(ctx context.Context, clubID, userID string) (*joinBankInvoice, string, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	inv := &joinBankInvoice{}
	err = tx.QueryRow(ctx,
		`SELECT id FROM orders
		  WHERE club_id = $1 AND user_id = $2 AND purpose = $3 AND status = 'pending'
		  ORDER BY created_at DESC
		  LIMIT 1
		  FOR UPDATE`,
		clubID, userID, membership.Purpose,
	).Scan(&inv.orderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", fmt.Errorf("lock join order: %w", err)
	}

	var applicant bool
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(u.full_name, ''), COALESCE(u.email, ''),
		        EXISTS (SELECT 1 FROM user_roles r
		                 WHERE r.user_id = u.id AND r.club_id = u.club_id AND r.role = 'applicant')
		   FROM users u
		  WHERE u.id = $1 AND u.club_id = $2 AND u.kind = 'person' AND u.deactivated_at IS NULL
		  FOR UPDATE`,
		userID, clubID,
	).Scan(&inv.name, &inv.issuedTo, &applicant)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !applicant) {
		return nil, codeJoinNotApplicant, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("lock user: %w", err)
	}

	err = tx.QueryRow(ctx,
		`SELECT i.id, i.invoice_number, i.kid_number, i.total_amount::float8, i.due_date
		   FROM invoices i WHERE i.id = (`+openJoinInvoiceSQL+`)`,
		clubID, userID,
	).Scan(&inv.id, &inv.number, &inv.kid, &inv.total, &inv.due)
	if err == nil {
		inv.orderID = ""
		return inv, "", tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", fmt.Errorf("load join faktura: %w", err)
	}
	if inv.orderID == "" {
		return nil, codeJoinNoPending, nil
	}

	line, code, err := joinOrderLine(ctx, tx, inv.orderID)
	if err != nil || code != "" {
		return nil, code, err
	}

	inv.due = time.Now().AddDate(0, 0, defaultFakturaDueDays)
	created, err := accounting.CreateInvoiceTx(ctx, tx, clubID, accounting.InvoiceDraft{
		UserID:      &userID,
		DueDate:     inv.due,
		MemberName:  inv.name,
		MemberEmail: inv.issuedTo,
		Purpose:     membership.Purpose,
		Sent:        true,
		Lines:       []accounting.InvoiceDraftLine{line},
	})
	if err != nil {
		return nil, "", err
	}
	inv.id, inv.number, inv.kid, inv.total, inv.created = created.ID, created.Number, created.KID, created.Total, true

	// The order becomes the faktura's pay order. Its purpose goes: the
	// faktura carries it now, and an order with a purpose and no join line
	// would read as a withheld grant once paid.
	if _, err := tx.Exec(ctx, `DELETE FROM order_lines WHERE order_id = $1`, inv.orderID); err != nil {
		return nil, "", fmt.Errorf("clear join order lines: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, inv.orderID, inv.id,
	); err != nil {
		return nil, "", fmt.Errorf("link join order to its faktura: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET purpose = NULL, total_amount = $2, updated_at = now() WHERE id = $1`,
		inv.orderID, inv.total,
	); err != nil {
		return nil, "", fmt.Errorf("repoint join order: %w", err)
	}
	return inv, "", tx.Commit(ctx)
}
