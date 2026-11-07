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

var mailedTokenRE = regexp.MustCompile(`/api/v1/auth/verify\?token=([A-Za-z0-9_-]+)`)

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

func (e *magicLinkHashEnv) verify(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/verify?token="+token, nil))
	return rec
}

func assertRejectedLink(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assertStatus(t, rec, http.StatusBadRequest)
	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "invalid or expired login link" {
		t.Errorf("error = %q, want %q", resp.Error, "invalid or expired login link")
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
	assertStatus(t, rec, http.StatusFound)
	if loc := rec.Header().Get("Location"); loc != e.cfg.FrontendURL+"/portal" {
		t.Errorf("redirect = %q, want the portal", loc)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "=") {
		t.Error("verify set no session cookie")
	}

	assertRejectedLink(t, e.verify(t, token))
}

// The stored hash is not a login token, and an expired link does not log in.
func TestIntegration_MagicLinkRejectsHashAndExpired(t *testing.T) {
	t.Parallel()
	e := newMagicLinkHashEnv(t)
	ctx := context.Background()

	token := e.requestLink(t)
	assertRejectedLink(t, e.verify(t, magicLinkTokenHash(token)))

	if _, err := e.db.Exec(ctx,
		`UPDATE magic_links SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`,
		magicLinkTokenHash(token)); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	assertRejectedLink(t, e.verify(t, token))

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
