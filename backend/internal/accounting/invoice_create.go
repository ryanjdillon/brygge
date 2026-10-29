package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// Creating a faktura. Every path that issues one (the single faktura form,
// bulk issuing, the previous-system import, and the credit notes and debit
// fakturas card refunds issue from the booking pass) numbers it through
// NextInvoiceNumberTx, which serialises numbering per club on a transaction
// advisory lock, so two of them running at once never take the same
// number. The unique (club_id, invoice_number) index is the last line of
// defence.

// InvoiceDraftLine is one line of a faktura to create. LineTotal is
// Quantity × UnitPrice.
type InvoiceDraftLine struct {
	Description    string
	SubDescription string
	Quantity       int
	UnitPrice      float64
	AccountID      *string
	PriceItemID    *string
	// Category is the price item's category as it was at issue; booking
	// resolves the revenue account from it (finance.LineCategorySQL).
	Category      *string
	ExemptionRole *string
	BoatID        *string
}

// InvoiceDraft is a faktura to create: who it bills, its dates and lines.
// The total is the sum of the lines.
type InvoiceDraft struct {
	UserID *string
	// IssueDate zero means today (the column default).
	IssueDate      time.Time
	DueDate        time.Time
	FiscalPeriodID *string
	// RecipientKind "" means private.
	RecipientKind          string
	RecipientOrgName       *string
	RecipientOrgNumber     *string
	RecipientOrgAddress    *string
	RecipientContactPerson *string
	RecipientTheirRef      *string
	RecipientEmail         *string
	RecipientID            *string
	MemberName             string
	MemberEmail            string
	// Sent stamps sent_at at creation: the faktura is issued as it is
	// created (a refund's credit note), not by a later send.
	Sent  bool
	Lines []InvoiceDraftLine
	// RenderPDF, when set, renders the faktura's PDF once its number and
	// KID are known; the bytes are stored with the faktura. It runs inside
	// the transaction, so it must not touch the database.
	RenderPDF func(number int, kid string) ([]byte, error)
}

// CreatedInvoice is a faktura CreateInvoiceTx stored.
type CreatedInvoice struct {
	ID        string
	Number    int
	KID       string
	Total     float64
	CreatedAt time.Time
	// PDF is what RenderPDF returned (nil without it).
	PDF []byte
}

// NextInvoiceNumberTx returns the club's next faktura number and holds the
// club's numbering lock until tx ends, so the caller inserts the faktura
// under that number before anyone else can read the same maximum. The read
// is a statement after the lock: under READ COMMITTED it sees the faktura
// the previous holder committed.
func NextInvoiceNumberTx(ctx context.Context, tx pgx.Tx, clubID string) (int, error) {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('invoice_number:' || $1::text))`, clubID); err != nil {
		return 0, fmt.Errorf("locking invoice numbering: %w", err)
	}
	var next int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1`, clubID,
	).Scan(&next); err != nil {
		return 0, fmt.Errorf("next invoice number: %w", err)
	}
	return next, nil
}

// CreateInvoiceTx numbers and stores a faktura and its lines on tx, with
// the KID the club's check method gives the number. Everything runs on tx;
// the caller commits.
func CreateInvoiceTx(ctx context.Context, tx pgx.Tx, clubID string, d InvoiceDraft) (*CreatedInvoice, error) {
	if len(d.Lines) == 0 {
		return nil, errors.New("a faktura needs at least one line")
	}
	number, err := NextInvoiceNumberTx(ctx, tx, clubID)
	if err != nil {
		return nil, err
	}
	var kidMethod string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(kid_check_method, 'mod11') FROM clubs WHERE id = $1`, clubID,
	).Scan(&kidMethod); err != nil {
		return nil, fmt.Errorf("loading club: %w", err)
	}
	kid, err := finance.GenerateKIDMethod("000", number, 1, kidMethod)
	if err != nil {
		return nil, fmt.Errorf("generating KID: %w", err)
	}

	out := &CreatedInvoice{Number: number, KID: kid}
	for _, l := range d.Lines {
		out.Total += float64(l.Quantity) * l.UnitPrice
	}
	if d.RenderPDF != nil {
		if out.PDF, err = d.RenderPDF(number, kid); err != nil {
			return nil, fmt.Errorf("rendering faktura PDF: %w", err)
		}
	}

	recipientKind := d.RecipientKind
	if recipientKind == "" {
		recipientKind = "private"
	}
	var issueDate *time.Time
	if !d.IssueDate.IsZero() {
		issueDate = &d.IssueDate
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, issue_date, due_date,
		                       total_amount, pdf_data, fiscal_period_id,
		                       recipient_kind, recipient_org_name, recipient_org_number,
		                       recipient_org_address, recipient_contact_person,
		                       recipient_their_ref, recipient_email, recipient_id,
		                       member_name, member_email, sent_at)
		 VALUES ($1, $2, $3, $4, COALESCE($5::date, CURRENT_DATE), $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
		         $18, $19, CASE WHEN $20::bool THEN now() END)
		 RETURNING id, created_at`,
		clubID, d.UserID, number, kid, issueDate, d.DueDate, out.Total, out.PDF, d.FiscalPeriodID,
		recipientKind, d.RecipientOrgName, d.RecipientOrgNumber,
		d.RecipientOrgAddress, d.RecipientContactPerson,
		d.RecipientTheirRef, d.RecipientEmail, d.RecipientID,
		d.MemberName, d.MemberEmail, d.Sent,
	).Scan(&out.ID, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("storing faktura: %w", err)
	}

	for _, l := range d.Lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, sub_description, quantity,
			                            unit_price, line_total, account_id, price_item_id, category,
			                            exemption_role, boat_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			out.ID, l.Description, l.SubDescription, l.Quantity,
			l.UnitPrice, float64(l.Quantity)*l.UnitPrice, l.AccountID, l.PriceItemID, l.Category,
			l.ExemptionRole, l.BoatID,
		); err != nil {
			return nil, fmt.Errorf("storing faktura line %q: %w", l.Description, err)
		}
	}
	return out, nil
}
