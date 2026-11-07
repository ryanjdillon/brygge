package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// linkUsed reads the used flag of token's magic_links row.
func linkUsed(t *testing.T, e *magicLinkHashEnv, token string) bool {
	t.Helper()
	var used bool
	if err := e.db.QueryRow(context.Background(),
		`SELECT used FROM magic_links WHERE token_hash = $1`, magicLinkTokenHash(token)).Scan(&used); err != nil {
		t.Fatalf("read link: %v", err)
	}
	return used
}

// A valid token's peek names the full address the link was mailed to, and
// changes nothing: the token stays unused, no session or audit row is
// written, and the token still logs in afterwards.
func TestIntegration_MagicLinkPeekValid(t *testing.T) {
	t.Parallel()
	e, userID := newLoginAuditEnv(t)
	token := e.requestLink(t)

	for range 3 {
		rec := e.peek(t, token)
		assertStatus(t, rec, http.StatusOK)
		if got := decodeJSON[magicLinkPeekResponse](t, rec).Account; got != e.addr {
			t.Fatalf("account = %q, want the full address %q", got, e.addr)
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("the peek set a cookie")
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
	}
	if linkUsed(t, e, token) {
		t.Fatal("the peek used the link up")
	}
	if n := e.sessions(t); n != 0 {
		t.Fatalf("the peek created %d sessions", n)
	}
	if rows := loginAuditRows(t, e); len(rows) != 0 {
		t.Fatalf("a successful peek wrote %d audit rows: %+v", len(rows), rows)
	}

	assertStatus(t, e.verify(t, token), http.StatusOK)
	rows := loginAuditRows(t, e)
	assertOneLoginRow(t, rows, audit.ActionLoginSuccess, userID, map[string]string{"method": loginMethodMagicLink})
}

// A token verify would refuse, peek refuses with the same 410 code and no
// account, audits auth.login_failed with stage peek, and leaves the row as it
// was: unlike verify, it does not spend a deactivated user's link.
func TestIntegration_MagicLinkPeekRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name       string
		prepare    func(t *testing.T, e *magicLinkHashEnv) string
		code       string
		reason     string
		attributed bool
		wantUsed   bool // the row's used flag afterwards; ignored without a row
		hasRow     bool
	}{
		{
			name: "unknown",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				return "no-such-token-" + testutil.RandomHex(4)
			},
			code: codeLoginTokenInvalid, reason: loginFailInvalid,
		},
		{
			name: "used",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				token := e.requestLink(t)
				mustExec(t, e.db, ctx, `UPDATE magic_links SET used = true WHERE token_hash = $1`, magicLinkTokenHash(token))
				return token
			},
			code: codeLoginTokenUsed, reason: loginFailUsed, attributed: true, wantUsed: true, hasRow: true,
		},
		{
			name: "expired",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				token := e.requestLink(t)
				mustExec(t, e.db, ctx, `UPDATE magic_links SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, magicLinkTokenHash(token))
				return token
			},
			code: codeLoginTokenExpired, reason: loginFailExpired, attributed: true, hasRow: true,
		},
		{
			name: "deactivated user",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				token := e.requestLink(t)
				mustExec(t, e.db, ctx, `UPDATE users SET deactivated_at = now() WHERE email = $1`, e.addr)
				return token
			},
			code: codeLoginTokenInvalid, reason: loginFailInvalid, attributed: true, hasRow: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, userID := newLoginAuditEnv(t)
			token := tc.prepare(t, e)

			rec := e.peek(t, token)
			body := rec.Body.String()
			assertRejectedLink(t, rec, tc.code)
			var raw map[string]any
			if err := json.Unmarshal([]byte(body), &raw); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}
			if _, ok := raw["account"]; ok {
				t.Errorf("a refused peek carries an account: %s", body)
			}
			if strings.Contains(strings.ToLower(body), strings.ToLower(e.addr)) {
				t.Errorf("a refused peek names the address: %s", body)
			}

			if tc.hasRow {
				if got := linkUsed(t, e, token); got != tc.wantUsed {
					t.Errorf("used = %v after the peek, want %v", got, tc.wantUsed)
				}
			}

			wantActor := ""
			if tc.attributed {
				wantActor = userID
			}
			assertOneLoginRow(t, loginAuditRows(t, e), audit.ActionLoginFailed, wantActor,
				map[string]string{"method": loginMethodMagicLink, "reason": tc.reason, "stage": loginStagePeek},
				token, magicLinkTokenHash(token), e.addr)
		})
	}
}

// A peek that presents no credential is refused like verify's and audited
// by neither: a wrong content type, a malformed body or an empty token.
func TestIntegration_MagicLinkPeekNoCredential(t *testing.T) {
	t.Parallel()
	e, _ := newLoginAuditEnv(t)
	post := func(contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/verify/peek", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		return rec
	}
	assertStatus(t, post("text/plain", `{"token":"abc"}`), http.StatusUnsupportedMediaType)
	assertStatus(t, post("", `{"token":"abc"}`), http.StatusUnsupportedMediaType)
	assertStatus(t, post("application/json", `not json`), http.StatusBadRequest)
	assertRejectedLink(t, post("application/json", `{"token":"  "}`), codeLoginTokenInvalid)
	if rows := loginAuditRows(t, e); len(rows) != 0 {
		t.Fatalf("requests with no credential wrote %d audit rows: %+v", len(rows), rows)
	}
}

// withDedupe gives e's handler a fresh miniredis for the login-failure dedupe.
func withDedupe(t *testing.T, e *magicLinkHashEnv) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	e.h.SetLoginFailDedupe(rdb)
	return mr
}

func countLoginFailed(rows []loginAuditRow) int {
	n := 0
	for _, r := range rows {
		if r.action == audit.ActionLoginFailed {
			n++
		}
	}
	return n
}

// A refused peek is audited once an hour per user, reason and stage: a member
// who reopens an old email does not write a row per open, but repeated
// attempts still show up hourly.
func TestIntegration_MagicLinkPeekAuditDedupeAttributed(t *testing.T) {
	t.Parallel()
	e, userID := newLoginAuditEnv(t)
	mr := withDedupe(t, e)
	token := e.requestLink(t)
	mustExec(t, e.db, context.Background(), `UPDATE magic_links SET used = true WHERE token_hash = $1`, magicLinkTokenHash(token))

	assertRejectedLink(t, e.peek(t, token), codeLoginTokenUsed)
	assertRejectedLink(t, e.peek(t, token), codeLoginTokenUsed)
	rows := loginAuditRows(t, e)
	assertOneLoginRow(t, rows, audit.ActionLoginFailed, userID,
		map[string]string{"method": loginMethodMagicLink, "reason": loginFailUsed, "stage": loginStagePeek})

	mr.FastForward(loginFailStageDedupeWindow)
	assertRejectedLink(t, e.peek(t, token), codeLoginTokenUsed)
	if n := countLoginFailed(loginAuditRows(t, e)); n != 2 {
		t.Fatalf("%d login_failed rows after the window, want 2", n)
	}
}

// Anonymous peek refusals are deduped per IP bucket for the hour too.
func TestIntegration_MagicLinkPeekAuditDedupeAnonymous(t *testing.T) {
	t.Parallel()
	e, _ := newLoginAuditEnv(t)
	withDedupe(t, e)
	for range 3 {
		assertRejectedLink(t, e.peek(t, "no-such-token-"+testutil.RandomHex(4)), codeLoginTokenInvalid)
	}
	if n := countLoginFailed(loginAuditRows(t, e)); n != 1 {
		t.Fatalf("%d anonymous peek rows from one IP, want 1", n)
	}
}

// The login itself keeps its dedupe: a refusal naming a user is always
// written, and a peek row does not hide it.
func TestIntegration_MagicLinkVerifyAuditNotDedupedAttributed(t *testing.T) {
	t.Parallel()
	e, _ := newLoginAuditEnv(t)
	withDedupe(t, e)
	token := e.requestLink(t)
	mustExec(t, e.db, context.Background(), `UPDATE magic_links SET used = true WHERE token_hash = $1`, magicLinkTokenHash(token))

	assertRejectedLink(t, e.peek(t, token), codeLoginTokenUsed)
	assertRejectedLink(t, e.verify(t, token), codeLoginTokenUsed)
	assertRejectedLink(t, e.verify(t, token), codeLoginTokenUsed)
	if n := countLoginFailed(loginAuditRows(t, e)); n != 3 {
		t.Fatalf("%d login_failed rows, want 3 (one peek, two verifies)", n)
	}
}

func TestStagedLoginFailDedupeKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/auth/verify/peek", nil)
	r.RemoteAddr = "2001:db8:1:2:3:4:5:6"
	if got, want := stagedLoginFailDedupeKey(r, "u-1", loginMethodMagicLink, loginFailUsed, loginStagePeek),
		"loginfail:peek:user:u-1:magic_link:used"; got != want {
		t.Errorf("attributed key = %q, want %q", got, want)
	}
	anon := stagedLoginFailDedupeKey(r, "", loginMethodMagicLink, loginFailInvalid, loginStagePeek)
	if !strings.HasPrefix(anon, "loginfail:peek:ip:") || !strings.HasSuffix(anon, ":magic_link:invalid") {
		t.Errorf("anonymous key = %q", anon)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/auth/verify/peek", nil)
	r2.RemoteAddr = "2001:db8:1:2:ffff::1"
	if other := stagedLoginFailDedupeKey(r2, "", loginMethodMagicLink, loginFailInvalid, loginStagePeek); other != anon {
		t.Errorf("same /64 gave keys %q and %q", anon, other)
	}
}
