package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Every export query, and the deletion-request list, must plan against the
// migrated schema: a renamed or dropped column fails here, not as a 500 to
// the member who asked for their data.
func TestIntegration_DataExportQueriesMatchSchema(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	const someID = "00000000-0000-0000-0000-000000000001"

	for _, s := range dataExportSections {
		if _, err := db.Exec(ctx, "EXPLAIN "+s.sql(), someID, someID); err != nil {
			t.Errorf("export section %q does not match the schema: %v", s.key, err)
		}
	}
	if _, err := db.Exec(ctx, "EXPLAIN "+deletionRequestListSQL, someID); err != nil {
		t.Errorf("deletion request list does not match the schema: %v", err)
	}
}

// exportSeed is one synthetic member with a row in every export section.
// Each text value carries the member's tag, so the other member's export can
// be searched for it.
type exportSeed struct {
	userID, email, tag   string
	boatID, invoiceID    string
	paymentID, orderID   string
	kid, boardRecipient  string
	invoiceNumber        int
	slipNumber, bookNote string
}

func seedExportMember(t *testing.T, db *pgxpool.Pool, clubID, tag string, invoiceNumber int) exportSeed {
	t.Helper()
	ctx := context.Background()
	userID, email := testutil.SeedUser(t, db, clubID, []string{"member"})
	s := exportSeed{
		userID: userID, email: email, tag: tag, invoiceNumber: invoiceNumber,
		kid:            "77" + testutil.RandomHex(4),
		boardRecipient: "board-" + tag + "@example.test",
		slipNumber:     "S" + tag,
		bookNote:       "note-" + tag,
	}
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

	exec("profile", `UPDATE users SET address_line = $2, postal_code = '5000', city = 'Testby',
	        is_local = true, address_verified = true, address_kommunenummer = '4601',
	        address_verified_at = '2026-01-02T03:04:05Z', member_since = '2020-03-01',
	        harbor_share_owner = true, harbor_share_paid_on = '2021-04-01',
	        admin_notes = $3
	  WHERE id = $1`, userID, "Street "+tag, "admin note "+tag)
	scan("boat", `INSERT INTO boats (user_id, club_id, name, registration_number, manufacturer, model, length_m)
	        VALUES ($1, $2, $3, $4, 'Testmaker', 'T-20', 6.5) RETURNING id`,
		&s.boatID, userID, clubID, "Boat-"+tag, "REG-"+tag)

	var slipID string
	scan("slip", `INSERT INTO slips (club_id, number, section) VALUES ($1, $2, 'S') RETURNING id`,
		&slipID, clubID, s.slipNumber)
	exec("slip assignment", `INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id, harbor_membership_amount)
	        VALUES ($1, $2, $3, $4, 15000)`, slipID, userID, clubID, s.boatID)
	exec("waiting list", `INSERT INTO waiting_list_entries (user_id, club_id, boat_id, position, status)
	        VALUES ($1, $2, $3, $4, 'active')`, userID, clubID, s.boatID, invoiceNumber)

	scan("payment", `INSERT INTO payments (club_id, user_id, type, amount, status, description, paid_at)
	        VALUES ($1, $2, 'dues', 450, 'completed', $3, now()) RETURNING id`,
		&s.paymentID, clubID, userID, "payment-"+tag)
	scan("faktura", `INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, total_amount, status,
	                                       issue_date, due_date, payment_id, member_name, member_email)
	        VALUES ($1, $2, $3, $4, 450, 'open', CURRENT_DATE, CURRENT_DATE + 14, $5, $6, $7) RETURNING id`,
		&s.invoiceID, clubID, userID, invoiceNumber, s.kid, s.paymentID, "Test User "+tag, email)
	exec("faktura lines", `INSERT INTO invoice_lines (invoice_id, description, sub_description, quantity, unit_price, line_total, boat_id)
	        VALUES ($1, $2, 'sub', 1, 300, 300, $3), ($1, $4, '', 1, 150, 150, NULL)`,
		s.invoiceID, "Slip fee "+tag, s.boatID, "Dues "+tag)

	scan("order", `INSERT INTO orders (club_id, user_id, status, total_amount, vipps_reference, token)
	        VALUES ($1, $2, 'paid', 450, $3, $4) RETURNING id`,
		&s.orderID, clubID, userID, "vref-"+tag, "order-token-"+tag)
	exec("order source", `INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, s.orderID, s.invoiceID)

	exec("notification pref", `INSERT INTO notification_preferences (user_id, club_id, category, enabled)
	        VALUES ($1, $2, 'news', false)`, userID, clubID)
	exec("communication pref", `INSERT INTO communication_preferences (user_id, club_id, category, email_enabled)
	        VALUES ($1, $2, 'invoices', true)`, userID, clubID)
	exec("push", `INSERT INTO push_subscriptions (user_id, club_id, endpoint, p256dh, auth, user_agent)
	        VALUES ($1, $2, $3, $4, $5, 'TestBrowser/1')`,
		userID, clubID, "https://push.example.test/sub/secret-"+tag, "p256-"+tag, "auth-"+tag)
	exec("consent", `INSERT INTO user_consents (user_id, club_id, consent_type, version)
	        VALUES ($1, $2, 'privacy', $3)`, userID, clubID, "v-"+tag)

	var resourceID string
	scan("resource", `INSERT INTO resources (club_id, type, name) VALUES ($1, 'club_room', $2) RETURNING id`,
		&resourceID, clubID, "Room "+tag)
	exec("booking", `INSERT INTO bookings (resource_id, user_id, club_id, start_date, end_date, status, notes,
	                                      guest_name, guest_email)
	        VALUES ($1, $2, $3, now() + interval '1 day', now() + interval '2 days', 'confirmed', $4,
	                $5, $6)`, resourceID, userID, clubID, s.bookNote, "Guest "+tag, "guest-"+tag+"@example.test")

	// One send to the member, one notice about them to the board.
	exec("email sends", `INSERT INTO email_sends (club_id, user_id, kind, recipient, recipient_domain, provider, status, error)
	        VALUES ($1, $2, 'invoice', $3, 'example.com', 'ses', 'accepted', NULL),
	               ($1, $2, 'membership_cancel_board_notice', $4, 'example.test', 'ses', 'failed', $5)`,
		clubID, userID, email, s.boardRecipient, "550 rejected "+s.boardRecipient)
	return s
}

func getDataExport(t *testing.T, db *pgxpool.Pool, userID, clubID string) (map[string]json.RawMessage, string) {
	t.Helper()
	h := NewGDPRHandler(db, &config.Config{Port: 8080}, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Get("/members/me/data-export", h.HandleDataExport)
	req := httptest.NewRequest(http.MethodGet, "/members/me/data-export", nil)
	req.Header.Set("Authorization", authHeader(generateTestToken(userID, clubID, []string{"member"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantDisp := `attachment; filename="data-export-` + time.Now().UTC().Format(time.DateOnly) + `.json"`
	if got := rec.Header().Get("Content-Disposition"); got != wantDisp {
		t.Errorf("Content-Disposition = %q, want %q", got, wantDisp)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode export: %v; body: %s", err, rec.Body.String())
	}
	return out, rec.Body.String()
}

func decodeSection[T any](t *testing.T, export map[string]json.RawMessage, key string) T {
	t.Helper()
	raw, ok := export[key]
	if !ok {
		t.Fatalf("section %q missing from export", key)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode section %q: %v; raw: %s", key, err, raw)
	}
	return v
}

func TestIntegration_DataExportHoldsOnlyTheMembersOwnData(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, db)
	me := seedExportMember(t, db, clubID, "alfa", 1001)
	other := seedExportMember(t, db, clubID, "bravo", 1002)

	export, body := getDataExport(t, db, me.userID, clubID)

	for _, s := range dataExportSections {
		if _, ok := export[s.key]; !ok {
			t.Errorf("section %q missing", s.key)
		}
	}
	if _, ok := export["exported_at"]; !ok {
		t.Errorf("exported_at missing")
	}

	profile := decodeSection[map[string]any](t, export, "profile")
	want := map[string]any{
		"first_name": "Test", "last_name": "User", "email": me.email, "phone": "+4700000000",
		"address_line": "Street alfa", "postal_code": "5000", "city": "Testby",
		"is_local": true, "address_verified": true, "member_since": "2020-03-01",
		"address_kommunenummer": "4601",
	}
	for k, v := range want {
		if profile[k] != v {
			t.Errorf("profile[%s] = %v, want %v", k, profile[k], v)
		}
	}
	if at, err := time.Parse(time.RFC3339, fmt.Sprint(profile["address_verified_at"])); err != nil ||
		!at.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("profile address_verified_at = %v", profile["address_verified_at"])
	}
	for _, k := range []string{"membership_cancelled_at", "membership_ends_on", "created_at"} {
		if _, ok := profile[k]; !ok {
			t.Errorf("profile lacks %s", k)
		}
	}
	// Interim until BRY-654 removes the field: the board's notes stay out of
	// the download (BRD-18), and the seeded note must not appear anywhere.
	if _, ok := profile["admin_notes"]; ok {
		t.Errorf("profile carries admin_notes")
	}

	roles := decodeSection[[]map[string]any](t, export, "roles")
	if len(roles) != 1 || roles[0]["role"] != "member" {
		t.Errorf("roles = %v, want [member]", roles)
	}

	share := decodeSection[map[string]any](t, export, "harbor_share")
	if share["owner"] != true || share["paid_on"] != "2021-04-01" {
		t.Errorf("harbor_share = %v", share)
	}

	boats := decodeSection[[]map[string]any](t, export, "boats")
	if len(boats) != 1 || boats[0]["id"] != me.boatID || boats[0]["name"] != "Boat-alfa" ||
		boats[0]["registration_number"] != "REG-alfa" || boats[0]["length_m"] != 6.5 {
		t.Errorf("boats = %v", boats)
	}

	slips := decodeSection[[]map[string]any](t, export, "slip_assignments")
	if len(slips) != 1 || slips[0]["slip_label"] != "Salfa" || slips[0]["boat_id"] != me.boatID ||
		slips[0]["harbor_membership_amount"] != 15000.0 {
		t.Errorf("slip_assignments = %v", slips)
	}

	waiting := decodeSection[[]map[string]any](t, export, "waiting_list")
	if len(waiting) != 1 || waiting[0]["status"] != "active" || waiting[0]["position"] != 1001.0 {
		t.Errorf("waiting_list = %v", waiting)
	}

	fakturas := decodeSection[[]map[string]any](t, export, "fakturas")
	if len(fakturas) != 1 {
		t.Fatalf("fakturas = %v, want one", fakturas)
	}
	f := fakturas[0]
	if f["id"] != me.invoiceID || f["invoice_number"] != 1001.0 || f["kid_number"] != me.kid ||
		f["total_amount"] != 450.0 || f["status"] != "open" || f["payment_id"] != me.paymentID {
		t.Errorf("faktura = %v", f)
	}
	for _, k := range []string{"issue_date", "due_date"} {
		if s, _ := f[k].(string); s == "" {
			t.Errorf("faktura %s = %v", k, f[k])
		}
	}
	lines, _ := f["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("faktura lines = %v, want two", f["lines"])
	}
	if l := lines[0].(map[string]any); l["description"] != "Dues alfa" || l["line_total"] != 150.0 {
		t.Errorf("first line = %v", l)
	}
	if l := lines[1].(map[string]any); l["description"] != "Slip fee alfa" || l["boat_id"] != me.boatID {
		t.Errorf("second line = %v", l)
	}

	payments := decodeSection[[]map[string]any](t, export, "payments")
	if len(payments) != 1 || payments[0]["id"] != me.paymentID || payments[0]["amount"] != 450.0 ||
		payments[0]["status"] != "completed" {
		t.Errorf("payments = %v", payments)
	}

	orders := decodeSection[[]map[string]any](t, export, "orders")
	if len(orders) != 1 || orders[0]["id"] != me.orderID || orders[0]["vipps_reference"] != "vref-alfa" {
		t.Fatalf("orders = %v", orders)
	}
	if ids, _ := orders[0]["faktura_ids"].([]any); len(ids) != 1 || ids[0] != me.invoiceID {
		t.Errorf("order faktura_ids = %v", orders[0]["faktura_ids"])
	}
	if _, ok := orders[0]["token"]; ok {
		t.Errorf("order carries its pay token")
	}

	consents := decodeSection[[]map[string]any](t, export, "consents")
	if len(consents) != 1 || consents[0]["consent_type"] != "privacy" || consents[0]["version"] != "v-alfa" ||
		consents[0]["revoked_at"] != nil {
		t.Errorf("consents = %v", consents)
	}

	notif := decodeSection[[]map[string]any](t, export, "notification_preferences")
	if len(notif) != 1 || notif[0]["category"] != "news" || notif[0]["enabled"] != false {
		t.Errorf("notification_preferences = %v", notif)
	}
	comm := decodeSection[[]map[string]any](t, export, "communication_preferences")
	if len(comm) != 1 || comm[0]["category"] != "invoices" || comm[0]["email_enabled"] != true {
		t.Errorf("communication_preferences = %v", comm)
	}
	push := decodeSection[[]map[string]any](t, export, "push_subscriptions")
	if len(push) != 1 || push[0]["endpoint_host"] != "push.example.test" || push[0]["user_agent"] != "TestBrowser/1" {
		t.Errorf("push_subscriptions = %v", push)
	}
	if p := payments[0]; p["allocation_only"] != false || p["description"] != "payment-alfa" {
		t.Errorf("own payment should be whole: %v", p)
	}
	if o := orders[0]; o["allocation_only"] != false || o["total_amount"] != 450.0 {
		t.Errorf("own order should be whole: %v", o)
	}

	bookings := decodeSection[[]map[string]any](t, export, "bookings")
	if len(bookings) != 1 || bookings[0]["resource_name"] != "Room alfa" || bookings[0]["notes"] != me.bookNote ||
		bookings[0]["status"] != "confirmed" {
		t.Errorf("bookings = %v", bookings)
	}

	sends := decodeSection[[]map[string]any](t, export, "email_sends")
	if len(sends) != 2 {
		t.Fatalf("email_sends = %v, want two", sends)
	}
	byKind := map[string]map[string]any{}
	for _, s := range sends {
		byKind[s["kind"].(string)] = s
	}
	if s := byKind["invoice"]; s == nil || s["recipient"] != me.email || s["status"] != "accepted" || s["created_at"] == nil {
		t.Errorf("send to the member = %v", s)
	}
	if s := byKind["membership_cancel_board_notice"]; s == nil || s["recipient"] != nil {
		t.Errorf("notice to the board = %v, want its recipient withheld", s)
	}

	// Nothing of the other member, the board's address, the admin's note,
	// the booking's guest or the order's pay token.
	for _, leak := range []string{
		other.userID, other.email, other.boatID, other.invoiceID, other.paymentID, other.orderID, other.kid,
		"bravo", me.boardRecipient, "admin note", "Guest alfa", "guest-alfa@", "order-token-alfa", "550 rejected",
		"secret-alfa", "p256-alfa", "auth-alfa",
	} {
		if strings.Contains(body, leak) {
			t.Errorf("export contains %q", leak)
		}
	}
}

// A member with nothing but a profile still gets every section: empty lists,
// not nulls, so the download is the same shape for everyone.
func TestIntegration_DataExportEmptySectionsAreLists(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, nil)

	export, _ := getDataExport(t, db, userID, clubID)
	for _, s := range dataExportSections {
		raw := strings.TrimSpace(string(export[s.key]))
		switch {
		case s.single && !strings.HasPrefix(raw, "{"):
			t.Errorf("section %q = %s, want an object", s.key, raw)
		case !s.single && raw != "[]":
			t.Errorf("section %q = %s, want []", s.key, raw)
		}
	}
}

// One payment and one order settle two members' fakturas. Each member sees
// only their own allocation: no description, provider reference, order
// lines or the other member's amount.
func TestIntegration_DataExportSharedPaymentShowsOwnAllocation(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	me, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	other, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	var paymentID, orderID, mine, theirs string
	scan := func(dst *string, sql string, args ...any) {
		t.Helper()
		if err := db.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// The caller is the payer of record, yet the payment also settles the
	// other member's faktura, so it is still shown as an allocation.
	scan(&paymentID, `INSERT INTO payments (club_id, user_id, type, amount, status, description, vipps_reference, paid_at)
	        VALUES ($1, $2, 'dues', 750, 'completed', 'Bank text naming Other Payer', 'pay-ref-shared', now())
	        RETURNING id`, clubID, me)
	const faktura = `INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, total_amount, status,
	                                       issue_date, due_date, payment_id)
	        VALUES ($1, $2, $3, $4, $5, 'open', CURRENT_DATE, CURRENT_DATE, $6) RETURNING id`
	scan(&mine, faktura, clubID, me, 2001, "7700001", 300, paymentID)
	scan(&theirs, faktura, clubID, other, 2002, "7700002", 450, paymentID)
	scan(&orderID, `INSERT INTO orders (club_id, user_id, status, total_amount, vipps_reference)
	        VALUES ($1, $2, 'paid', 750, 'order-ref-shared') RETURNING id`, clubID, other)
	if _, err := db.Exec(ctx, `INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2), ($1, $3)`,
		orderID, mine, theirs); err != nil {
		t.Fatalf("seed order sources: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO order_lines (order_id, name, quantity, unit_price, total_price)
	        VALUES ($1, 'Line for the other member', 1, 450, 450)`, orderID); err != nil {
		t.Fatalf("seed order line: %v", err)
	}

	for _, tc := range []struct {
		name, userID, invoiceID string
		own                     float64
	}{
		{"payer of record", me, mine, 300},
		{"other member", other, theirs, 450},
	} {
		t.Run(tc.name, func(t *testing.T) {
			export, body := getDataExport(t, db, tc.userID, clubID)

			payments := decodeSection[[]map[string]any](t, export, "payments")
			if len(payments) != 1 {
				t.Fatalf("payments = %v, want one", payments)
			}
			p := payments[0]
			if p["id"] != paymentID || p["allocation_only"] != true || p["amount"] != tc.own ||
				p["description"] != nil || p["vipps_reference"] != nil {
				t.Errorf("payment = %v, want only the caller's allocation %v", p, tc.own)
			}

			orders := decodeSection[[]map[string]any](t, export, "orders")
			if len(orders) != 1 {
				t.Fatalf("orders = %v, want one", orders)
			}
			o := orders[0]
			if o["id"] != orderID || o["allocation_only"] != true || o["total_amount"] != tc.own ||
				o["vipps_reference"] != nil {
				t.Errorf("order = %v, want only the caller's allocation %v", o, tc.own)
			}
			if lines, _ := o["lines"].([]any); len(lines) != 0 {
				t.Errorf("order lines = %v, want none", o["lines"])
			}
			if ids, _ := o["faktura_ids"].([]any); len(ids) != 1 || ids[0] != tc.invoiceID {
				t.Errorf("order faktura_ids = %v, want only %s", o["faktura_ids"], tc.invoiceID)
			}

			for _, leak := range []string{"Bank text", "pay-ref-shared", "order-ref-shared", "Line for the other"} {
				if strings.Contains(body, leak) {
					t.Errorf("export contains %q", leak)
				}
			}
		})
	}
}
