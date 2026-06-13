package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// TestRegenerateOnePDF locks in the contract for DIL-364 in-place
// PDF regeneration: every field on the invoices row except pdf_data
// must be byte-identical after a regenerate, pdf_data must change,
// and the prior bytes must land in invoice_pdf_archive (DIL-374).
//
// A future refactor that drops a preserved field, fails the archive
// insert, or breaks the transactional safety of the overwrite fails
// loudly here.
func TestRegenerateOnePDF(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	// Set bank account #1 + period as the default.
	originalBank := "1234.56.78901"
	if _, err := db.Exec(ctx,
		`UPDATE clubs SET name = 'Klokkarvik Båtlag Test',
		                  org_number = '999999999',
		                  address = 'Brygga 1, 5378 Klokkarvik',
		                  bank_account = $1
		   WHERE id = $2`,
		originalBank, clubID,
	); err != nil {
		t.Fatalf("setting club bank account: %v", err)
	}

	periodID := seedFiscalPeriod(t, db, clubID, 2026)

	// Build + insert the invoice with a real PDF so we can verify
	// the regenerate path produced a different (but valid) one.
	invoice := finance.Invoice{
		ClubName:      "Klokkarvik Båtlag Test",
		OrgNumber:     "999999999",
		ClubAddress:   "Brygga 1, 5378 Klokkarvik",
		MemberName:    "Test User",
		MemberAddress: "Stranda 4, 5378 Klokkarvik",
		InvoiceNumber: 42,
		IssueDate:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		DueDate:       time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		KID:           "000000420019",
		BankAccount:   originalBank,
		Lines: []finance.InvoiceLine{
			{Description: "Medlemskontingent 2026", Quantity: 1, UnitPrice: 450.00},
		},
	}
	originalPDF, err := finance.GeneratePDF(invoice)
	if err != nil {
		t.Fatalf("generating original PDF: %v", err)
	}

	var invoiceID string
	if err := db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, pdf_data, fiscal_period_id,
		    recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id`,
		clubID, userID, invoice.InvoiceNumber, invoice.KID, invoice.DueDate,
		450.00, originalPDF, periodID,
		"private", "test@example.com", time.Now(),
	).Scan(&invoiceID); err != nil {
		t.Fatalf("inserting invoice: %v", err)
	}

	// Add one invoice_lines row so loadInvoiceLinesForPDF returns
	// something meaningful for the regenerate path.
	if _, err := db.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total)
		 VALUES ($1, $2, $3, $4, $5)`,
		invoiceID, "Medlemskontingent 2026", 1, 450.00, 450.00,
	); err != nil {
		t.Fatalf("inserting invoice line: %v", err)
	}

	// Snapshot every preserved field BEFORE regenerate.
	before := loadInvoiceSnapshot(t, db, invoiceID)

	// Switch the club bank account to the new value, then regenerate.
	newBank := "9999.88.77777"
	if _, err := db.Exec(ctx,
		`UPDATE clubs SET bank_account = $1 WHERE id = $2`,
		newBank, clubID,
	); err != nil {
		t.Fatalf("switching club bank account: %v", err)
	}

	h := &InvoiceHandler{
		db:     db,
		config: &config.Config{},
		email:  nil,
		audit:  audit.NewService(db, zerolog.Nop()),
		log:    zerolog.Nop(),
	}
	clubFields, err := loadClubInvoiceFields(ctx, db, clubID)
	if err != nil {
		t.Fatalf("loading club invoice fields: %v", err)
	}
	if err := h.regenerateOnePDF(ctx, clubID, userID, "", invoiceID, clubFields); err != nil {
		t.Fatalf("regenerateOnePDF: %v", err)
	}

	after := loadInvoiceSnapshot(t, db, invoiceID)

	// Invariants — every field except pdf_data MUST be identical.
	if before.invoiceNumber != after.invoiceNumber {
		t.Errorf("invoice_number changed: %d -> %d", before.invoiceNumber, after.invoiceNumber)
	}
	if before.kidNumber != after.kidNumber {
		t.Errorf("kid_number changed: %q -> %q", before.kidNumber, after.kidNumber)
	}
	if !before.dueDate.Equal(after.dueDate) {
		t.Errorf("due_date changed: %v -> %v", before.dueDate, after.dueDate)
	}
	if !before.issueDate.Equal(after.issueDate) {
		t.Errorf("issue_date changed: %v -> %v", before.issueDate, after.issueDate)
	}
	if before.totalAmount != after.totalAmount {
		t.Errorf("total_amount changed: %v -> %v", before.totalAmount, after.totalAmount)
	}
	if before.status != after.status {
		t.Errorf("status changed: %q -> %q", before.status, after.status)
	}
	if !timesEqual(before.sentAt, after.sentAt) {
		t.Errorf("sent_at changed: %v -> %v", before.sentAt, after.sentAt)
	}
	if before.userID != after.userID {
		t.Errorf("user_id changed: %v -> %v", before.userID, after.userID)
	}
	if before.recipientKind != after.recipientKind {
		t.Errorf("recipient_kind changed: %q -> %q", before.recipientKind, after.recipientKind)
	}
	if before.recipientEmail != after.recipientEmail {
		t.Errorf("recipient_email changed: %q -> %q", before.recipientEmail, after.recipientEmail)
	}

	// pdf_data MUST be present and different from the original.
	if len(after.pdfData) == 0 {
		t.Fatal("pdf_data is empty after regenerate")
	}
	if bytesEqual(before.pdfData, after.pdfData) {
		t.Error("pdf_data is byte-identical after regenerate — regen didn't run?")
	}

	// Prior PDF MUST be in the archive table with reason='regenerate'.
	var archiveCount int
	var archivedBytes int
	var archivedReason string
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*),
		        COALESCE(MAX(octet_length(pdf_data)), 0),
		        COALESCE(MAX(reason), '')
		   FROM invoice_pdf_archive WHERE invoice_id = $1`,
		invoiceID,
	).Scan(&archiveCount, &archivedBytes, &archivedReason); err != nil {
		t.Fatalf("counting archive rows: %v", err)
	}
	if archiveCount != 1 {
		t.Errorf("expected 1 archive row, got %d", archiveCount)
	}
	if archivedBytes != len(before.pdfData) {
		t.Errorf("archived bytes length %d != original pdf length %d", archivedBytes, len(before.pdfData))
	}
	if archivedReason != "regenerate" {
		t.Errorf("archive reason = %q, want %q", archivedReason, "regenerate")
	}
}

type invoiceSnapshot struct {
	invoiceNumber  int
	kidNumber      string
	dueDate        time.Time
	issueDate      time.Time
	totalAmount    float64
	status         string
	sentAt         *time.Time
	userID         string
	recipientKind  string
	recipientEmail string
	pdfData        []byte
}

func loadInvoiceSnapshot(t *testing.T, db *pgxpool.Pool, invoiceID string) invoiceSnapshot {
	t.Helper()
	var s invoiceSnapshot
	var userIDPtr *string
	if err := db.QueryRow(context.Background(),
		`SELECT invoice_number, kid_number, due_date, issue_date,
		        total_amount, status, sent_at, user_id,
		        recipient_kind, COALESCE(recipient_email, ''),
		        pdf_data
		   FROM invoices WHERE id = $1`,
		invoiceID,
	).Scan(&s.invoiceNumber, &s.kidNumber, &s.dueDate, &s.issueDate,
		&s.totalAmount, &s.status, &s.sentAt, &userIDPtr,
		&s.recipientKind, &s.recipientEmail, &s.pdfData); err != nil {
		t.Fatalf("loading invoice snapshot: %v", err)
	}
	if userIDPtr != nil {
		s.userID = *userIDPtr
	}
	return s
}

func seedFiscalPeriod(t *testing.T, db *pgxpool.Pool, clubID string, year int) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, $3, $4, 'open')
		 RETURNING id`,
		clubID, year,
		time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC),
	).Scan(&id); err != nil {
		t.Fatalf("seeding fiscal period: %v", err)
	}
	return id
}

func timesEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
