package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

var mailedTokenRE = regexp.MustCompile(`/login/verify\?token=([A-Za-z0-9_-]+)`)

type magicLinkHashEnv struct {
	db     *pgxpool.Pool
	cfg    *config.Config
	clubID string
	addr   string
	mailer *email.MockSender
	router chi.Router
}

func newMagicLinkHashEnv(t *testing.T) *magicLinkHashEnv {
	t.Helper()
	db := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, db)
	_, addr := testutil.SeedUser(t, db, clubID, []string{"member"})
	cfg := testConfig()
	cfg.FrontendURL = "http://localhost:5173"
	if err := db.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&cfg.ClubSlug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	mailer := &email.MockSender{}
	h := NewMagicLinkHandler(db, cfg, mailer, auth.NewSessionService(db), zerolog.Nop())
	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)
	r.Get("/auth/verify", h.HandleVerifyMagicLink)
	r.Post("/auth/verify", h.HandleConsumeMagicLink)
	return &magicLinkHashEnv{db: db, cfg: cfg, clubID: clubID, addr: addr, mailer: mailer, router: r}
}

// requestLink asks for a login link the way the login form does and returns
// the token the mail carries.
func (e *magicLinkHashEnv) requestLink(t *testing.T) string {
	t.Helper()
	before := len(e.mailer.Calls)
	body, _ := json.Marshal(magicLinkRequest{Email: e.addr})
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(string(body))))
	assertStatus(t, rec, http.StatusOK)
	if len(e.mailer.Calls) != before+1 {
		t.Fatalf("sent %d mails, want 1", len(e.mailer.Calls)-before)
	}
	m := mailedTokenRE.FindStringSubmatch(e.mailer.Calls[before].HTML)
	if m == nil {
		t.Fatal("the mail carries no login link")
	}
	return m[1]
}

// verify logs in with token the way the login page does: a JSON POST.
func (e *magicLinkHashEnv) verify(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(magicLinkVerifyRequest{Token: token})
	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *magicLinkHashEnv) sessions(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.email = $1`, e.addr).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// assertSessionCookie checks that rec set the session cookie with the
// attributes a login must give it, and returns its value.
func assertSessionCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != "brygge_session" {
			continue
		}
		if c.Value == "" {
			t.Error("the session cookie is empty")
		}
		if !c.HttpOnly {
			t.Error("the session cookie is not HttpOnly")
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("session cookie SameSite = %v, want Lax", c.SameSite)
		}
		return c.Value
	}
	t.Fatal("verify set no brygge_session cookie")
	return ""
}

func assertRejectedLink(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	assertStatus(t, rec, http.StatusGone)
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["code"] != code {
		t.Errorf("code = %q, want %q", resp["code"], code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("a refused login set a cookie")
	}
}

// The table holds only the token's sha256: no column of the row equals the
// mailed token, and the mailed token still logs in exactly once.
func TestIntegration_MagicLinkStoresOnlyTheTokenHash(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	ctx := context.Background()
	token := e.requestLink(t)

	var row map[string]any
	if err := e.db.QueryRow(ctx, `SELECT to_jsonb(m) FROM magic_links m WHERE email = $1`, e.addr).Scan(&row); err != nil {
		t.Fatalf("read link row: %v", err)
	}
	for col, v := range row {
		if s, ok := v.(string); ok && strings.Contains(s, token) {
			t.Errorf("column %s holds the raw token", col)
		}
	}
	if got, want := row["token_hash"], magicLinkTokenHash(token); got != want {
		t.Errorf("token_hash = %v, want sha256 hex %s", got, want)
	}

	rec := e.verify(t, token)
	assertStatus(t, rec, http.StatusOK)
	if got := decodeJSON[magicLinkVerifyResponse](t, rec).Status; got != "logged_in" {
		t.Errorf("status = %q, want logged_in", got)
	}
	assertSessionCookie(t, rec)

	assertRejectedLink(t, e.verify(t, token), codeLoginTokenUsed)
	if n := e.sessions(t); n != 1 {
		t.Errorf("%d sessions, want 1", n)
	}
}

// Opening the link (the GET a mail scanner makes) consumes nothing: it
// redirects to the login page, and the token still logs in afterwards.
func TestIntegration_MagicLinkGetDoesNotConsume(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	token := e.requestLink(t)

	for range 3 {
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/verify?token="+token, nil))
		assertStatus(t, rec, http.StatusFound)
		if got, want := rec.Header().Get("Location"), e.cfg.FrontendURL+"/login/verify?token="+token; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Error("the GET set a cookie")
		}
	}
	var used bool
	if err := e.db.QueryRow(context.Background(),
		`SELECT used FROM magic_links WHERE token_hash = $1`, magicLinkTokenHash(token)).Scan(&used); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if used {
		t.Fatal("the GET used the link up")
	}
	if n := e.sessions(t); n != 0 {
		t.Fatalf("the GET created %d sessions", n)
	}

	assertStatus(t, e.verify(t, token), http.StatusOK)
}

// A form-encoded POST (what a cross-site form can send) neither logs in nor
// spends the token.
func TestIntegration_MagicLinkFormPostRefused(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	token := e.requestLink(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader("token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnsupportedMediaType)
	if n := e.sessions(t); n != 0 {
		t.Fatalf("a form POST created %d sessions", n)
	}
	assertStatus(t, e.verify(t, token), http.StatusOK)
}

// The stored hash is not a login token, and an expired link does not log in.
func TestIntegration_MagicLinkRejectsHashAndExpired(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	ctx := context.Background()

	token := e.requestLink(t)
	assertRejectedLink(t, e.verify(t, magicLinkTokenHash(token)), codeLoginTokenInvalid)
	assertRejectedLink(t, e.verify(t, "no-such-token"), codeLoginTokenInvalid)

	if _, err := e.db.Exec(ctx,
		`UPDATE magic_links SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`,
		magicLinkTokenHash(token)); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	assertRejectedLink(t, e.verify(t, token), codeLoginTokenExpired)

	var sessions int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("%d sessions created, want 0", sessions)
	}
}

// The Go hash and the SQL expression 000142 backfilled with agree, so a link
// issued before the migration still verifies after it.
func TestIntegration_TokenHashMatchesSQLBackfill(t *testing.T) {
	t.Parallel()
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		token, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		var got string
		if err := db.QueryRow(ctx, `SELECT encode(sha256(convert_to($1, 'UTF8')), 'hex')`, token).Scan(&got); err != nil {
			t.Fatalf("sql hash: %v", err)
		}
		if want := magicLinkTokenHash(token); got != want {
			t.Errorf("token %q: SQL hash %s, Go hash %s", token, got, want)
		}
	}
}

// A user deactivated after the link was sent is not logged in: the answer is
// 410 login_token_invalid, no session is created, and the link is spent.
func TestIntegration_MagicLinkDeactivatedAfterSend(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	ctx := context.Background()
	token := e.requestLink(t)

	if _, err := e.db.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE email = $1`, e.addr); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	assertRejectedLink(t, e.verify(t, token), codeLoginTokenInvalid)
	if n := e.sessions(t); n != 0 {
		t.Fatalf("a deactivated user got %d sessions", n)
	}
	var used bool
	if err := e.db.QueryRow(ctx,
		`SELECT used FROM magic_links WHERE token_hash = $1`, magicLinkTokenHash(token)).Scan(&used); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if !used {
		t.Error("the refused link was not spent")
	}

	// Reactivating does not bring the spent link back.
	if _, err := e.db.Exec(ctx, `UPDATE users SET deactivated_at = NULL WHERE email = $1`, e.addr); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	assertRejectedLink(t, e.verify(t, token), codeLoginTokenUsed)
}

// A login from a browser that already holds a session deletes that session,
// so it does not stay valid behind the new cookie. A cookie naming no session
// does not stop the login.
func TestIntegration_MagicLinkEndsPriorSession(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	ctx := context.Background()

	first := assertSessionCookie(t, e.verify(t, e.requestLink(t)))

	verifyWithCookie := func(token, cookie string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(magicLinkVerifyRequest{Token: token})
		req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "brygge_session", Value: cookie})
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		return rec
	}

	rec := verifyWithCookie(e.requestLink(t), first)
	assertStatus(t, rec, http.StatusOK)
	second := assertSessionCookie(t, rec)
	if second == first {
		t.Fatal("the login reused the previous session id")
	}
	var n int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id::text = $1`, first).Scan(&n); err != nil {
		t.Fatalf("read old session: %v", err)
	}
	if n != 0 {
		t.Error("the previous session is still in the store")
	}
	if n := e.sessions(t); n != 1 {
		t.Errorf("%d sessions, want 1", n)
	}

	rec = verifyWithCookie(e.requestLink(t), "not-a-session")
	assertStatus(t, rec, http.StatusOK)
	assertSessionCookie(t, rec)
}
