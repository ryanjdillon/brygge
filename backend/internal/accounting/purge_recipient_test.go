package accounting

import (
	"context"
	"fmt"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// PurgeExpiredRecipientPII scrubs a dormant non-member recipient past the
// retention window, leaves one still inside the window, never touches an
// organization (legal person), and never touches a recipient with an open
// invoice.
func TestPurgeExpiredRecipientPII(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	// A fiscal period per year we reference, so invoices insert cleanly.
	mkPeriod := func(year int) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
			 VALUES ($1, $2, make_date($2,1,1), make_date($2,12,31), 'open') RETURNING id`,
			clubID, year,
		).Scan(&id); err != nil {
			t.Fatalf("period %d: %v", year, err)
		}
		return id
	}

	mkRecipient := func(kind, name, email string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO billing_recipients (club_id, kind, org_name, contact_person, email)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			clubID, kind,
			ifElse(kind == "organization", name, ""),
			ifElse(kind == "organization", "", name), email,
		).Scan(&id); err != nil {
			t.Fatalf("recipient: %v", err)
		}
		return id
	}

	// invoice linked to a recipient in a given year, paid or open.
	mkInvoice := func(num int, year int, periodID, recipientID string, paid bool, memberName, email string) {
		var payment any
		if paid {
			var pid string
			if err := pool.QueryRow(ctx,
				`INSERT INTO payments (club_id, recipient_id, type, amount, status, paid_at)
				 VALUES ($1, $2, 'dues', 100, 'completed', now()) RETURNING id`,
				clubID, recipientID,
			).Scan(&pid); err != nil {
				t.Fatalf("payment: %v", err)
			}
			payment = pid
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO invoices
			   (club_id, user_id, recipient_id, invoice_number, kid_number, issue_date,
			    due_date, total_amount, status, fiscal_period_id, recipient_kind,
			    member_name, recipient_email, payment_id, sent_at)
			 VALUES ($1, NULL, $2, $3, $4, make_date($5,6,1), make_date($5,6,14), 100,
			    'open', $6, 'private', $7, $8, $9, now())`,
			clubID, recipientID, num, fmt.Sprintf("%012d", num), year, periodID,
			memberName, email, payment,
		); err != nil {
			t.Fatalf("invoice %d: %v", num, err)
		}
	}

	oldYear := 2018 // well past retention (2018 + 5 + 1 = 2024 clears)
	nowYear := 2026

	pOld := mkPeriod(oldYear)
	pNow := mkPeriod(nowYear)

	// (a) dormant non-member: only an old paid invoice → eligible.
	dormant := mkRecipient("private_nonmember", "Guest Dormant", "dormant@example.com")
	mkInvoice(1001, oldYear, pOld, dormant, true, "Guest Dormant", "dormant@example.com")

	// (b) recent non-member: a within-window paid invoice → NOT eligible.
	recent := mkRecipient("private_nonmember", "Guest Recent", "recent@example.com")
	mkInvoice(1002, nowYear, pNow, recent, true, "Guest Recent", "recent@example.com")

	// (c) non-member with an OPEN old invoice → NOT eligible (still owes / active).
	openOwed := mkRecipient("private_nonmember", "Guest Open", "open@example.com")
	mkInvoice(1003, oldYear, pOld, openOwed, false, "Guest Open", "open@example.com")

	// (d) organization, dormant old invoice → NEVER purged (legal person).
	org := mkRecipient("organization", "Gammel Bedrift AS", "post@bedrift.example")
	mkInvoice(1004, oldYear, pOld, org, true, "", "post@bedrift.example")

	n, err := svc.PurgeExpiredRecipientPII(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d, want 1 (only the dormant non-member)", n)
	}

	scrubbed := func(id string) (contact, email string, purgedSet bool) {
		if err := pool.QueryRow(ctx,
			`SELECT contact_person, email, pii_purged_at IS NOT NULL
			   FROM billing_recipients WHERE id = $1`, id,
		).Scan(&contact, &email, &purgedSet); err != nil {
			t.Fatalf("read recipient: %v", err)
		}
		return
	}

	if c, e, p := scrubbed(dormant); c != "" || e != "" || !p {
		t.Errorf("dormant recipient not scrubbed: contact=%q email=%q purged=%v", c, e, p)
	}
	if _, _, p := scrubbed(recent); p {
		t.Error("recent recipient must not be purged (within retention)")
	}
	if _, _, p := scrubbed(openOwed); p {
		t.Error("recipient with an open invoice must not be purged")
	}
	if c, _, p := scrubbed(org); p || c != "" {
		t.Errorf("organization must never be purged: purged=%v contact=%q", p, c)
	}

	// Idempotent second run: the dormant one is already scrubbed → nothing.
	if n2, err := svc.PurgeExpiredRecipientPII(ctx); err != nil || n2 != 0 {
		t.Errorf("second run = %d (err %v), want 0", n2, err)
	}

	_ = userID
}

func ifElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
