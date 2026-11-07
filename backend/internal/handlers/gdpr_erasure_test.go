package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
)

// erasureSeed is a member holding everything erasure must remove or keep:
// admin notes, preferences, a consent, and three boats. billedBoat is on a
// faktura line, slipBoat holds an active slip, freeBoat only a released slip
// and a waiting-list entry.
type erasureSeed struct {
	userID                         string
	billedBoat, slipBoat, freeBoat string
	invoiceID, paymentID, slipID   string
	waitingID, releasedID          string
}

// seedErasureMember seeds the member; issueDate places their faktura inside
// the retention window (identity erasure deferred) or past it (erased now).
func seedErasureMember(t *testing.T, db *pgxpool.Pool, clubID, userID, issueDate string) erasureSeed {
	t.Helper()
	ctx := context.Background()
	s := erasureSeed{userID: userID}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	scan := func(what, sql string, dst *string, args ...any) {
		t.Helper()
		if err := db.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	exec("admin notes", `UPDATE users SET admin_notes = 'Synthetic board note' WHERE id = $1`, userID)
	exec("push", `INSERT INTO push_subscriptions (user_id, club_id, endpoint, p256dh, auth)
	        VALUES ($1, $2, 'https://push.example.test/1', 'k', 'a')`, userID, clubID)
	exec("notification pref", `INSERT INTO notification_preferences (user_id, club_id, category) VALUES ($1, $2, 'news')`, userID, clubID)
	exec("communication pref", `INSERT INTO communication_preferences (user_id, club_id, category) VALUES ($1, $2, 'news')`, userID, clubID)
	exec("consents", `INSERT INTO user_consents (user_id, club_id, consent_type, version)
	        VALUES ($1, $2, 'privacy', '1'), ($1, $2, 'terms_of_sale', '1'), ($1, $2, 'newsletter', '')`, userID, clubID)

	boat := func(name string) string {
		t.Helper()
		var id string
		scan("boat "+name, `INSERT INTO boats (user_id, club_id, name, registration_number, mmsi, call_sign, manufacturer, model, length_m)
		        VALUES ($1, $2, $3, 'REG-'||$3, '257000000', 'LA0000', 'Testmaker', 'T-20', 6.5) RETURNING id`,
			&id, userID, clubID, name)
		return id
	}
	s.billedBoat, s.slipBoat, s.freeBoat = boat("Billed"), boat("Slipped"), boat("Free")

	scan("payment", `INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
	        VALUES ($1, $2, 'slip_fee', 300, 'completed', now()) RETURNING id`, &s.paymentID, clubID, userID)
	scan("faktura", `INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date,
	                                       payment_id, member_name, member_email)
	        VALUES ($1, $2, $3, 300, 'open', $4::date, $4::date + 14, $5, 'Test User', 'member@example.test')
	        RETURNING id`, &s.invoiceID, clubID, userID, "88"+userID[:6], issueDate, s.paymentID)
	exec("faktura line", `INSERT INTO invoice_lines (invoice_id, description, sub_description, quantity, unit_price, line_total, boat_id)
	        VALUES ($1, 'Slip fee Billed REG-Billed', 'Billed', 1, 300, 300, $2)`, s.invoiceID, s.billedBoat)

	var slipA, slipB string
	scan("slip", `INSERT INTO slips (club_id, number) VALUES ($1, 'A1') RETURNING id`, &slipA, clubID)
	scan("slip", `INSERT INTO slips (club_id, number) VALUES ($1, 'A2') RETURNING id`, &slipB, clubID)
	scan("active slip", `INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		&s.slipID, slipA, userID, clubID, s.slipBoat)
	scan("released slip", `INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id, released_at)
	        VALUES ($1, $2, $3, $4, now() - interval '1 year') RETURNING id`,
		&s.releasedID, slipB, userID, clubID, s.freeBoat)
	scan("waiting list", `INSERT INTO waiting_list_entries (user_id, club_id, boat_id, position) VALUES ($1, $2, $3, 1) RETURNING id`,
		&s.waitingID, userID, clubID, s.freeBoat)
	return s
}

// assertErased checks what every erasure path leaves: the profile data gone,
// the free boat deleted, the referenced boats anonymised, and the faktura,
// its line and its payment exactly as they were.
func assertErased(t *testing.T, db *pgxpool.Pool, s erasureSeed) {
	t.Helper()
	ctx := context.Background()
	count := func(q string, args ...any) int { t.Helper(); return gdprCount(t, db, q, args...) }

	if n := count(`SELECT count(*) FROM users WHERE id = $1 AND admin_notes = ''`, s.userID); n != 1 {
		t.Errorf("admin_notes not cleared")
	}
	for _, tbl := range []string{"push_subscriptions", "notification_preferences", "communication_preferences"} {
		if n := count(`SELECT count(*) FROM `+tbl+` WHERE user_id = $1`, s.userID); n != 0 {
			t.Errorf("%s left = %d, want 0", tbl, n)
		}
	}

	// BRD-18: the newsletter opt-in goes at erasure; the legal-document
	// consents stay as proof until retention ends.
	if n := count(`SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = 'newsletter'`, s.userID); n != 0 {
		t.Errorf("newsletter consent left after erasure")
	}
	if n := count(`SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type IN ('privacy', 'terms_of_sale')`, s.userID); n != 2 {
		t.Errorf("legal consents after erasure = %d, want 2", n)
	}

	if n := count(`SELECT count(*) FROM boats WHERE id = $1`, s.freeBoat); n != 0 {
		t.Errorf("unreferenced boat not deleted")
	}
	if n := count(`SELECT count(*) FROM waiting_list_entries WHERE id = $1 AND boat_id IS NULL`, s.waitingID); n != 1 {
		t.Errorf("waiting-list entry should stay with its boat link cleared")
	}
	if n := count(`SELECT count(*) FROM slip_assignments WHERE id = $1 AND boat_id IS NULL`, s.releasedID); n != 1 {
		t.Errorf("released slip assignment should stay with its boat link cleared")
	}
	for _, id := range []string{s.billedBoat, s.slipBoat} {
		var name, reg, mmsi, sign, model string
		var length float64
		if err := db.QueryRow(ctx,
			`SELECT name, registration_number, mmsi, call_sign, model, length_m FROM boats WHERE id = $1`, id,
		).Scan(&name, &reg, &mmsi, &sign, &model, &length); err != nil {
			t.Fatalf("referenced boat %s should be kept: %v", id, err)
		}
		if name != "" || reg != "" || mmsi != "" || sign != "" {
			t.Errorf("boat %s kept identifying columns (%q, %q, %q, %q)", id, name, reg, mmsi, sign)
		}
		if model != "T-20" || length != 6.5 {
			t.Errorf("boat %s lost its model or size (%q, %v)", id, model, length)
		}
	}
	if n := count(`SELECT count(*) FROM slip_assignments WHERE id = $1 AND boat_id = $2 AND released_at IS NULL`,
		s.slipID, s.slipBoat); n != 1 {
		t.Errorf("active slip assignment changed")
	}

	if n := count(`SELECT count(*) FROM invoices WHERE id = $1 AND user_id = $2 AND payment_id = $3
	                 AND total_amount = 300 AND member_name = 'Test User' AND member_email = 'member@example.test'`,
		s.invoiceID, s.userID, s.paymentID); n != 1 {
		t.Errorf("faktura changed")
	}
	if n := count(`SELECT count(*) FROM invoice_lines WHERE invoice_id = $1 AND boat_id = $2
	                 AND description = 'Slip fee Billed REG-Billed' AND sub_description = 'Billed' AND line_total = 300`,
		s.invoiceID, s.billedBoat); n != 1 {
		t.Errorf("faktura line changed")
	}
	if n := count(`SELECT count(*) FROM payments WHERE id = $1 AND user_id = $2 AND amount = 300`, s.paymentID, s.userID); n != 1 {
		t.Errorf("payment changed")
	}
}

func TestIntegration_GDPRErasureClearsNotesAndBoats(t *testing.T) {
	for _, tc := range []struct {
		name      string
		issueDate string
		erased    bool
	}{
		{"identity erased now", "2015-06-01", true},
		{"identity deferred behind retention", "now", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGDPRProcessEnv(t)
			userID, email, _, requestID := e.seedMember(t, false)
			s := seedErasureMember(t, e.db, e.clubID, userID, tc.issueDate)

			rec := e.process(requestID)
			assertStatus(t, rec, http.StatusOK)
			assertErased(t, e.db, s)
			if n := gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1 AND email = $2`, userID, email); (n == 0) != tc.erased {
				t.Errorf("identity erased = %v, want %v", n == 0, tc.erased)
			}

			// The daily run deletes the legal consents of an erased member
			// whose retention has ended (the 2015 faktura), and keeps those
			// of a member still inside it.
			svc := accounting.NewService(e.db, nil, zerolog.Nop())
			if _, err := svc.PurgeRetentionClearedConsents(context.Background()); err != nil {
				t.Fatalf("PurgeRetentionClearedConsents: %v", err)
			}
			left := gdprCount(t, e.db, `SELECT count(*) FROM user_consents WHERE user_id = $1`, userID)
			if want := map[bool]int{true: 0, false: 2}[tc.erased]; left != want {
				t.Errorf("consents after the retention run = %d, want %d", left, want)
			}
		})
	}
}

func TestIntegration_AdminDeleteClearsNotesAndBoats(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)
	userID, _, _, _ := e.seedMember(t, false)
	s := seedErasureMember(t, e.db, e.clubID, userID, "now")

	assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)
	assertErased(t, e.db, s)

	// The faktura is from this year, so retention keeps the legal consents.
	svc := accounting.NewService(e.db, nil, zerolog.Nop())
	if _, err := svc.PurgeRetentionClearedConsents(context.Background()); err != nil {
		t.Fatalf("PurgeRetentionClearedConsents: %v", err)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_consents WHERE user_id = $1`, userID); n != 2 {
		t.Errorf("consents inside retention = %d, want 2", n)
	}
}
