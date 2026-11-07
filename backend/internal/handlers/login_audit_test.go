package handlers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/passkey"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// loginAuditRow is an auth.* audit row as the tests read it back.
type loginAuditRow struct {
	action  string
	actorID string // "" for NULL
	clubID  string // "" for NULL
	details string // the stored JSON, as text
}

// loginAuditRows returns every auth.* row scoped to clubID, oldest first.
func loginAuditRows(t *testing.T, e *magicLinkHashEnv) []loginAuditRow {
	t.Helper()
	rows, err := e.db.Query(context.Background(),
		`SELECT action, COALESCE(actor_id::text, ''), COALESCE(club_id::text, ''), COALESCE(details::text, '')
		   FROM audit_log WHERE club_id = $1 AND action LIKE 'auth.%' ORDER BY created_at, id`, e.clubID)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var out []loginAuditRow
	for rows.Next() {
		var r loginAuditRow
		if err := rows.Scan(&r.action, &r.actorID, &r.clubID, &r.details); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

// assertOneLoginRow checks that exactly one auth.* row exists, with the given
// action, actor and details, and that its details hold none of secrets.
func assertOneLoginRow(t *testing.T, rows []loginAuditRow, action, actorID string, want map[string]string, secrets ...string) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("%d auth audit rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.action != action {
		t.Errorf("action = %q, want %q", r.action, action)
	}
	if r.actorID != actorID {
		t.Errorf("actor_id = %q, want %q", r.actorID, actorID)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(r.details), &got); err != nil {
		t.Fatalf("details %q: %v", r.details, err)
	}
	if len(got) != len(want) {
		t.Errorf("details = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("details[%s] = %q, want %q", k, got[k], v)
		}
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(strings.ToLower(r.details), strings.ToLower(s)) {
			t.Errorf("details %s carry a secret or address %q", r.details, s)
		}
	}
}

func TestLoginFailReason(t *testing.T) {
	for code, want := range map[string]string{
		codeLoginTokenInvalid: loginFailInvalid,
		codeLoginTokenUsed:    loginFailUsed,
		codeLoginTokenExpired: loginFailExpired,
		"anything_else":       loginFailInvalid,
	} {
		if got := loginFailReason(code); got != want {
			t.Errorf("loginFailReason(%q) = %q, want %q", code, got, want)
		}
	}
}

// newLoginAuditEnv is the magic-link env with an example.test address, and
// the member's id.
func newLoginAuditEnv(t *testing.T) (*magicLinkHashEnv, string) {
	t.Helper()
	e := newMagicLinkHashEnv(t)
	addr := "login-audit-" + testutil.RandomHex(4) + "@example.test"
	var userID string
	if err := e.db.QueryRow(context.Background(),
		`UPDATE users SET email = $2 WHERE email = $1 RETURNING id`, e.addr, addr).Scan(&userID); err != nil {
		t.Fatalf("set address: %v", err)
	}
	e.addr = addr
	return e, userID
}

func TestIntegration_LoginAuditMagicLinkSuccess(t *testing.T) {
	t.Parallel()
	e, userID := newLoginAuditEnv(t)
	token := e.requestLink(t)

	rec := e.verify(t, token)
	assertStatus(t, rec, http.StatusOK)
	sessionID := assertSessionCookie(t, rec)

	rows := loginAuditRows(t, e)
	assertOneLoginRow(t, rows, audit.ActionLoginSuccess, userID,
		map[string]string{"method": loginMethodMagicLink},
		token, magicLinkTokenHash(token), sessionID, e.addr)
	if rows[0].clubID != e.clubID {
		t.Errorf("club_id = %q, want %q", rows[0].clubID, e.clubID)
	}
}

func TestIntegration_LoginAuditMagicLinkFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name    string
		prepare func(t *testing.T, e *magicLinkHashEnv) string // returns the token to present
		code    string
		reason  string
		// attributed: the row names the member the link was for.
		attributed bool
	}{
		{
			name: "unknown token",
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
			code: codeLoginTokenUsed, reason: loginFailUsed, attributed: true,
		},
		{
			name: "expired",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				token := e.requestLink(t)
				mustExec(t, e.db, ctx, `UPDATE magic_links SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, magicLinkTokenHash(token))
				return token
			},
			code: codeLoginTokenExpired, reason: loginFailExpired, attributed: true,
		},
		{
			name: "deactivated after send",
			prepare: func(t *testing.T, e *magicLinkHashEnv) string {
				token := e.requestLink(t)
				mustExec(t, e.db, ctx, `UPDATE users SET deactivated_at = now() WHERE email = $1`, e.addr)
				return token
			},
			code: codeLoginTokenInvalid, reason: loginFailInvalid, attributed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, userID := newLoginAuditEnv(t)
			token := tc.prepare(t, e)

			assertRejectedLink(t, e.verify(t, token), tc.code)

			wantActor := ""
			if tc.attributed {
				wantActor = userID
			}
			rows := loginAuditRows(t, e)
			assertOneLoginRow(t, rows, audit.ActionLoginFailed, wantActor,
				map[string]string{"method": loginMethodMagicLink, "reason": tc.reason},
				token, magicLinkTokenHash(token), e.addr)
		})
	}
}

// Requests that present no credential are not audited: a wrong content type,
// a malformed body, an empty token, and a request the rate limiter refuses.
func TestIntegration_LoginAuditMagicLinkNoCredentialNotAudited(t *testing.T) {
	t.Parallel()
	e, _ := newLoginAuditEnv(t)

	post := func(router http.Handler, contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.10"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	assertStatus(t, post(e.router, "text/plain", `{"token":"abc"}`), http.StatusUnsupportedMediaType)
	assertStatus(t, post(e.router, "application/json", `not json`), http.StatusBadRequest)
	assertStatus(t, post(e.router, "application/json", `{"token":"   "}`), http.StatusGone)
	if rows := loginAuditRows(t, e); len(rows) != 0 {
		t.Fatalf("requests with no credential wrote %d audit rows: %+v", len(rows), rows)
	}

	// Behind a one-per-window limiter the first unknown token is audited and
	// the refused second request is not. This checks the ordering only (the
	// limiter answers before the handler runs); the real strictRL wiring on
	// /auth/verify and /auth/passkeys/login/finish is in cmd/api/routes.go.
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rl := middleware.NewRateLimiter(rdb, zerolog.Nop(), 1, time.Minute, func(r *http.Request) string { return "verify:" + r.RemoteAddr })
	limited := chi.NewRouter()
	limited.Use(rl.Handler)
	limited.Mount("/", e.router)

	assertStatus(t, post(limited, "application/json", `{"token":"unknown-one"}`), http.StatusGone)
	assertStatus(t, post(limited, "application/json", `{"token":"unknown-two"}`), http.StatusTooManyRequests)
	if rows := loginAuditRows(t, e); len(rows) != 1 {
		t.Fatalf("%d audit rows after one audited and one rate-limited request, want 1: %+v", len(rows), rows)
	}
}

func TestIntegration_LoginAuditLogout(t *testing.T) {
	t.Parallel()
	e, userID := newLoginAuditEnv(t)
	sessionID := assertSessionCookie(t, e.verify(t, e.requestLink(t)))

	h := NewMagicLinkHandler(e.db, e.cfg, nil, auth.NewSessionService(e.db), audit.NewService(e.db, zerolog.Nop()), zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/auth/session/logout", nil)
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID, ClubID: e.clubID, Roles: []string{"member"}})
	ctx = middleware.WithSessionID(ctx, sessionID)
	rec := httptest.NewRecorder()
	h.HandleSessionLogout(rec, req.WithContext(ctx))
	assertStatus(t, rec, http.StatusOK)

	var logouts []loginAuditRow
	for _, r := range loginAuditRows(t, e) {
		if r.action == audit.ActionLogout {
			logouts = append(logouts, r)
		}
	}
	if len(logouts) != 1 {
		t.Fatalf("%d auth.logout rows, want 1", len(logouts))
	}
	if logouts[0].actorID != userID {
		t.Errorf("logout actor = %q, want %q", logouts[0].actorID, userID)
	}
	if strings.Contains(logouts[0].details, sessionID) {
		t.Error("the logout row carries the session id")
	}
	if n := e.sessions(t); n != 0 {
		t.Errorf("%d sessions after logout, want 0", n)
	}
}

// virtualAuthenticator signs WebAuthn assertions with a P-256 key, enough to
// drive the passkey login ceremony end to end in a test.
type virtualAuthenticator struct {
	key  *ecdsa.PrivateKey
	cred *webauthn.Credential
}

func newVirtualAuthenticator(t *testing.T, credID string) *virtualAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		t.Fatalf("ecdh public key: %v", err)
	}
	raw := pub.Bytes() // 0x04 || X || Y
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{
			KeyType:   int64(webauthncose.EllipticKey),
			Algorithm: int64(webauthncose.AlgES256),
		},
		Curve:  int64(webauthncose.P256),
		XCoord: raw[1:33],
		YCoord: raw[33:],
	})
	if err != nil {
		t.Fatalf("encode COSE key: %v", err)
	}
	cred := passkeyTestCredential(credID)
	cred.PublicKey = cose
	return &virtualAuthenticator{key: key, cred: cred}
}

// assertion is the login/finish body for challenge, signed by the key unless
// badSignature.
func (a *virtualAuthenticator) assertion(t *testing.T, rpID, origin, challenge string, userHandle []byte, badSignature bool) []byte {
	t.Helper()
	clientData, _ := json.Marshal(map[string]string{
		"type":      "webauthn.get",
		"challenge": challenge,
		"origin":    origin,
	})
	rpHash := sha256.Sum256([]byte(rpID))
	authData := make([]byte, 0, 37)
	authData = append(authData, rpHash[:]...)
	authData = append(authData, 0x05) // user present + user verified
	authData = binary.BigEndian.AppendUint32(authData, 1)

	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if badSignature {
		sig[len(sig)-1] ^= 0xff
	}

	b64 := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{
		"id":    b64(a.cred.ID),
		"rawId": b64(a.cred.ID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64(clientData),
			"authenticatorData": b64(authData),
			"signature":         b64(sig),
			"userHandle":        b64(userHandle),
		},
	})
	return body
}

type passkeyLoginEnv struct {
	store  *passkey.Store
	h      *PasskeyHandler
	clubID string
	userID string
	handle []byte
	auth   *virtualAuthenticator
	rows   func(t *testing.T) []loginAuditRow
}

func newPasskeyLoginEnv(t *testing.T) *passkeyLoginEnv {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	mustExec(t, pool, ctx, `UPDATE users SET email = $2 WHERE id = $1`, userID, "passkey-"+testutil.RandomHex(4)+"@example.test")
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}

	store := passkey.NewStore(pool)
	handle, err := store.EnsureHandle(ctx, userID)
	if err != nil {
		t.Fatalf("EnsureHandle: %v", err)
	}
	va := newVirtualAuthenticator(t, "cred-"+testutil.RandomHex(4))
	if err := store.SaveCredential(ctx, userID, clubID, va.cred, ""); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := &config.Config{FrontendURL: "https://club.example.test", ClubSlug: slug}
	wa, err := passkey.New(cfg)
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	h := NewPasskeyHandler(wa, store, passkey.NewChallengeStore(rdb), auth.NewSessionService(pool), audit.NewService(pool, zerolog.Nop()), cfg, zerolog.Nop())

	e := &passkeyLoginEnv{store: store, h: h, clubID: clubID, userID: userID, handle: handle, auth: va}
	e.rows = func(t *testing.T) []loginAuditRow {
		return loginAuditRows(t, &magicLinkHashEnv{db: pool, clubID: clubID})
	}
	return e
}

// login runs begin then finish with an assertion over the begin's challenge.
func (e *passkeyLoginEnv) login(t *testing.T, a *virtualAuthenticator, handle []byte, badSignature bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e.h.HandleLoginBegin(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/begin", nil))
	assertStatus(t, rec, http.StatusOK)
	var begin struct {
		Options struct {
			Challenge string `json:"challenge"`
		} `json:"options"`
		ChallengeID string `json:"challenge_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &begin); err != nil {
		t.Fatalf("decode begin: %v", err)
	}
	body := a.assertion(t, "club.example.test", "https://club.example.test", begin.Options.Challenge, handle, badSignature)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/finish?cid="+begin.ChallengeID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	fin := httptest.NewRecorder()
	e.h.HandleLoginFinish(fin, req)
	return fin
}

func TestIntegration_LoginAuditPasskey(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		rec := e.login(t, e.auth, e.handle, false)
		assertStatus(t, rec, http.StatusOK)
		sessionID := assertSessionCookie(t, rec)
		assertOneLoginRow(t, e.rows(t), audit.ActionLoginSuccess, e.userID,
			map[string]string{"method": loginMethodPasskey},
			sessionID, base64.RawURLEncoding.EncodeToString(e.auth.cred.ID), string(e.auth.cred.ID))
	})

	t.Run("bad signature names the member", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		assertStatus(t, e.login(t, e.auth, e.handle, true), http.StatusUnauthorized)
		assertOneLoginRow(t, e.rows(t), audit.ActionLoginFailed, e.userID,
			map[string]string{"method": loginMethodPasskey, "reason": loginFailPasskeyVerification},
			base64.RawURLEncoding.EncodeToString(e.auth.cred.ID), string(e.auth.cred.ID))
	})

	t.Run("credential not the member's has no actor", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		stranger := newVirtualAuthenticator(t, "stranger-"+testutil.RandomHex(4))
		assertStatus(t, e.login(t, stranger, e.handle, false), http.StatusUnauthorized)
		assertOneLoginRow(t, e.rows(t), audit.ActionLoginFailed, "",
			map[string]string{"method": loginMethodPasskey, "reason": loginFailPasskeyUnknownCredential})
	})

	t.Run("unknown user handle is scoped to the club", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		stranger := newVirtualAuthenticator(t, "stranger-"+testutil.RandomHex(4))
		assertStatus(t, e.login(t, stranger, []byte("no-such-handle"), false), http.StatusUnauthorized)
		assertOneLoginRow(t, e.rows(t), audit.ActionLoginFailed, "",
			map[string]string{"method": loginMethodPasskey, "reason": loginFailPasskeyUnknownCredential})
	})

	// Every refusal answers the same, so the response does not tell a prober
	// whether a handle or credential exists.
	t.Run("failures answer identically", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		stranger := newVirtualAuthenticator(t, "stranger-"+testutil.RandomHex(4))

		rec := httptest.NewRecorder()
		e.h.HandleLoginBegin(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/begin", nil))
		cid := decodeJSON[passkeyLoginBeginResponse](t, rec).ChallengeID
		unparseable := httptest.NewRecorder()
		e.h.HandleLoginFinish(unparseable, httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/finish?cid="+cid, strings.NewReader(`not json`)))

		answers := map[string]*httptest.ResponseRecorder{
			"unparseable body":   unparseable,
			"unknown handle":     e.login(t, stranger, []byte("no-such-handle"), false),
			"foreign credential": e.login(t, stranger, e.handle, false),
			"bad signature":      e.login(t, e.auth, e.handle, true),
		}
		want := answers["bad signature"]
		if want.Code != http.StatusUnauthorized {
			t.Fatalf("bad signature status = %d, want 401", want.Code)
		}
		for name, got := range answers {
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Errorf("%s answered %d %q, want %d %q", name, got.Code, got.Body.String(), want.Code, want.Body.String())
			}
			if got.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s set a cookie", name)
			}
		}
	})

	t.Run("unparseable body is not audited", func(t *testing.T) {
		t.Parallel()
		e := newPasskeyLoginEnv(t)
		rec := httptest.NewRecorder()
		e.h.HandleLoginBegin(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/begin", nil))
		cid := decodeJSON[passkeyLoginBeginResponse](t, rec).ChallengeID
		fin := httptest.NewRecorder()
		e.h.HandleLoginFinish(fin, httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkeys/login/finish?cid="+cid, strings.NewReader(`not json`)))
		assertStatus(t, fin, http.StatusUnauthorized)
		if rows := e.rows(t); len(rows) != 0 {
			t.Fatalf("an unparseable body wrote %d audit rows", len(rows))
		}
	})
}

// Anonymous failure rows are written once per client IP, method and reason a
// minute; rows that name a user are never deduped; a Redis outage writes the
// row rather than losing it.
func TestIntegration_LoginAuditAnonymousDedupe(t *testing.T) {
	t.Parallel()
	e, userID := newLoginAuditEnv(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	h := NewMagicLinkHandler(e.db, e.cfg, e.mailer, auth.NewSessionService(e.db), audit.NewService(e.db, zerolog.Nop()), zerolog.Nop())
	h.SetLoginFailDedupe(rdb)
	router := chi.NewRouter()
	router.Post("/auth/verify", h.HandleConsumeMagicLink)
	verifyFrom := func(ip, token string) {
		t.Helper()
		body, _ := json.Marshal(magicLinkVerifyRequest{Token: token})
		req := httptest.NewRequest(http.MethodPost, "/auth/verify", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusGone)
	}
	count := func(actor string) int {
		t.Helper()
		n := 0
		for _, r := range loginAuditRows(t, e) {
			if r.action == audit.ActionLoginFailed && r.actorID == actor {
				n++
			}
		}
		return n
	}

	// Three unknown tokens from one IP: one row. Another IP gets its own.
	for i := range 3 {
		verifyFrom("192.0.2.20", "unknown-"+string(rune('a'+i)))
	}
	if n := count(""); n != 1 {
		t.Fatalf("%d anonymous rows from one IP, want 1", n)
	}
	verifyFrom("192.0.2.21", "unknown-other-ip")
	if n := count(""); n != 2 {
		t.Fatalf("%d anonymous rows from two IPs, want 2", n)
	}

	// A used link names its user: every attempt is written.
	token := e.requestLink(t)
	mustExec(t, e.db, context.Background(), `UPDATE magic_links SET used = true WHERE token_hash = $1`, magicLinkTokenHash(token))
	verifyFrom("192.0.2.20", token)
	verifyFrom("192.0.2.20", token)
	if n := count(userID); n != 2 {
		t.Errorf("%d attributed rows, want 2 (never deduped)", n)
	}

	// The window expires.
	mr.FastForward(loginFailDedupeWindow + time.Second)
	verifyFrom("192.0.2.20", "unknown-after-window")
	if n := count(""); n != 3 {
		t.Errorf("%d anonymous rows after the window, want 3", n)
	}

	// Redis down: the row is written.
	mr.Close()
	verifyFrom("192.0.2.20", "unknown-redis-down-1")
	verifyFrom("192.0.2.20", "unknown-redis-down-2")
	if n := count(""); n != 5 {
		t.Errorf("%d anonymous rows with Redis down, want 5", n)
	}
}

// The passkey path shares the dedupe: an unknown handle twice from one IP is
// one row.
func TestIntegration_LoginAuditPasskeyAnonymousDedupe(t *testing.T) {
	t.Parallel()
	e := newPasskeyLoginEnv(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	e.h.SetLoginFailDedupe(rdb)

	stranger := newVirtualAuthenticator(t, "stranger-"+testutil.RandomHex(4))
	for range 2 {
		assertStatus(t, e.login(t, stranger, []byte("no-such-handle"), false), http.StatusUnauthorized)
	}
	if rows := e.rows(t); len(rows) != 1 {
		t.Errorf("%d rows for two unknown-handle attempts from one IP, want 1", len(rows))
	}
}
