package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// An organization-recipient invoice (no member) that gets a receivables-credit
// bank match links a payment through a billing_recipients row and flips to
// paid — the BRY-234 fix. Before it, linkInvoicePayment early-returned on
// user_id IS NULL and the invoice stayed forever unpaid.
func TestLinkInvoicePaymentOrgRecipient(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("period: %v", err)
	}

	// Organization invoice: no user_id, org recipient snapshot set.
	var invID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_org_name,
		    recipient_org_number, recipient_email, sent_at)
		 VALUES ($1, NULL, 300, '000000030015', '2026-06-14', 2000.00,
		    'open', $2, 'organization', 'Testbedrift AS', '999888777',
		    'post@testbedrift.example', now())
		 RETURNING id`,
		clubID, periodID,
	).Scan(&invID); err != nil {
		t.Fatalf("insert org invoice: %v", err)
	}

	// Baseline: linking works even though there is no member.
	if err := svc.linkInvoicePayment(ctx, clubID, invID, time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("linkInvoicePayment: %v", err)
	}

	// Invoice now reads paid via a recipient (not member) payment.
	var paymentID *string
	if err := pool.QueryRow(ctx,
		`SELECT payment_id FROM invoices WHERE id = $1`, invID,
	).Scan(&paymentID); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if paymentID == nil {
		t.Fatal("org invoice payment_id not set — still unpaid")
	}

	var (
		payUser      *string
		payRecipient *string
		recipKind    string
		recipOrgName string
	)
	if err := pool.QueryRow(ctx,
		`SELECT p.user_id, p.recipient_id, br.kind, br.org_name
		   FROM payments p JOIN billing_recipients br ON br.id = p.recipient_id
		  WHERE p.id = $1`, *paymentID,
	).Scan(&payUser, &payRecipient, &recipKind, &recipOrgName); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if payUser != nil {
		t.Errorf("recipient payment must have NULL user_id, got %v", *payUser)
	}
	if payRecipient == nil {
		t.Fatal("payment.recipient_id not set")
	}
	if recipKind != "organization" || recipOrgName != "Testbedrift AS" {
		t.Errorf("recipient = %q/%q, want organization/Testbedrift AS", recipKind, recipOrgName)
	}

	// Idempotent: a second call is a no-op (invoice already has payment_id).
	if err := svc.linkInvoicePayment(ctx, clubID, invID, time.Now()); err != nil {
		t.Fatalf("second link: %v", err)
	}
	var payCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE recipient_id = $1`, *payRecipient,
	).Scan(&payCount); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payCount != 1 {
		t.Errorf("expected exactly 1 recipient payment, got %d", payCount)
	}

	// A second org invoice to the same org (by org_number) reuses the recipient.
	var inv2 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_org_name,
		    recipient_org_number, sent_at)
		 VALUES ($1, NULL, 301, '000003010013', '2026-06-14', 500.00,
		    'open', $2, 'organization', 'Testbedrift AS', '999888777', now())
		 RETURNING id`,
		clubID, periodID,
	).Scan(&inv2); err != nil {
		t.Fatalf("insert org invoice 2: %v", err)
	}
	if err := svc.linkInvoicePayment(ctx, clubID, inv2, time.Now()); err != nil {
		t.Fatalf("link invoice 2: %v", err)
	}
	var recipCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_recipients WHERE club_id = $1`, clubID,
	).Scan(&recipCount); err != nil {
		t.Fatalf("count recipients: %v", err)
	}
	if recipCount != 1 {
		t.Errorf("expected 1 shared recipient for both invoices, got %d", recipCount)
	}

	_ = userID
}
