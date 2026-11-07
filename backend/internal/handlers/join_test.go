package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type joinEnv struct {
	db          *pgxpool.Pool
	mr          *miniredis.Miniredis
	cfg         *config.Config
	clubID      string
	priceItemID string
	mailer      *email.MockSender
	h           *JoinHandler
	router      *chi.Mux
	register    *fakeAddressRegister
}

// setupJoinEnv seeds a club that takes joins: a flat, active join price item
// and every required document published. The handler runs its background
// work inline so a test sees its effects when the request returns.
func setupJoinEnv(t *testing.T) *joinEnv {
	t.Helper()
	db := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, db)
	ctx := context.Background()

	var slug string
	if err := db.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	// The club's municipality is the fake register's 4601; never the real API.
	register := newFakeAddressRegister(t)
	mustExec(t, db, ctx, `UPDATE clubs SET municipality_codes = '{4601}' WHERE id = $1`, clubID)
	cfg := &config.Config{
		ClubSlug:             slug,
		ClubName:             "Testklubben",
		Domain:               "club.example.test",
		FrontendURL:          "https://club.example.test",
		KartverketAddressURL: register.srv.URL,
	}

	var priceItemID string
	if err := db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, description, amount)
		 VALUES ($1, 'membership', 'Medlemskontingent', 'Kontingent for året', 650)
		 RETURNING id`, clubID,
	).Scan(&priceItemID); err != nil {
		t.Fatalf("seed price item: %v", err)
	}
	mustExec(t, db, ctx, `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, clubID, priceItemID)
	for _, docType := range joinRequiredDocs {
		seedLegalDoc(t, db, clubID, docType, docType+"-v1", "1 day")
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	mailer := &email.MockSender{}
	h := NewJoinHandler(db, rdb, cfg, mailer, auth.NewSessionService(db), audit.NewService(db, zerolog.Nop()), zerolog.Nop())
	h.async = func(f func()) { f() }

	r := chi.NewRouter()
	r.Get("/join/status", h.HandleJoinStatus)
	r.Get("/join/address-search", h.HandleAddressSearch)
	r.Post("/join", h.HandleJoin)
	r.Get("/join/confirm/{token}", h.HandleJoinConfirmStatus)
	r.Post("/join/confirm", h.HandleJoinConfirm)

	return &joinEnv{db: db, mr: mr, cfg: cfg, clubID: clubID, priceItemID: priceItemID, mailer: mailer, h: h, router: r, register: register}
}

func joinBody(addr string) map[string]any {
	return map[string]any{
		"first_name":   "Kari",
		"last_name":    "Testesen",
		"email":        addr,
		"phone":        "400 00 001",
		"address_line": "Testvegen 1",
		"postal_code":  "0001",
		"city":         "Testby",
		"newsletter":   true,
		"accept_terms": true,
	}
}

func (e *joinEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

var joinTokenInMail = regexp.MustCompile(`/bli-medlem/stadfest\?token=([A-Za-z0-9_-]+)`)

// lastConfirmToken is the token in the newest confirmation email to addr.
func (e *joinEnv) lastConfirmToken(t *testing.T, addr string) string {
	t.Helper()
	for i := len(e.mailer.Calls) - 1; i >= 0; i-- {
		c := e.mailer.Calls[i]
		if c.To != addr {
			continue
		}
		m := joinTokenInMail.FindStringSubmatch(c.HTML)
		if m == nil {
			t.Fatalf("newest mail to the address is not a confirmation: %q", c.Subject)
		}
		return m[1]
	}
	t.Fatalf("no mail to the address")
	return ""
}

func (e *joinEnv) mailsTo(addr string) []email.MockCall {
	var out []email.MockCall
	for _, c := range e.mailer.Calls {
		if c.To == addr {
			out = append(out, c)
		}
	}
	return out
}

func (e *joinEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (e *joinEnv) seedUser(t *testing.T, addr string, deactivated bool, userRoles ...string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, deactivated_at)
		 VALUES ($1, $2, 'Old', 'Name', CASE WHEN $3 THEN now() ELSE NULL END)
		 RETURNING id`, e.clubID, addr, deactivated,
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, role := range userRoles {
		mustExec(t, e.db, ctx, `INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, $3)`, id, e.clubID, role)
	}
	return id
}

func (e *joinEnv) roles(t *testing.T, userID string) []string {
	t.Helper()
	var rs []string
	if err := e.db.QueryRow(context.Background(),
		`SELECT COALESCE(array_agg(role::text ORDER BY role::text), '{}') FROM user_roles WHERE user_id = $1`, userID,
	).Scan(&rs); err != nil {
		t.Fatalf("roles: %v", err)
	}
	return rs
}

func (e *joinEnv) auditOutcomes(t *testing.T) []string {
	t.Helper()
	rows, err := e.db.Query(context.Background(),
		`SELECT details->>'outcome' FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at, id`,
		e.clubID, audit.ActionMembershipJoinRequested)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		out = append(out, o)
	}
	return out
}

func TestJoinHappyPath(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "kari.join@example.com"

	rec := e.do(t, http.MethodGet, "/join/status", nil)
	assertStatus(t, rec, http.StatusOK)
	status := decodeJSON[joinStatusResponse](t, rec)
	if !status.Open || status.Price == nil || status.Price.Amount != 650 || len(status.Missing) != 0 {
		t.Fatalf("status = %+v, want open with price 650", status)
	}
	if status.Year != time.Now().Year() {
		t.Errorf("status year = %d, want the server's %d", status.Year, time.Now().Year())
	}

	rec = e.do(t, http.MethodPost, "/join", joinBody("Kari.Join@Example.com"))
	assertStatus(t, rec, http.StatusAccepted)

	// Nothing but the application exists before the link is clicked.
	if n := e.count(t, `SELECT count(*) FROM users WHERE club_id = $1 AND lower(email) = $2`, e.clubID, addr); n != 0 {
		t.Fatalf("a user exists before confirmation: %d", n)
	}
	var (
		tokenHash string
		versions  map[string]string
		ttl       time.Duration
	)
	if err := e.db.QueryRow(ctx,
		`SELECT token_hash, consent_versions, expires_at - now() FROM membership_applications WHERE club_id = $1 AND email = $2`,
		e.clubID, addr,
	).Scan(&tokenHash, &versions, &ttl); err != nil {
		t.Fatalf("application row: %v", err)
	}
	if ttl < 23*time.Hour || ttl > 25*time.Hour {
		t.Errorf("application expires in %v, want about 24h", ttl)
	}
	for _, d := range joinRequiredDocs {
		if versions[d] != d+"-v1" {
			t.Errorf("consent version %s = %q, want %q", d, versions[d], d+"-v1")
		}
	}

	mails := e.mailsTo(addr)
	if len(mails) != 1 || mails[0].Subject != email.JoinConfirmSubject("nb", e.cfg.ClubName) {
		t.Fatalf("mails = %+v, want one confirmation", mails)
	}
	token := e.lastConfirmToken(t, addr)
	if joinTokenHash(token) != tokenHash || strings.Contains(tokenHash, token) {
		t.Fatal("the stored hash is not the sha256 of the mailed token")
	}
	if !strings.HasPrefix(joinConfirmURL(e.cfg, token), "https://club.example.test/bli-medlem/stadfest?token=") {
		t.Fatalf("confirm URL = %s", joinConfirmURL(e.cfg, token))
	}

	// Reading the link's status, as a scanner or the page does, uses nothing up.
	for range 2 {
		rec = e.do(t, http.MethodGet, "/join/confirm/"+token, nil)
		assertStatus(t, rec, http.StatusOK)
		st := decodeJSON[joinConfirmStatusResponse](t, rec)
		if !st.Valid || st.FirstName != "Kari" {
			t.Fatalf("confirm status = %+v, want valid for Kari", st)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE club_id = $1 AND consumed_at IS NOT NULL`, e.clubID); n != 0 {
		t.Fatal("GET consumed the application")
	}

	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusOK)
	resp := decodeJSON[joinConfirmResponse](t, rec)
	if resp.OrderToken == "" || resp.CheckoutURL != "https://club.example.test/betal/"+resp.OrderToken {
		t.Fatalf("confirm response = %+v", resp)
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "brygge_session" && c.Value != "" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("confirm set no session cookie")
	}

	// A typed address is unverified, so residency is unknown (false) and the
	// phone is stored in E.164.
	var userID, firstName, phone string
	var isLocal bool
	var verified *bool
	if err := e.db.QueryRow(ctx,
		`SELECT id, first_name, phone, is_local, address_verified FROM users WHERE club_id = $1 AND email = $2 AND kind = 'person' AND deactivated_at IS NULL`,
		e.clubID, addr,
	).Scan(&userID, &firstName, &phone, &isLocal, &verified); err != nil {
		t.Fatalf("user after confirm: %v", err)
	}
	if firstName != "Kari" || phone != "+4740000001" || isLocal || verified == nil || *verified {
		t.Errorf("user = %s %s local=%v verified=%v", firstName, phone, isLocal, verified)
	}
	if got := e.roles(t, userID); !slices.Equal(got, []string{"applicant"}) {
		t.Errorf("roles = %v, want [applicant]", got)
	}
	for _, d := range joinRequiredDocs {
		if n := e.count(t, `SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = $2 AND version = $3`,
			userID, d, d+"-v1"); n != 1 {
			t.Errorf("%s consents at v1 = %d, want 1", d, n)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = 'newsletter'`, userID); n != 1 {
		t.Errorf("newsletter consents = %d, want 1", n)
	}

	var (
		orderStatus, purpose, lineItem string
		total, lineTotal               float64
		orderUser                      string
	)
	if err := e.db.QueryRow(ctx,
		`SELECT o.status::text, o.purpose, o.total_amount::float8, o.user_id, l.price_item_id, l.total_price::float8
		   FROM orders o JOIN order_lines l ON l.order_id = o.id
		  WHERE o.token = $1`, resp.OrderToken,
	).Scan(&orderStatus, &purpose, &total, &orderUser, &lineItem, &lineTotal); err != nil {
		t.Fatalf("join order: %v", err)
	}
	if orderStatus != "pending" || purpose != "membership_join" || total != 650 || lineTotal != 650 ||
		orderUser != userID || lineItem != e.priceItemID {
		t.Errorf("order = %s %s %v %s %s %v", orderStatus, purpose, total, orderUser, lineItem, lineTotal)
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE club_id = $1 AND consumed_at IS NOT NULL`, e.clubID); n != 1 {
		t.Error("the application is not consumed")
	}
	if n := e.count(t, `SELECT count(*) FROM sessions WHERE id = $1 AND user_id = $2`, sessionCookie.Value, userID); n != 1 {
		t.Error("no session row for the cookie")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_id = $2 AND resource_id = $2::text`,
		audit.ActionMembershipJoinConfirmed, userID); n != 1 {
		t.Error("no membership.join_confirmed audit row")
	}
	if got := e.auditOutcomes(t); !slices.Equal(got, []string{joinOutcomeNew}) {
		t.Errorf("join_requested outcomes = %v", got)
	}
	var details string
	if err := e.db.QueryRow(ctx, `SELECT details::text FROM audit_log WHERE action = $1`, audit.ActionMembershipJoinRequested).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(details), "kari") || !strings.Contains(details, `"email_hmac": "`+e.h.emailHMAC(addr)+`"`) {
		t.Errorf("join_requested details = %s, want the address HMAC only", details)
	}

	// The applicant's session does not get in the way of the pay page.
	inv := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
	pay := chi.NewRouter()
	pay.Get("/checkout/{token}", inv.HandleGetCheckout)
	req := httptest.NewRequest(http.MethodGet, "/checkout/"+resp.OrderToken, nil)
	req.AddCookie(sessionCookie)
	payRec := httptest.NewRecorder()
	pay.ServeHTTP(payRec, req)
	assertStatus(t, payRec, http.StatusOK)
	checkout := decodeJSON[checkoutResp](t, payRec)
	if checkout.Total != 650 || len(checkout.Lines) != 1 || checkout.Lines[0].Name != "Medlemskontingent" || checkout.AlreadyPaid {
		t.Errorf("checkout = %+v", checkout)
	}

	// The link is single-use.
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusGone)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinTokenUsed {
		t.Errorf("reuse code = %q", code)
	}
	if n := e.count(t, `SELECT count(*) FROM orders WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("orders after reuse = %d, want 1", n)
	}
}

// Every R7 branch answers the same 202 body; only the email differs, and only
// the branches that may become an applicant leave an application behind.
func TestJoinBranchesSameResponse(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()

	pending := "pending@example.com"
	mustExec(t, e.db, ctx,
		`INSERT INTO membership_applications (club_id, first_name, last_name, email, token_hash, expires_at)
		 VALUES ($1, 'P', 'P', $2, 'old-hash', now() + interval '1 hour')`, e.clubID, pending)
	applicant := "applicant@example.com"
	e.seedUser(t, applicant, false, "applicant")
	former := "former@example.com"
	e.seedUser(t, former, true, "member")
	member := "member@example.com"
	memberID := e.seedUser(t, member, false, "member")

	cases := []struct {
		addr, outcome string
		confirm       bool
	}{
		{"new@example.com", joinOutcomeNew, true},
		{pending, joinOutcomeResent, true},
		{applicant, joinOutcomeApplicant, true},
		{former, joinOutcomeReactivation, true},
		{member, joinOutcomeMember, false},
	}
	var firstBody string
	for _, c := range cases {
		rec := e.do(t, http.MethodPost, "/join", joinBody(c.addr))
		assertStatus(t, rec, http.StatusAccepted)
		if firstBody == "" {
			firstBody = rec.Body.String()
		} else if rec.Body.String() != firstBody {
			t.Errorf("%s: body %q differs from %q", c.addr, rec.Body.String(), firstBody)
		}
		mails := e.mailsTo(c.addr)
		if len(mails) != 1 {
			t.Fatalf("%s: %d mails, want 1", c.addr, len(mails))
		}
		wantSubject := email.JoinConfirmSubject("nb", e.cfg.ClubName)
		if !c.confirm {
			wantSubject = email.JoinAlreadyMemberSubject("nb", e.cfg.ClubName)
		}
		if mails[0].Subject != wantSubject {
			t.Errorf("%s: subject %q, want %q", c.addr, mails[0].Subject, wantSubject)
		}
		apps := e.count(t, `SELECT count(*) FROM membership_applications WHERE club_id = $1 AND email = $2`, e.clubID, c.addr)
		if c.confirm && apps != 1 {
			t.Errorf("%s: %d applications, want exactly 1 (the old one replaced)", c.addr, apps)
		}
		if !c.confirm && apps != 0 {
			t.Errorf("%s: an existing member got an application", c.addr)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE token_hash = 'old-hash'`); n != 0 {
		t.Error("the pending application's old link still works")
	}
	if n := e.count(t, `SELECT count(*) FROM magic_links WHERE email = $1 AND used = false`, member); n != 1 {
		t.Error("the member got no login link")
	}
	if !strings.Contains(e.mailsTo(member)[0].HTML, "/api/v1/auth/verify?token=") {
		t.Error("the already-member mail carries no login link")
	}
	if got := e.roles(t, memberID); !slices.Equal(got, []string{"member"}) {
		t.Errorf("member roles changed: %v", got)
	}
	want := []string{joinOutcomeNew, joinOutcomeResent, joinOutcomeApplicant, joinOutcomeReactivation, joinOutcomeMember}
	if got := e.auditOutcomes(t); !slices.Equal(got, want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}

	// The existing applicant confirms into the same user and order.
	applicantID := ""
	_ = e.db.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, applicant).Scan(&applicantID)
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, applicant)})
	assertStatus(t, rec, http.StatusOK)
	first := decodeJSON[joinConfirmResponse](t, rec)
	e.mr.FastForward(joinRecipientGap + time.Minute)
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(applicant)), http.StatusAccepted)
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, applicant)})
	assertStatus(t, rec, http.StatusOK)
	if second := decodeJSON[joinConfirmResponse](t, rec); second.OrderToken != first.OrderToken {
		t.Errorf("a second confirm opened another order: %s vs %s", second.OrderToken, first.OrderToken)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE club_id = $1 AND email = $2`, e.clubID, applicant); n != 1 {
		t.Errorf("applicant users = %d, want 1", n)
	}
	if got := e.roles(t, applicantID); !slices.Equal(got, []string{"applicant"}) {
		t.Errorf("applicant roles = %v", got)
	}
}

func TestJoinHoneypotDropsSilently(t *testing.T) {
	e := setupJoinEnv(t)
	body := joinBody("bot@example.com")
	body["website"] = "https://spam.example"
	rec := e.do(t, http.MethodPost, "/join", body)
	assertStatus(t, rec, http.StatusAccepted)
	if rec.Body.String() != e.do(t, http.MethodPost, "/join", joinBody("real@example.com")).Body.String() {
		t.Error("the honeypot answer differs from a real one")
	}
	if len(e.mailsTo("bot@example.com")) != 0 {
		t.Error("the honeypot sent mail")
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = 'bot@example.com'`); n != 0 {
		t.Error("the honeypot stored an application")
	}
	if got := e.auditOutcomes(t); len(got) != 1 {
		t.Errorf("audit rows = %v, want only the real request's", got)
	}

	// While join is closed, a filled honeypot gets the same refusal as a real
	// submission, so the answer cannot tell a bot it was spotted.
	mustExec(t, e.db, context.Background(), `UPDATE clubs SET join_price_item_id = NULL WHERE id = $1`, e.clubID)
	realRec := e.do(t, http.MethodPost, "/join", joinBody("closed.real@example.com"))
	bot := e.do(t, http.MethodPost, "/join", body)
	if realRec.Code != http.StatusConflict || bot.Code != realRec.Code || bot.Body.String() != realRec.Body.String() {
		t.Errorf("closed: honeypot %d %s, real %d %s", bot.Code, bot.Body.String(), realRec.Code, realRec.Body.String())
	}
	invalid := joinBody("x")
	invalid["website"] = "https://spam.example"
	if rec := e.do(t, http.MethodPost, "/join", invalid); rec.Code != http.StatusBadRequest {
		t.Errorf("an invalid honeypot submission answered %d, want 400 like a real one", rec.Code)
	}
}

func TestJoinValidation(t *testing.T) {
	e := setupJoinEnv(t)
	noTerms := joinBody("terms@example.com")
	noTerms["accept_terms"] = false
	rec := e.do(t, http.MethodPost, "/join", noTerms)
	assertStatus(t, rec, http.StatusBadRequest)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinTermsNotAccepted {
		t.Errorf("code = %q", code)
	}
	for _, mutate := range []func(map[string]any){
		func(b map[string]any) { b["email"] = "not-an-email" },
		func(b map[string]any) { b["email"] = "someone@system.invalid" },
		func(b map[string]any) { b["first_name"] = "" },
		func(b map[string]any) { b["city"] = "" },
		func(b map[string]any) { b["address_line"] = "" },
		func(b map[string]any) { b["phone"] = "" },
		func(b map[string]any) { b["phone"] = "12345" },
		func(b map[string]any) { b["address_id"] = strings.Repeat("9", joinMaxAddressID+1) },
		func(b map[string]any) { b["last_name"] = strings.Repeat("x", joinMaxName+1) },
	} {
		b := joinBody("valid@example.com")
		mutate(b)
		assertStatus(t, e.do(t, http.MethodPost, "/join", b), http.StatusBadRequest)
	}
	if len(e.mailer.Calls) != 0 {
		t.Error("an invalid request sent mail")
	}
}

// Every email /join sends counts against the address: one per 10 minutes,
// three a day, from whatever IP. Over budget the answer is the same 202.
func TestJoinRecipientThrottle(t *testing.T) {
	e := setupJoinEnv(t)
	addr := "throttle@example.com"

	send := func() {
		t.Helper()
		assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	}
	send()
	send()
	if n := len(e.mailsTo(addr)); n != 1 {
		t.Fatalf("mails within 10 minutes = %d, want 1", n)
	}
	e.mr.FastForward(joinRecipientGap + time.Second)
	send()
	e.mr.FastForward(joinRecipientGap + time.Second)
	send()
	if n := len(e.mailsTo(addr)); n != 3 {
		t.Fatalf("mails after two gaps = %d, want 3", n)
	}
	e.mr.FastForward(joinRecipientGap + time.Second)
	send()
	if n := len(e.mailsTo(addr)); n != 3 {
		t.Fatalf("a fourth mail went out the same day: %d", n)
	}
	got := e.auditOutcomes(t)
	if got[1] != joinOutcomeThrottled || got[4] != joinOutcomeThrottled {
		t.Errorf("outcomes = %v, want the 2nd and 5th throttled", got)
	}

	// The already-member mail counts against the same budget.
	member := "member.throttle@example.com"
	e.seedUser(t, member, false, "member")
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(member)), http.StatusAccepted)
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(member)), http.StatusAccepted)
	if n := len(e.mailsTo(member)); n != 1 {
		t.Errorf("already-member mails = %d, want 1", n)
	}
}

// With the throttle's Redis gone nothing is sent and nothing stored, and the
// visitor still gets the one answer.
func TestJoinRecipientThrottleFailsClosed(t *testing.T) {
	e := setupJoinEnv(t)
	e.mr.Close()
	rec := e.do(t, http.MethodPost, "/join", joinBody("down@example.com"))
	assertStatus(t, rec, http.StatusAccepted)
	if len(e.mailer.Calls) != 0 {
		t.Error("sent mail without the throttle")
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications`); n != 0 {
		t.Error("stored an application without the throttle")
	}
	if got := e.auditOutcomes(t); !slices.Equal(got, []string{joinOutcomeFailed}) {
		t.Errorf("outcomes = %v", got)
	}
}

func TestJoinTokenExpiryAndUnknown(t *testing.T) {
	e := setupJoinEnv(t)
	addr := "expiry@example.com"
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	token := e.lastConfirmToken(t, addr)
	mustExec(t, e.db, context.Background(),
		`UPDATE membership_applications SET expires_at = now() - interval '1 minute' WHERE email = $1`, addr)

	st := decodeJSON[joinConfirmStatusResponse](t, e.do(t, http.MethodGet, "/join/confirm/"+token, nil))
	if st.Valid || st.Code != codeJoinTokenExpired {
		t.Errorf("status of an expired link = %+v", st)
	}
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusGone)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinTokenExpired {
		t.Errorf("code = %q", code)
	}
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": "no-such-token"})
	assertStatus(t, rec, http.StatusGone)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinTokenInvalid {
		t.Errorf("unknown token code = %q", code)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE email = $1`, addr); n != 0 {
		t.Error("an expired link created a user")
	}
}

func TestJoinClosed(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, e *joinEnv)
		missing string
	}{
		{"unset", func(t *testing.T, e *joinEnv) {
			mustExec(t, e.db, context.Background(), `UPDATE clubs SET join_price_item_id = NULL WHERE id = $1`, e.clubID)
		}, joinMissingPriceItem},
		{"inactive", func(t *testing.T, e *joinEnv) {
			mustExec(t, e.db, context.Background(), `UPDATE price_items SET is_active = false WHERE id = $1`, e.priceItemID)
		}, joinMissingPriceInactive},
		{"tiered", func(t *testing.T, e *joinEnv) {
			mustExec(t, e.db, context.Background(),
				`UPDATE price_items SET pricing_kind = 'tiered', tier_dimension = 'beam' WHERE id = $1`, e.priceItemID)
		}, joinMissingPriceNotFlat},
		{"other club", func(t *testing.T, e *joinEnv) {
			other := testutil.SeedClub(t, e.db)
			mustExec(t, e.db, context.Background(), `UPDATE price_items SET club_id = $2 WHERE id = $1`, e.priceItemID, other)
		}, joinMissingPriceOtherClub},
		{"unpublished document", func(t *testing.T, e *joinEnv) {
			mustExec(t, e.db, context.Background(),
				`DELETE FROM legal_documents WHERE club_id = $1 AND doc_type = 'harbour_rules'`, e.clubID)
		}, "harbour_rules"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setupJoinEnv(t)
			addr := "closed@example.com"
			// An application made while open cannot be confirmed once closed.
			assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
			token := e.lastConfirmToken(t, addr)
			c.setup(t, e)

			st := decodeJSON[joinStatusResponse](t, e.do(t, http.MethodGet, "/join/status", nil))
			if st.Open || st.Price != nil || !slices.Contains(st.Missing, c.missing) {
				t.Errorf("status = %+v, want closed with %q", st, c.missing)
			}
			rec := e.do(t, http.MethodPost, "/join", joinBody("other@example.com"))
			assertStatus(t, rec, http.StatusConflict)
			if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinClosed {
				t.Errorf("join code = %q", code)
			}
			rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
			assertStatus(t, rec, http.StatusConflict)
			if n := e.count(t, `SELECT count(*) FROM users WHERE email = $1`, addr); n != 0 {
				t.Error("confirm created a user while closed")
			}
		})
	}
}

func TestJoinReactivatesFormerMember(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "former.board@example.com"
	userID := e.seedUser(t, addr, true, "member", "board")
	mustExec(t, e.db, ctx,
		`UPDATE users SET membership_cancelled_at = now() - interval '1 year', membership_ends_on = '2025-12-31',
		        membership_cancel_reason = 'synthetic reason'
		  WHERE id = $1`, userID)

	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)

	var (
		deactivated            bool
		firstName              string
		cancelled, endsOn, why *string
	)
	if err := e.db.QueryRow(ctx,
		`SELECT deactivated_at IS NOT NULL, first_name, membership_cancelled_at::text, membership_ends_on::text, membership_cancel_reason
		   FROM users WHERE id = $1`, userID,
	).Scan(&deactivated, &firstName, &cancelled, &endsOn, &why); err != nil {
		t.Fatal(err)
	}
	if deactivated || firstName != "Kari" || cancelled != nil || endsOn != nil || why != nil {
		t.Errorf("reactivated user: deactivated=%v first=%s cancel=%v ends=%v reason=%v", deactivated, firstName, cancelled, endsOn, why)
	}
	if got := e.roles(t, userID); !slices.Equal(got, []string{"applicant"}) {
		t.Errorf("roles = %v, want [applicant]", got)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE club_id = $1 AND lower(email) = $2`, e.clubID, addr); n != 1 {
		t.Errorf("users with the address = %d, want 1", n)
	}
	var reactivated bool
	if err := e.db.QueryRow(ctx,
		`SELECT (details->>'reactivated')::bool FROM audit_log WHERE action = $1 AND actor_id = $2`,
		audit.ActionMembershipJoinConfirmed, userID,
	).Scan(&reactivated); err != nil || !reactivated {
		t.Errorf("join_confirmed reactivated = %v (err %v)", reactivated, err)
	}
}

// Join reads and writes only the deploy's club: another club's member with
// the same address is a stranger here, and another club's token is unknown.
func TestJoinClubScoping(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "two.clubs@example.com"

	other := testutil.SeedClub(t, e.db)
	var otherUser string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name) VALUES ($1, $2, 'O', 'C') RETURNING id`, other, addr,
	).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, ctx, `INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'member')`, otherUser, other)

	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	mails := e.mailsTo(addr)
	if len(mails) != 1 || mails[0].Subject != email.JoinConfirmSubject("nb", e.cfg.ClubName) {
		t.Fatalf("mails = %+v, want a confirmation (the other club's member is not this club's)", mails)
	}

	foreignToken := "foreign-club-token"
	mustExec(t, e.db, ctx,
		`INSERT INTO membership_applications (club_id, first_name, last_name, email, token_hash, consent_versions, expires_at)
		 VALUES ($1, 'F', 'C', 'foreign@example.com', $2, '{}', now() + interval '1 hour')`,
		other, joinTokenHash(foreignToken))
	st := decodeJSON[joinConfirmStatusResponse](t, e.do(t, http.MethodGet, "/join/confirm/"+foreignToken, nil))
	if st.Valid || st.Code != codeJoinTokenInvalid {
		t.Errorf("another club's token = %+v", st)
	}
	assertStatus(t, e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": foreignToken}), http.StatusGone)

	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	if n := e.count(t, `SELECT count(*) FROM users WHERE club_id = $1 AND email = $2`, e.clubID, addr); n != 1 {
		t.Error("no user in this club")
	}
	if got := e.roles(t, otherUser); !slices.Equal(got, []string{"member"}) {
		t.Errorf("the other club's member changed: %v", got)
	}
}

func TestJoinPurgeExpired(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	mustExec(t, e.db, ctx,
		`INSERT INTO membership_applications (club_id, first_name, last_name, email, token_hash, expires_at, consumed_at)
		 VALUES ($1, 'A', 'A', 'old@example.com', 'h1', now() - interval '1 minute', NULL),
		        ($1, 'B', 'B', 'used@example.com', 'h2', now() - interval '1 hour', now() - interval '2 hours'),
		        ($1, 'C', 'C', 'live@example.com', 'h3', now() + interval '1 hour', NULL)`, e.clubID)
	n, err := e.h.PurgeExpired(ctx)
	if err != nil || n != 2 {
		t.Fatalf("purged %d (err %v), want 2", n, err)
	}
	if left := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = 'live@example.com'`); left != 1 {
		t.Error("purged a live application")
	}
}

type fakeMailProvisioner struct{ called chan string }

func (f *fakeMailProvisioner) EnsureUserPrincipal(_ context.Context, userID, _ string) (string, error) {
	f.called <- userID
	return "slug", nil
}

// A login provisions a club mailbox for a member, never for an applicant.
func TestMagicLinkSkipsMailProvisioningForApplicants(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	applicant := e.seedUser(t, "applicant.mail@example.com", false, "applicant")
	member := e.seedUser(t, "member.mail@example.com", false, "member")

	fake := &fakeMailProvisioner{called: make(chan string, 4)}
	ml := NewMagicLinkHandler(e.db, e.cfg, nil, auth.NewSessionService(e.db), zerolog.Nop())
	ml.MailProvisioner = fake
	r := chi.NewRouter()
	r.Get("/auth/verify", ml.HandleVerifyMagicLink)

	login := func(addr string) {
		t.Helper()
		loginURL, err := issueMagicLink(ctx, e.db, e.cfg, e.clubID, addr)
		if err != nil {
			t.Fatal(err)
		}
		token := loginURL[strings.Index(loginURL, "token=")+len("token="):]
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/verify?token="+token, nil))
		assertStatus(t, rec, http.StatusFound)
	}

	login("applicant.mail@example.com")
	select {
	case id := <-fake.called:
		t.Fatalf("provisioned %s, an applicant", id)
	case <-time.After(300 * time.Millisecond):
	}

	login("member.mail@example.com")
	select {
	case id := <-fake.called:
		if id != member {
			t.Errorf("provisioned %s, want the member %s", id, member)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the member was not provisioned")
	}
	_ = applicant
}

// A join order's content is fixed: its line cannot be swapped for a cheaper
// add-on, it offers no catalog, and the join item is no other order's add-on.
func TestJoinOrderIsFixedContent(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "locked@example.com"
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	token := decodeJSON[joinConfirmResponse](t, rec).OrderToken

	var cheap string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, 'merchandise', 'Klistremerke', 10) RETURNING id`,
		e.clubID).Scan(&cheap); err != nil {
		t.Fatal(err)
	}
	var lineID string
	if err := e.db.QueryRow(ctx,
		`SELECT l.id FROM order_lines l JOIN orders o ON o.id = l.order_id WHERE o.token = $1`, token).Scan(&lineID); err != nil {
		t.Fatal(err)
	}

	inv := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
	r := chi.NewRouter()
	r.Get("/checkout/{token}", inv.HandleGetCheckout)
	r.Post("/checkout/{token}/lines", inv.HandleAddCheckoutLine)
	r.Delete("/checkout/{token}/lines/{lineID}", inv.HandleRemoveCheckoutLine)
	serve := func(method, path string, body any) *httptest.ResponseRecorder {
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(method, path, jsonBody(t, body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	for _, rec := range []*httptest.ResponseRecorder{
		serve(http.MethodDelete, "/checkout/"+token+"/lines/"+lineID, nil),
		serve(http.MethodPost, "/checkout/"+token+"/lines", map[string]any{"price_item_id": cheap, "quantity": 1}),
	} {
		assertStatus(t, rec, http.StatusConflict)
		if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeOrderLocked {
			t.Errorf("code = %q, want %q", code, codeOrderLocked)
		}
	}
	co := decodeJSON[checkoutResp](t, serve(http.MethodGet, "/checkout/"+token, nil))
	if len(co.Catalog) != 0 || co.Total != 650 || len(co.Lines) != 1 {
		t.Errorf("join checkout = %+v, want one line and no catalog", co)
	}

	// An ordinary order: the catalog leaves the join item out, and it cannot
	// be added by id.
	otherToken := genCheckoutToken()
	mustExec(t, e.db, ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, 0, $2)`, e.clubID, otherToken)
	co = decodeJSON[checkoutResp](t, serve(http.MethodGet, "/checkout/"+otherToken, nil))
	for _, c := range co.Catalog {
		if c.PriceItemID == e.priceItemID {
			t.Error("the join item is in an ordinary order's catalog")
		}
	}
	if len(co.Catalog) == 0 {
		t.Error("the ordinary order's catalog lost the other items too")
	}
	assertStatus(t, serve(http.MethodPost, "/checkout/"+otherToken+"/lines",
		map[string]any{"price_item_id": e.priceItemID, "quantity": 1}), http.StatusBadRequest)
}

// A user row that appears after the confirm's lookup (here one the lookup
// never sees) makes the insert conflict: 409 already_member, not a 500.
func TestJoinConfirmUniqueRaceIsAlreadyMember(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "race@example.com"
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	token := e.lastConfirmToken(t, addr)
	// A non-person row is invisible to findJoinUser but holds the address
	// under the (club_id, email) unique constraint.
	mustExec(t, e.db, ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, kind) VALUES ($1, $2, 'S', 'S', 'system')`, e.clubID, addr)

	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusConflict)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinAlreadyMember {
		t.Errorf("code = %q", code)
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = $1 AND consumed_at IS NOT NULL`, addr); n != 1 {
		t.Error("the token was not spent")
	}
}

// An address that became an active member between request and confirm.
func TestJoinConfirmAfterBecomingMember(t *testing.T) {
	e := setupJoinEnv(t)
	addr := "became.member@example.com"
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	token := e.lastConfirmToken(t, addr)
	memberID := e.seedUser(t, addr, false, "member")

	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusConflict)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinAlreadyMember {
		t.Errorf("code = %q", code)
	}
	if got := e.roles(t, memberID); !slices.Equal(got, []string{"member"}) {
		t.Errorf("member roles = %v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM orders WHERE user_id = $1`, memberID); n != 0 {
		t.Error("an order was opened for the member")
	}
	assertStatus(t, e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token}), http.StatusGone)
}

// The club-wide daily cap stops join mail across addresses; the day counters
// always carry an expiry, even one found without it.
func TestJoinClubDailyCapAndCounterExpiry(t *testing.T) {
	e := setupJoinEnv(t)
	e.cfg.JoinDailyMailCap = 2

	// A counter left without an expiry gets one on the next increment.
	addrA := "cap.a@example.com"
	dayKey := "join:rcpt:day:" + e.h.emailHMAC(addrA)
	e.mr.Set(dayKey, "0")

	for _, a := range []string{addrA, "cap.b@example.com", "cap.c@example.com"} {
		assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(a)), http.StatusAccepted)
	}
	if n := len(e.mailer.Calls); n != 2 {
		t.Fatalf("mails = %d, want the cap of 2", n)
	}
	if len(e.mailsTo("cap.c@example.com")) != 0 {
		t.Error("mail went out over the cap")
	}
	if got := e.auditOutcomes(t); !slices.Equal(got, []string{joinOutcomeNew, joinOutcomeNew, joinOutcomeCapped}) {
		t.Errorf("outcomes = %v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = 'cap.c@example.com'`); n != 0 {
		t.Error("an application was stored over the cap")
	}
	for _, k := range []string{dayKey, "join:club:day:" + e.clubID} {
		if ttl := e.mr.TTL(k); ttl <= 0 || ttl > joinRecipientDay {
			t.Errorf("%s TTL = %v, want within a day", k, ttl)
		}
	}

	e.mr.FastForward(joinRecipientDay + time.Minute)
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody("cap.c@example.com")), http.StatusAccepted)
	if len(e.mailsTo("cap.c@example.com")) != 1 {
		t.Error("the cap did not reset after a day")
	}
}

// A panic in the background job is logged, not a crash of the API.
func TestJoinBackgroundPanicIsRecovered(t *testing.T) {
	logged := make(chan string, 4)
	h := NewJoinHandler(nil, nil, &config.Config{}, nil, nil, nil, zerolog.New(chanWriter(logged)))
	h.async(func() { panic("synthetic failure") })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-logged:
			if strings.Contains(line, "background job panicked") && strings.Contains(line, "synthetic failure") {
				return
			}
		case <-deadline:
			t.Fatal("the panic was not logged")
		}
	}
}

type chanWriter chan string

func (c chanWriter) Write(p []byte) (int, error) {
	c <- string(p)
	return len(p), nil
}

func TestJoinConsentVersionsMustBeCurrent(t *testing.T) {
	e := setupJoinEnv(t)
	shown := map[string]string{}
	for _, d := range joinRequiredDocs {
		shown[d] = d + "-v1"
	}
	body := joinBody("versions@example.com")
	body["consent_versions"] = shown
	assertStatus(t, e.do(t, http.MethodPost, "/join", body), http.StatusAccepted)

	seedLegalDoc(t, e.db, e.clubID, "statutes", "statutes-v2", "1 minute")
	e.mr.FastForward(joinRecipientGap + time.Minute)
	rec := e.do(t, http.MethodPost, "/join", body)
	assertStatus(t, rec, http.StatusConflict)
	resp := decodeJSON[map[string]any](t, rec)
	if resp["code"] != codeLegalDocumentStale {
		t.Errorf("code = %v", resp["code"])
	}
	if v, _ := resp["versions"].(map[string]any); v["statutes"] != "statutes-v2" {
		t.Errorf("versions = %v, want the current ones", resp["versions"])
	}
	if n := len(e.mailsTo("versions@example.com")); n != 1 {
		t.Errorf("mails = %d, want only the first request's", n)
	}

	// Confirm records what was agreed at submit time, not what is current now.
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, "versions@example.com")})
	assertStatus(t, rec, http.StatusOK)
	if n := e.count(t, `SELECT count(*) FROM user_consents c JOIN users u ON u.id = c.user_id
	                     WHERE u.email = 'versions@example.com' AND c.consent_type = 'statutes' AND c.version = 'statutes-v1'`); n != 1 {
		t.Error("the confirm did not record the version agreed to")
	}
}

func TestJoinNewsletterRevokedOnRejoinWithoutIt(t *testing.T) {
	e := setupJoinEnv(t)
	addr := "newsletter.rejoin@example.com"
	userID := e.seedUser(t, addr, true, "member")
	mustExec(t, e.db, context.Background(),
		`INSERT INTO user_consents (user_id, club_id, consent_type, version) VALUES ($1, $2, 'newsletter', '')`, userID, e.clubID)

	body := joinBody(addr)
	body["newsletter"] = false
	assertStatus(t, e.do(t, http.MethodPost, "/join", body), http.StatusAccepted)
	assertStatus(t, e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)}), http.StatusOK)
	if n := e.count(t, `SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = 'newsletter' AND revoked_at IS NULL`, userID); n != 0 {
		t.Error("the old newsletter consent is still open")
	}
}

// A reused pending join order is re-priced to the current join item.
func TestJoinReusedOrderIsRepriced(t *testing.T) {
	e := setupJoinEnv(t)
	addr := "reprice@example.com"
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	token := decodeJSON[joinConfirmResponse](t, rec).OrderToken

	mustExec(t, e.db, context.Background(), `UPDATE price_items SET amount = 700, name = 'Kontingent ny' WHERE id = $1`, e.priceItemID)
	e.mr.FastForward(joinRecipientGap + time.Minute)
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	if got := decodeJSON[joinConfirmResponse](t, rec).OrderToken; got != token {
		t.Fatalf("a new order was opened")
	}
	var total, lineTotal float64
	var lines int
	var name string
	if err := e.db.QueryRow(context.Background(),
		`SELECT o.total_amount::float8, count(l.id), max(l.total_price)::float8, max(l.name)
		   FROM orders o JOIN order_lines l ON l.order_id = o.id WHERE o.token = $1 GROUP BY o.total_amount`, token,
	).Scan(&total, &lines, &lineTotal, &name); err != nil {
		t.Fatal(err)
	}
	if total != 700 || lines != 1 || lineTotal != 700 || name != "Kontingent ny" {
		t.Errorf("reused order: total %v, %d lines, line %v %q", total, lines, lineTotal, name)
	}
}

func TestJoinRequiresJSON(t *testing.T) {
	e := setupJoinEnv(t)
	for _, path := range []string{"/join", "/join/confirm"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"token":"x"}`))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusUnsupportedMediaType)
	}
}

// The email key is derived from the server secret, so it is stable across
// restarts and is not a plain hash of the address.
func TestJoinEmailHMACKey(t *testing.T) {
	cfg := &config.Config{TOTPEncryptionKey: strings.Repeat("ab", 32)}
	a := NewJoinHandler(nil, nil, cfg, nil, nil, nil, zerolog.Nop())
	b := NewJoinHandler(nil, nil, cfg, nil, nil, nil, zerolog.Nop())
	if a.emailHMAC("Kari@Example.com") != b.emailHMAC("kari@example.com") {
		t.Error("the HMAC is not stable for one key and address")
	}
	other := NewJoinHandler(nil, nil, &config.Config{TOTPEncryptionKey: strings.Repeat("cd", 32)}, nil, nil, nil, zerolog.Nop())
	if a.emailHMAC("kari@example.com") == other.emailHMAC("kari@example.com") {
		t.Error("the HMAC does not depend on the key")
	}
	plain := sha256.Sum256([]byte("kari@example.com"))
	if a.emailHMAC("kari@example.com") == hex.EncodeToString(plain[:]) {
		t.Error("the HMAC is a plain hash")
	}
}

// The board's "can't rejoin" mark: the address, active or deactivated, gets
// the contact-board email and no application, and the answer is the same 202.
func TestJoinRejoinBlocked(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	mustExec(t, e.db, ctx, `UPDATE clubs SET secretary_email = 'styret@example.com' WHERE id = $1`, e.clubID)

	former := "blocked.former@example.com"
	formerID := e.seedUser(t, former, true, "member")
	active := "blocked.active@example.com"
	activeID := e.seedUser(t, active, false, "member")
	mustExec(t, e.db, ctx, `UPDATE users SET rejoin_blocked = true WHERE id = ANY($1)`, []string{formerID, activeID})

	want := e.do(t, http.MethodPost, "/join", joinBody("open.new@example.com")).Body.String()
	for _, addr := range []string{former, active} {
		rec := e.do(t, http.MethodPost, "/join", joinBody(addr))
		assertStatus(t, rec, http.StatusAccepted)
		if rec.Body.String() != want {
			t.Errorf("%s: body %q differs", addr, rec.Body.String())
		}
		mails := e.mailsTo(addr)
		if len(mails) != 1 || mails[0].Subject != email.JoinContactBoardSubject("nb", e.cfg.ClubName) ||
			!strings.Contains(mails[0].HTML, "mailto:styret@example.com") {
			t.Fatalf("%s: mails = %+v, want one contact-board email", addr, mails)
		}
		if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = $1`, addr); n != 0 {
			t.Errorf("%s: an application was stored", addr)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM magic_links WHERE email = $1`, active); n != 0 {
		t.Error("a blocked member got a login link")
	}
	got := e.auditOutcomes(t)
	if !slices.Equal(got, []string{joinOutcomeNew, joinOutcomeBlocked, joinOutcomeBlocked}) {
		t.Errorf("outcomes = %v", got)
	}
	if got := e.roles(t, formerID); !slices.Equal(got, []string{"member"}) {
		t.Errorf("the blocked former member changed: %v", got)
	}
}

// A block set after the request refuses the confirm and changes nothing but
// the token, which is spent.
func TestJoinConfirmAfterRejoinBlocked(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	addr := "blocked.later@example.com"
	userID := e.seedUser(t, addr, true, "member")
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	token := e.lastConfirmToken(t, addr)
	mustExec(t, e.db, ctx, `UPDATE users SET rejoin_blocked = true WHERE id = $1`, userID)

	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token})
	assertStatus(t, rec, http.StatusConflict)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinRejoinBlocked {
		t.Errorf("code = %q", code)
	}
	var deactivated bool
	var first string
	if err := e.db.QueryRow(ctx, `SELECT deactivated_at IS NOT NULL, first_name FROM users WHERE id = $1`, userID).Scan(&deactivated, &first); err != nil {
		t.Fatal(err)
	}
	if !deactivated || first != "Old" {
		t.Errorf("the user changed: deactivated=%v first=%s", deactivated, first)
	}
	if got := e.roles(t, userID); !slices.Equal(got, []string{"member"}) {
		t.Errorf("roles = %v", got)
	}
	for _, q := range []string{
		`SELECT count(*) FROM user_consents WHERE user_id = $1`,
		`SELECT count(*) FROM orders WHERE user_id = $1`,
		`SELECT count(*) FROM sessions WHERE user_id = $1`,
	} {
		if n := e.count(t, q, userID); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE email = $1 AND consumed_at IS NOT NULL`, addr); n != 1 {
		t.Error("the token was not spent")
	}
}

// The board sets and clears the mark through the admin PATCH; each change is
// audited, read back on GET and list, and another club's member is out of reach.
func TestAdminSetsRejoinBlocked(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	adminID := e.seedUser(t, "board.admin@example.com", false, "board")
	memberID := e.seedUser(t, "flag.member@example.com", true, "member")
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Patch("/admin/users/{userID}", h.HandleUpdateUser)
		r.Get("/admin/users/{userID}", h.HandleGetUser)
		r.Get("/admin/users", h.HandleListUsers)
	})
	auth := authHeader(generateTestToken(adminID, e.clubID, []string{"board"}))
	serve := func(method, path string, body any) *httptest.ResponseRecorder {
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(method, path, jsonBody(t, body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	for _, blocked := range []bool{true, false} {
		assertStatus(t, serve(http.MethodPatch, "/admin/users/"+memberID, map[string]any{"rejoin_blocked": blocked}), http.StatusOK)
		var got bool
		if err := e.db.QueryRow(ctx, `SELECT rejoin_blocked FROM users WHERE id = $1`, memberID).Scan(&got); err != nil || got != blocked {
			t.Fatalf("rejoin_blocked = %v (err %v), want %v", got, err, blocked)
		}
		type detailResp struct {
			User struct {
				RejoinBlocked *bool `json:"rejoin_blocked"`
			} `json:"user"`
		}
		detail := decodeJSON[detailResp](t, serve(http.MethodGet, "/admin/users/"+memberID, nil))
		if detail.User.RejoinBlocked == nil || *detail.User.RejoinBlocked != blocked {
			t.Errorf("GET rejoin_blocked = %v, want %v", detail.User.RejoinBlocked, blocked)
		}
	}
	rows, err := e.db.Query(ctx,
		`SELECT (details->>'rejoin_blocked')::bool, actor_id::text FROM audit_log
		  WHERE action = $1 AND resource_id = $2 ORDER BY created_at, id`,
		audit.ActionUserRejoinBlockedSet, memberID)
	if err != nil {
		t.Fatal(err)
	}
	var trail []bool
	for rows.Next() {
		var v bool
		var actor string
		if err := rows.Scan(&v, &actor); err != nil {
			t.Fatal(err)
		}
		if actor != adminID {
			t.Errorf("audit actor = %s", actor)
		}
		trail = append(trail, v)
	}
	rows.Close()
	if !slices.Equal(trail, []bool{true, false}) {
		t.Errorf("audit trail = %v, want [true false]", trail)
	}

	mustExec(t, e.db, ctx, `UPDATE users SET rejoin_blocked = true WHERE id = $1`, memberID)
	list := serve(http.MethodGet, "/admin/users?status=all&limit=200", nil)
	assertStatus(t, list, http.StatusOK)
	if !strings.Contains(list.Body.String(), `"rejoin_blocked":true`) {
		t.Error("the list does not carry rejoin_blocked")
	}

	other := testutil.SeedClub(t, e.db)
	var foreign string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name) VALUES ($1, 'foreign.flag@example.com', 'F', 'C') RETURNING id`,
		other).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, serve(http.MethodPatch, "/admin/users/"+foreign, map[string]any{"rejoin_blocked": true}), http.StatusNotFound)
	var foreignBlocked bool
	_ = e.db.QueryRow(ctx, `SELECT rejoin_blocked FROM users WHERE id = $1`, foreign).Scan(&foreignBlocked)
	if foreignBlocked {
		t.Error("another club's member was blocked")
	}
}
