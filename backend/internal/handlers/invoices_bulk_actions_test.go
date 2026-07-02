package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	if err := h.regenerateOnePDF(ctx, clubID, userID, "", invoiceID, clubFields, ""); err != nil {
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

// TestReissueOneKID verifies the Phase B reissue path: an invoice carrying
// the legacy mod-10 KID is reissued to the club's configured mod-11 KID, the
// PDF is rebuilt with the new reference (prior PDF archived under
// reason='reissue_kid'), and paid / already-correct invoices are skipped so
// booked reconciliation is never disturbed.
func TestReissueOneKID(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	if _, err := db.Exec(ctx,
		`UPDATE clubs SET name = 'Klokkarvik Båtlag Test', org_number = '999999999',
		                  address = 'Brygga 1, 5378 Klokkarvik', bank_account = '1234.56.78901',
		                  kid_check_method = 'mod11'
		   WHERE id = $1`, clubID,
	); err != nil {
		t.Fatalf("updating club: %v", err)
	}

	periodID := seedFiscalPeriod(t, db, clubID, 2026)

	// The invoice starts life with the legacy mod-10 KID for its number.
	// #74 is a real Klokkarvik case whose mod-10 KID (…40019) fails mod-11,
	// so the two schemes yield different check digits and reissue has work.
	const invoiceNumber = 74
	oldKID, err := finance.GenerateKIDMethod("000", invoiceNumber, 1, finance.KIDMethodMod10)
	if err != nil {
		t.Fatalf("generating mod-10 KID: %v", err)
	}
	wantKID, err := finance.GenerateKIDMethod("000", invoiceNumber, 1, finance.KIDMethodMod11)
	if err != nil {
		t.Fatalf("generating mod-11 KID: %v", err)
	}
	if oldKID == wantKID {
		t.Skip("invoice #42's mod-10 and mod-11 KIDs coincide — pick another number to exercise reissue")
	}

	invoice := finance.Invoice{
		ClubName: "Klokkarvik Båtlag Test", OrgNumber: "999999999",
		ClubAddress: "Brygga 1, 5378 Klokkarvik", MemberName: "Test User",
		MemberAddress: "Stranda 4", InvoiceNumber: invoiceNumber,
		IssueDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		DueDate:   time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		KID:       oldKID, BankAccount: "1234.56.78901",
		Lines: []finance.InvoiceLine{{Description: "Kontingent 2026", Quantity: 1, UnitPrice: 450}},
	}
	originalPDF, err := finance.GeneratePDF(invoice)
	if err != nil {
		t.Fatalf("generating PDF: %v", err)
	}

	var invoiceID string
	if err := db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, pdf_data, fiscal_period_id,
		    recipient_kind, recipient_email, sent_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'private','test@example.com', now())
		 RETURNING id`,
		clubID, userID, invoiceNumber, oldKID, invoice.DueDate,
		450.00, originalPDF, periodID,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("inserting invoice: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total)
		 VALUES ($1, 'Kontingent 2026', 1, 450.00, 450.00)`, invoiceID,
	); err != nil {
		t.Fatalf("inserting line: %v", err)
	}

	h := &InvoiceHandler{
		db:     db,
		config: &config.Config{},
		audit:  audit.NewService(db, zerolog.Nop()),
		log:    zerolog.Nop(),
	}
	clubFields, err := loadClubInvoiceFields(ctx, db, clubID)
	if err != nil {
		t.Fatalf("loading club fields: %v", err)
	}

	if err := h.reissueOneKID(ctx, clubID, userID, "", invoiceID, clubFields, finance.KIDMethodMod11); err != nil {
		t.Fatalf("reissueOneKID: %v", err)
	}

	after := loadInvoiceSnapshot(t, db, invoiceID)
	if after.kidNumber != wantKID {
		t.Errorf("kid_number = %q, want mod-11 %q (was %q)", after.kidNumber, wantKID, oldKID)
	}
	if !finance.ValidateKIDMethod(after.kidNumber, finance.KIDMethodMod11) {
		t.Errorf("reissued KID %q does not validate as mod-11", after.kidNumber)
	}
	if bytesEqual(originalPDF, after.pdfData) {
		t.Error("pdf_data unchanged after reissue — PDF was not rebuilt with the new KID")
	}

	var archiveReason string
	if err := db.QueryRow(ctx,
		`SELECT COALESCE(MAX(reason), '') FROM invoice_pdf_archive WHERE invoice_id = $1`,
		invoiceID,
	).Scan(&archiveReason); err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if archiveReason != "reissue_kid" {
		t.Errorf("archive reason = %q, want %q", archiveReason, "reissue_kid")
	}

	// Re-running is a no-op now that the KID already matches the club method.
	if err := h.reissueOneKID(ctx, clubID, userID, "", invoiceID, clubFields, finance.KIDMethodMod11); !errors.Is(err, errSkip) {
		t.Errorf("second reissue: err = %v, want errSkip (KID already correct)", err)
	}

	// A paid invoice must be skipped — its KID backs a booked payment/bilag.
	var paymentID string
	if err := db.QueryRow(ctx,
		`INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
		 VALUES ($1, $2, 'dues', 450.00, 'completed', now()) RETURNING id`,
		clubID, userID,
	).Scan(&paymentID); err != nil {
		t.Fatalf("inserting payment: %v", err)
	}
	var paidID string
	paidKID, _ := finance.GenerateKIDMethod("000", 43, 1, finance.KIDMethodMod10)
	if err := db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, fiscal_period_id, recipient_kind, recipient_email,
		    sent_at, payment_id)
		 VALUES ($1,$2,43,$3,$4,450.00,$5,'private','p@example.com', now(), $6)
		 RETURNING id`,
		clubID, userID, paidKID, invoice.DueDate, periodID, paymentID,
	).Scan(&paidID); err != nil {
		t.Fatalf("inserting paid invoice: %v", err)
	}
	if err := h.reissueOneKID(ctx, clubID, userID, "", paidID, clubFields, finance.KIDMethodMod11); !errors.Is(err, errSkip) {
		t.Errorf("paid invoice reissue: err = %v, want errSkip", err)
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

// seedPriceItem inserts a flat, batch-enabled price item and returns its ID.
func seedPriceItem(t *testing.T, db *pgxpool.Pool, clubID, category, name string, amount float64) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		`INSERT INTO price_items
		   (club_id, category, name, description, amount, currency, unit,
		    is_active, show_in_batch, pricing_kind, sort_order,
		    audience, metadata, installments_allowed, max_installments)
		 VALUES ($1, $2, $3, $4, $5, 'NOK', 'year',
		    true, true, 'flat', 0,
		    'all', '{}', false, 1)
		 RETURNING id`,
		clubID, category, name, name, amount,
	).Scan(&id); err != nil {
		t.Fatalf("seeding price item %q: %v", name, err)
	}
	return id
}

// doBulkRequest calls HandleBulkCreateInvoices and returns the parsed response.
func doBulkRequest(t *testing.T, h *InvoiceHandler, userID, clubID, periodID string, priceItemIDs []string, dueDate string) bulkInvoiceResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"user_ids":         []string{userID},
		"fiscal_period_id": periodID,
		"price_item_ids":   priceItemIDs,
		"due_date":         dueDate,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/financials/invoices/bulk", bytes.NewReader(body))
	req = req.WithContext(withTestClaims(req.Context(), "board-user", clubID, []string{"board"}))
	rec := httptest.NewRecorder()
	h.HandleBulkCreateInvoices(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk create returned %d: %s", rec.Code, rec.Body.String())
	}
	var resp bulkInvoiceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding bulk response: %v", err)
	}
	return resp
}

// TestBulkCreateInvoicesDedup exercises the per-line idempotency check
// that prevents double-billing a member for the same category within one
// fiscal period.
//
// Case A — same tier: re-submitting the identical price item for the same
// user + period must produce zero new invoices; the user lands in Skipped.
//
// Case B — changed tier: submitting a different price item that shares the
// same category (simulating a tier upgrade where the old item is still
// recorded) also produces zero new invoices, because dedup is keyed on
// (user, category, period), not on price_item_id.
func TestBulkCreateInvoicesDedup(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	if _, err := db.Exec(ctx,
		`UPDATE clubs SET name = 'Test Båtlag', org_number = '999999999',
		                  address = 'Brygga 1', bank_account = '1234.56.78901'
		   WHERE id = $1`, clubID,
	); err != nil {
		t.Fatalf("updating club: %v", err)
	}

	periodID := seedFiscalPeriod(t, db, clubID, 2026)
	dueDate := fmt.Sprintf("%d-06-01", 2026)

	h := &InvoiceHandler{
		db:     db,
		config: &config.Config{},
		audit:  audit.NewService(db, zerolog.Nop()),
		log:    zerolog.Nop(),
	}

	tierA := seedPriceItem(t, db, clubID, "membership", "Kontingent standard", 450)
	tierB := seedPriceItem(t, db, clubID, "membership", "Kontingent senior", 300)

	// ── Case A: same tier re-submitted ────────────────────────────────────
	r1 := doBulkRequest(t, h, userID, clubID, periodID, []string{tierA}, dueDate)
	if len(r1.Created) != 1 {
		t.Fatalf("case A first call: expected 1 created, got %d (skipped: %v)", len(r1.Created), r1.Skipped)
	}
	if len(r1.Skipped) != 0 {
		t.Errorf("case A first call: expected 0 skipped, got %d", len(r1.Skipped))
	}

	r2 := doBulkRequest(t, h, userID, clubID, periodID, []string{tierA}, dueDate)
	if len(r2.Created) != 0 {
		t.Errorf("case A second call: expected 0 created, got %d", len(r2.Created))
	}
	if len(r2.Skipped) != 1 {
		t.Errorf("case A second call: expected 1 skipped, got %d", len(r2.Skipped))
	}

	// ── Case B: different tier, same category ─────────────────────────────
	// Tier B shares category "membership" with tier A which was already billed.
	r3 := doBulkRequest(t, h, userID, clubID, periodID, []string{tierB}, dueDate)
	if len(r3.Created) != 0 {
		t.Errorf("case B (changed tier): expected 0 created, got %d", len(r3.Created))
	}
	if len(r3.Skipped) != 1 {
		t.Errorf("case B (changed tier): expected 1 skipped, got %d", len(r3.Skipped))
	}
}
