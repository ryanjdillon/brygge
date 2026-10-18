package accounting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// paymentTypeAccountMap maps both Brygge payment_type enum values and
// price_items.category strings to kontoplan account codes so that either
// vocabulary resolves to the correct revenue account. The two sets share
// harbor_membership and slip_fee verbatim; membership↔dues and the
// booking/guest/motorhome group diverge in name only.
var paymentTypeAccountMap = map[string]string{
	// payment_type enum values
	"dues":        "3100", // Medlemskontingent
	"booking":     "3200", // Gjestehavninntekter
	"merchandise": "3300", // Salgs- og tjenesteinntekter

	// price_items.category values
	"membership":        "3100", // Medlemskontingent
	"harbor_membership": "3110", // Havneavgift
	"slip_fee":          "3120", // Plassleie
	"electricity":       "3130", // Strøm
	"seasonal_rental":   "3120", // Plassleie (sesong)
	"guest":             "3200", // Gjestehavninntekter
	"motorhome":         "3210", // Bobil/campingplass
	"room_hire":         "3220", // Lokalleie
	"service":           "3300", // Salgs- og tjenesteinntekter
	"other":             "3900", // Andre inntekter
}

const (
	bankAccountCode       = "1920" // Bankkonto drift
	receivablesAccountCode = "1500" // Kundefordringer
	defaultRevenueCode     = "3100" // Fallback for unmatched types
)

// bookedEntryFilter selects journal entries that were ever posted. A voided
// entry keeps its original lines and gets a posted reversal (see
// VoidJournalEntry), so reports must count both sides to net to zero;
// counting only 'posted' would book the reversal against nothing.
const bookedEntryFilter = `je.status IN ('posted', 'voided')`

// SyncResult holds the outcome of a sync operation.
type SyncResult struct {
	Synced   int `json:"synced"`
	Skipped  int `json:"skipped"`
	Reversed int `json:"reversed"`
	// SkippedDetails says why each skipped item was not booked, so the
	// operator can act on it instead of seeing a bare count.
	SkippedDetails []SkippedItem `json:"skipped_details"`
}

// SkippedItem identifies one source row a sync did not book.
type SkippedItem struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

func (r *SyncResult) skip(ref, reason string) {
	r.Skipped++
	r.SkippedDetails = append(r.SkippedDetails, SkippedItem{Ref: ref, Reason: reason})
}

func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// SyncPayments creates journal entries from completed payments that haven't been synced yet.
// Each entry is automatically posted (completed payments are real transactions).
func (s *Service) SyncPayments(ctx context.Context, clubID, periodID, syncedBy string) (*SyncResult, error) {
	// Get period date range
	var startDate, endDate time.Time
	err := s.db.QueryRow(ctx,
		`SELECT start_date, end_date FROM fiscal_periods WHERE id = $1 AND club_id = $2 AND status = 'open'`,
		periodID, clubID,
	).Scan(&startDate, &endDate)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("fiscal period not found or not open")
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Add one day to end date for inclusive range
	endDatePlusOne := endDate.AddDate(0, 0, 1)

	// Query completed payments within the period that haven't been synced.
	// A payment that settles an invoice was already booked by the bank or
	// Vipps reconciliation that created it (DR bank / CR 1500); booking it
	// again here as DR bank / CR revenue would double both bank and revenue.
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.type, p.amount, p.created_at::date::text, u.full_name
		 FROM payments p
		 LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.club_id = $1
		   AND p.status = 'completed'
		   AND p.created_at >= $2 AND p.created_at < $3
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries je
		     WHERE je.source = 'payment_sync' AND je.source_id = p.id::text
		   )
		   AND NOT EXISTS (SELECT 1 FROM invoices i WHERE i.payment_id = p.id)
		 ORDER BY p.created_at`,
		clubID, startDate, endDatePlusOne,
	)
	if err != nil {
		return nil, fmt.Errorf("querying payments: %w", err)
	}
	defer rows.Close()

	result := &SyncResult{}

	for rows.Next() {
		var paymentID, paymentType, entryDate string
		var amount float64
		var memberName *string
		if err := rows.Scan(&paymentID, &paymentType, &amount, &entryDate, &memberName); err != nil {
			return nil, fmt.Errorf("scanning payment: %w", err)
		}

		revenueCode, ok := paymentTypeAccountMap[paymentType]
		if !ok {
			revenueCode = defaultRevenueCode
		}

		description := fmt.Sprintf("Betaling: %s", paymentType)
		if memberName != nil {
			description = fmt.Sprintf("Betaling %s: %s", paymentType, *memberName)
		}

		sourceID := paymentID
		sourceTable := "payments"

		entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      entryDate,
			Description:    description,
			Source:         "payment_sync",
			SourceID:       &sourceID,
			SourceTable:    &sourceTable,
			CreatedBy:      syncedBy,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: bankAccountCode, Debit: amount, Credit: 0, Description: ""},
				{AccountCode: revenueCode, Debit: 0, Credit: amount, Description: ""},
			},
		})
		if err != nil {
			s.log.Error().Err(err).Str("payment_id", paymentID).Msg("failed to sync payment")
			result.skip("payment "+paymentID, err.Error())
			continue
		}

		// Auto-post (these are completed, real transactions)
		if err := s.PostJournalEntry(ctx, clubID, entry.ID, syncedBy); err != nil {
			s.log.Error().Err(err).Str("entry_id", entry.ID).Msg("failed to auto-post synced payment")
			result.skip("payment "+paymentID, "post: "+err.Error())
			continue
		}

		result.Synced++
	}

	return result, rows.Err()
}

// SyncInvoices creates receivable journal entries from invoices that haven't been synced yet.
// Each entry debits Kundefordringer (1500) and credits the appropriate revenue account.
func (s *Service) SyncInvoices(ctx context.Context, clubID, periodID, syncedBy string) (*SyncResult, error) {
	// Get period date range
	var startDate, endDate time.Time
	err := s.db.QueryRow(ctx,
		`SELECT start_date, end_date FROM fiscal_periods WHERE id = $1 AND club_id = $2 AND status = 'open'`,
		periodID, clubID,
	).Scan(&startDate, &endDate)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("fiscal period not found or not open")
	}
	if err != nil {
		return nil, fmt.Errorf("getting period: %w", err)
	}

	// Only sent, non-voided invoices are receivables. A draft hasn't been
	// billed yet, and a voided one never should be (it is reversed below
	// if it was booked before being voided).
	rows, err := s.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, i.issue_date::text, u.full_name
		 FROM invoices i
		 LEFT JOIN users u ON u.id = i.user_id
		 WHERE i.club_id = $1
		   AND i.issue_date >= $2 AND i.issue_date <= $3
		   AND i.status <> 'voided'
		   AND i.sent_at IS NOT NULL
		   AND NOT EXISTS (
		     SELECT 1 FROM journal_entries je
		     WHERE je.source = 'invoice_sync' AND je.source_id = i.id::text
		   )
		 ORDER BY i.issue_date`,
		clubID, startDate, endDate,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoices: %w", err)
	}
	defer rows.Close()

	result := &SyncResult{}

	for rows.Next() {
		var invoiceID, entryDate string
		var invoiceNumber int
		var amount float64
		var memberName *string
		if err := rows.Scan(&invoiceID, &invoiceNumber, &amount, &entryDate, &memberName); err != nil {
			return nil, fmt.Errorf("scanning invoice: %w", err)
		}
		ref := fmt.Sprintf("faktura %d", invoiceNumber)
		if err := s.bookInvoiceEntry(ctx, clubID, periodID, invoiceID, invoiceNumber, amount, entryDate, memberName, syncedBy); err != nil {
			result.skip(ref, err.Error())
			continue
		}

		result.Synced++
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	if err := s.reverseVoidedInvoiceEntries(ctx, clubID, periodID, syncedBy, result); err != nil {
		return result, err
	}
	return result, nil
}

// errNothingToBook marks a faktura with no amount on any account: zero total
// and every line netting out on the same account.
var errNothingToBook = errors.New("net total is 0, nothing to book")

// bookInvoiceEntry creates and posts the invoice_sync entry for one faktura:
// DR 1500 the total, CR each revenue account its lines map to. Signs drive
// the sides, so a credit note books DR revenue / CR 1500. A zero-total
// faktura whose lines hit different accounts (a role exemption: CR revenue,
// DR the exemption expense account) is booked gross with no 1500 line, so it
// never becomes a receivable. A draft whose post fails is discarded so no
// orphan is left. This is the one code path both the send-time booking
// (BookInvoice) and the repair sync use.
func (s *Service) bookInvoiceEntry(ctx context.Context, clubID, periodID, invoiceID string, invoiceNumber int, amount float64, entryDate string, memberName *string, actor string) error {
	amount = roundCents(amount)

	description := fmt.Sprintf("Faktura #%d", invoiceNumber)
	if memberName != nil {
		description = fmt.Sprintf("Faktura #%d: %s", invoiceNumber, *memberName)
	}

	netByAccount, err := s.invoiceRevenueCredits(ctx, invoiceID, amount)
	if err != nil {
		s.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("failed to aggregate invoice lines")
		return fmt.Errorf("reading lines: %w", err)
	}

	// journal_line_one_side forbids zero or negative amounts, so each line
	// goes on the side its sign dictates and zero nets are dropped.
	var journalLines []CreateJournalLineInput
	if amount != 0 {
		journalLines = append(journalLines, signedLine(receivablesAccountCode, amount))
	}
	for code, net := range netByAccount {
		if net = roundCents(net); net != 0 {
			journalLines = append(journalLines, signedLine(code, -net))
		}
	}
	if len(journalLines) == 0 {
		return errNothingToBook
	}

	sourceID := invoiceID
	sourceTable := "invoices"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      entryDate,
		Description:    description,
		Source:         "invoice_sync",
		SourceID:       &sourceID,
		SourceTable:    &sourceTable,
		CreatedBy:      actor,
		ClubID:         clubID,
		Lines:          journalLines,
	})
	if err != nil {
		s.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("failed to book invoice")
		return err
	}
	if err := s.PostJournalEntry(ctx, clubID, entry.ID, actor); err != nil {
		s.log.Error().Err(err).Str("entry_id", entry.ID).Msg("failed to post invoice entry")
		s.discardDraft(ctx, clubID, entry.ID)
		return fmt.Errorf("post: %w", err)
	}
	return nil
}

// InvoiceBooking reports what BookInvoice did.
type InvoiceBooking string

const (
	InvoiceBooked        InvoiceBooking = "booked"
	InvoiceAlreadyBooked InvoiceBooking = "already_booked"
	InvoiceNothingToBook InvoiceBooking = "nothing_to_book"
)

// ErrInvoicePeriodClosed means the faktura's fiscal period is closed, so its
// entry cannot be booked or reversed there. The repair sync cannot fix it
// either; the period has to be reopened.
var ErrInvoicePeriodClosed = fmt.Errorf("the faktura's fiscal period is closed: %w", ErrPeriodClosed)

// BookInvoice books one sent, non-voided faktura into the period its
// issue_date falls in. It is idempotent: a faktura that already has an
// invoice_sync entry (the unique index enforces one) is reported as
// already booked. Callers on the send path treat an error as a booking
// failure to surface, not as a reason to fail the send; the repair sync
// books whatever was missed.
func (s *Service) BookInvoice(ctx context.Context, clubID, invoiceID, actor string) (InvoiceBooking, error) {
	var (
		invoiceNumber int
		amount        float64
		issueDate     time.Time
		status        string
		sent          bool
		memberName    *string
		booked        bool
	)
	err := s.db.QueryRow(ctx,
		`SELECT i.invoice_number, i.total_amount, i.issue_date, i.status,
		        i.sent_at IS NOT NULL, u.full_name,
		        EXISTS (SELECT 1 FROM journal_entries je
		                 WHERE je.club_id = i.club_id AND je.source = 'invoice_sync'
		                   AND je.source_id = i.id::text)
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&invoiceNumber, &amount, &issueDate, &status, &sent, &memberName, &booked)
	if err != nil {
		return "", fmt.Errorf("loading invoice: %w", err)
	}
	if booked {
		return InvoiceAlreadyBooked, nil
	}
	if !sent || status == "voided" {
		return "", fmt.Errorf("faktura %d is not a sent, open invoice", invoiceNumber)
	}

	periodID, periodStatus, err := s.ResolvePeriodForDate(ctx, clubID, issueDate)
	if err != nil {
		return "", fmt.Errorf("resolving fiscal period: %w", err)
	}
	if periodStatus != "open" {
		return "", ErrInvoicePeriodClosed
	}

	err = s.bookInvoiceEntry(ctx, clubID, periodID, invoiceID, invoiceNumber, amount, issueDate.Format("2006-01-02"), memberName, actor)
	switch {
	case errors.Is(err, errNothingToBook):
		return InvoiceNothingToBook, nil
	case isUniqueViolation(err):
		// A concurrent send or sync booked it first.
		return InvoiceAlreadyBooked, nil
	case err != nil:
		return "", err
	}
	return InvoiceBooked, nil
}

// invoiceEntrySources are the journal sources that book a faktura: the
// receivable entry itself and any revenue reclassifications posted against
// it (ResplitInvoiceBilags). Voiding a faktura must reverse all of them, or
// the reclassification's +/- per account survives the void forever.
const invoiceEntrySources = `('invoice_sync', 'invoice_resplit')`

// ReverseInvoice reverses every posted entry of a faktura that is being
// voided (its invoice_sync entry and any invoice_resplit corrections), in the
// same flow, so receivable and revenue net to zero immediately. It returns
// the reversal entry ids; none when there is nothing to reverse (never
// booked, or already reversed). If any of the entries sits in a closed
// period nothing is reversed and ErrInvoicePeriodClosed is returned.
func (s *Service) ReverseInvoice(ctx context.Context, clubID, invoiceID, actor string) ([]string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT je.id FROM journal_entries je
		  WHERE je.club_id = $1 AND je.source IN `+invoiceEntrySources+`
		    AND je.source_id = $2 AND je.status = 'posted'
		  ORDER BY je.created_at`,
		clubID, invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading invoice entries: %w", err)
	}
	entryIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scanning invoice entries: %w", err)
	}
	// Every void runs in this one transaction, each holding its period FOR
	// SHARE until commit. A family member in a closed period fails its void
	// and rolls back the ones before it, so the reversal is all-or-nothing
	// without a separate period pre-check that ClosePeriod could race.
	var reversals []string
	for _, id := range entryIDs {
		rev, err := voidJournalEntryTx(ctx, tx, clubID, id, actor)
		if errors.Is(err, ErrPeriodClosed) {
			return nil, ErrInvoicePeriodClosed
		}
		if err != nil {
			return nil, err
		}
		reversals = append(reversals, rev.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}
	return reversals, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// signedLine puts amount on the debit side when positive and on the
// credit side (as a positive number) when negative.
func signedLine(code string, amount float64) CreateJournalLineInput {
	if amount >= 0 {
		return CreateJournalLineInput{AccountCode: code, Debit: amount}
	}
	return CreateJournalLineInput{AccountCode: code, Credit: -amount}
}

// reverseVoidedInvoiceEntries voids every posted entry (invoice_sync and
// invoice_resplit) of each invoice in the period that has since been voided.
// VoidJournalEntry keeps the original and posts a reversal, so the
// receivable and revenue net to zero with a full trail. Restricted to the period being synced, which the
// caller has verified is open.
func (s *Service) reverseVoidedInvoiceEntries(ctx context.Context, clubID, periodID, syncedBy string, result *SyncResult) error {
	rows, err := s.db.Query(ctx,
		`SELECT je.id, i.invoice_number
		 FROM journal_entries je
		 JOIN invoices i ON i.id::text = je.source_id AND i.club_id = je.club_id
		 WHERE je.club_id = $1 AND je.fiscal_period_id = $2
		   AND je.source IN `+invoiceEntrySources+` AND je.status = 'posted'
		   AND i.status = 'voided'
		 ORDER BY i.invoice_number, je.created_at`,
		clubID, periodID,
	)
	if err != nil {
		return fmt.Errorf("querying voided invoice entries: %w", err)
	}
	type pending struct {
		entryID       string
		invoiceNumber int
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.entryID, &p.invoiceNumber); err != nil {
			rows.Close()
			return fmt.Errorf("scanning voided invoice entry: %w", err)
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	reversed := map[int]bool{}
	for _, p := range todo {
		if _, err := s.VoidJournalEntry(ctx, clubID, p.entryID, syncedBy); err != nil {
			s.log.Error().Err(err).Str("entry_id", p.entryID).Msg("failed to reverse voided invoice entry")
			result.skip(fmt.Sprintf("faktura %d", p.invoiceNumber), "reversing voided faktura: "+err.Error())
			continue
		}
		reversed[p.invoiceNumber] = true
	}
	// Reversed counts fakturas, not entries: one faktura may carry a sync
	// entry plus resplit corrections.
	result.Reversed += len(reversed)
	return nil
}

// invoiceRevenueCredits computes the net revenue per account for an
// invoice by walking its lines and summing line_total per account. A
// line's own account_id (set on custom lines) wins; otherwise its price
// item's category is mapped through paymentTypeAccountMap; otherwise it
// falls to defaultRevenueCode. Nets may be negative (credit notes,
// discount lines). If the line sum diverges from the invoice total, the
// residual goes to defaultRevenueCode so the bilag still balances.
func (s *Service) invoiceRevenueCredits(ctx context.Context, invoiceID string, invoiceTotal float64) (map[string]float64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT il.line_total, pi.category, a.code
		 FROM invoice_lines il
		 LEFT JOIN price_items pi ON pi.id = il.price_item_id
		 LEFT JOIN accounts a ON a.id = il.account_id
		 WHERE il.invoice_id = $1`,
		invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying invoice lines: %w", err)
	}
	defer rows.Close()

	credits := map[string]float64{}
	var lineSum float64
	for rows.Next() {
		var lineTotal float64
		var category, accountCode *string
		if err := rows.Scan(&lineTotal, &category, &accountCode); err != nil {
			return nil, fmt.Errorf("scanning line: %w", err)
		}
		code := defaultRevenueCode
		switch {
		case accountCode != nil:
			code = *accountCode
		case category != nil:
			if c, ok := paymentTypeAccountMap[*category]; ok {
				code = c
			}
		}
		credits[code] += lineTotal
		lineSum += lineTotal
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if residual := roundCents(invoiceTotal - lineSum); residual != 0 {
		credits[defaultRevenueCode] += residual
	}
	if len(credits) == 0 {
		credits[defaultRevenueCode] = invoiceTotal
	}
	return credits, nil
}
