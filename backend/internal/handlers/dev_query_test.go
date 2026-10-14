package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestValidateDevQuery(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"select", "SELECT 1", false},
		{"with", "WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"explain", "EXPLAIN SELECT 1", false},
		{"lowercase select", "select id from users", false},
		{"trailing semicolon trimmed", "SELECT 1;", false},
		{"empty", "", true},
		{"update", "UPDATE users SET email = ''", true},
		{"delete", "DELETE FROM users", true},
		{"stacked", "SELECT 1; DROP TABLE users", true},
		{"line comment", "SELECT 1 -- drop", true},
		{"block comment", "SELECT 1 /* x */", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateDevQuery(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateDevQuery(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			}
		})
	}
}

// When DEV_QUERY_DATABASE_URL is unset the handler has no pool and must
// report 503 rather than borrowing a privileged connection.
func TestDevQueryNilPoolReturns503(t *testing.T) {
	h := NewDevQueryHandler(nil, nil, zerolog.Nop())

	body, _ := json.Marshal(devQueryRequest{SQL: "SELECT 1"})
	req := httptest.NewRequest(http.MethodPost, "/admin/dev/query", bytes.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: "u1", ClubID: "c1", Roles: []string{"admin"}}))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// The core BRY-285 guarantee: because the dev pool authenticates AS
// brygge_dev_ro (not via SET ROLE on the brygge connection), the classic
// in-statement escalation — reset the role, then run arbitrary SQL via
// query_to_xml's SPI call — cannot reach a table the role has no rights
// to. There is no higher role to reset to.
func TestDevQueryRoleCannotEscalate(t *testing.T) {
	testutil.SkipIfNoDB(t)
	ctx := context.Background()
	adminURL := os.Getenv("DATABASE_URL")

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	// A private schema the read-only role has no USAGE on, holding a
	// synthetic secret. Qualified names only — no reliance on search_path.
	schema := "devqtest_" + testutil.RandomHex(6)
	secret := "sentinel-" + testutil.RandomHex(8)
	mustExec(t, adminPool, ctx, fmt.Sprintf("CREATE SCHEMA %s", schema))
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})
	mustExec(t, adminPool, ctx, fmt.Sprintf("CREATE TABLE %s.t (secret text)", schema))
	mustExec(t, adminPool, ctx, fmt.Sprintf("INSERT INTO %s.t (secret) VALUES ($1)", schema), secret)

	devPool, err := pgxpool.New(ctx, devRoleURL(t, adminURL))
	if err != nil {
		t.Skipf("brygge_dev_ro pool unavailable (run `just dev-role-bootstrap`): %v", err)
	}
	defer devPool.Close()
	if err := devPool.Ping(ctx); err != nil {
		t.Skipf("brygge_dev_ro not reachable (run `just dev-role-bootstrap`): %v", err)
	}

	h := NewDevQueryHandler(devPool, nil, zerolog.Nop())

	escalation := fmt.Sprintf(
		"SELECT set_config('role','none',true) AS r, "+
			"query_to_xml('select secret from %s.t', true, false, '') AS leak",
		schema)
	body, _ := json.Marshal(devQueryRequest{SQL: escalation})
	req := httptest.NewRequest(http.MethodPost, "/admin/dev/query", bytes.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: "u1", ClubID: "c1", Roles: []string{"admin"}}))
	rec := httptest.NewRecorder()

	h.HandleQuery(rec, req)

	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("escalation leaked the secret: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("escalation unexpectedly returned 200: %s", rec.Body.String())
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// devRoleURL rewrites DATABASE_URL's userinfo to the brygge_dev_ro role
// with its known local dev password (set by `just dev-role-bootstrap`).
func devRoleURL(t *testing.T, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	u.User = url.UserPassword("brygge_dev_ro", "brygge_dev_ro")
	return u.String()
}
