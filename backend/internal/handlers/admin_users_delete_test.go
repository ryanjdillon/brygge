package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	brmail "github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/passkey"
)

const deleteUserPath = "/admin/users/{userID}"

func newAdminDeleteEnv(t *testing.T) *gdprProcessEnv {
	t.Helper()
	e := newGDPRProcessEnv(t)
	h := NewAdminUsersHandler(e.db, &config.Config{Port: 8080}, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Delete(deleteUserPath, h.HandleDeleteUser)
	e.router = r
	return e
}

func (e *gdprProcessEnv) deleteUser(userID, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/"+userID+query, nil)
	req.Header.Set("Authorization", authHeader(generateTestToken(e.admin, e.clubID, []string{"admin"})))
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *gdprProcessEnv) deleteAuditSessionsRevoked(t *testing.T, userID string) int {
	t.Helper()
	var revoked int
	if err := e.db.QueryRow(context.Background(),
		`SELECT (details->>'sessions_revoked')::int FROM audit_log
		  WHERE club_id = $1 AND resource_id = $2 AND action = 'delete_user'`,
		e.clubID, userID).Scan(&revoked); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	return revoked
}

// A soft-deleted member must have no way left to authenticate: the session no
// longer validates, the passkey login lookup refuses them, and every other
// credential is gone, exactly as when a GDPR deletion is processed.
func TestIntegration_AdminDeleteUserSoftRevokesAccess(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)
	ctx := context.Background()
	userID, email, sessionID, _ := e.seedMember(t, false)

	store := passkey.NewStore(e.db)
	handle, err := store.EnsureHandle(ctx, userID)
	if err != nil {
		t.Fatalf("EnsureHandle: %v", err)
	}
	if _, err := store.UserByHandle(ctx, handle); err != nil {
		t.Fatalf("passkey lookup before delete should succeed: %v", err)
	}

	assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)

	if _, _, err := auth.NewSessionService(e.db).ValidateSession(ctx, sessionID); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("ValidateSession after delete = %v, want ErrSessionNotFound", err)
	}
	if _, err := store.UserByHandle(ctx, handle); !errors.Is(err, passkey.ErrNoUser) {
		t.Errorf("passkey login lookup after delete = %v, want ErrNoUser", err)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NOT NULL AND deactivated_by = $2`,
		userID, e.admin); n != 1 {
		t.Errorf("user not deactivated by the deleting admin")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM webauthn_credentials WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("passkeys left = %d, want 0", n)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("recovery codes left = %d, want 0", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND totp_enabled = false AND totp_secret_encrypted IS NULL AND payables_token IS NULL`,
		userID); n != 1 {
		t.Errorf("TOTP enrolment or pay link still present")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM magic_links WHERE club_id = $1 AND email = $2`, e.clubID, email); n != 0 {
		t.Errorf("magic links left = %d, want 0", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM developer_tokens WHERE created_by = $1 AND revoked_at IS NULL`, userID); n != 0 {
		t.Errorf("developer tokens left active = %d, want 0", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND email LIKE '%' || $2 AND phone = ''`,
		userID, accounting.ErasedEmailSuffix); n != 1 {
		t.Errorf("identity not anonymised with a per-member address")
	}
	if revoked := e.deleteAuditSessionsRevoked(t, userID); revoked != 1 {
		t.Errorf("audit sessions_revoked = %d, want 1", revoked)
	}
}

// users carries UNIQUE (club_id, email): a second anonymised member in the
// same club must not collide with the first.
func TestIntegration_AdminDeleteUserTwoMembersInOneClub(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)

	var ids []string
	for range 3 {
		userID, _, _, _ := e.seedMember(t, false)
		assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)
		ids = append(ids, userID)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(DISTINCT email) FROM users WHERE id = ANY($1) AND email LIKE '%' || $2`,
		ids, accounting.ErasedEmailSuffix); n != len(ids) {
		t.Errorf("distinct anonymised addresses = %d, want %d", n, len(ids))
	}

	// A member that is already anonymised can be deleted again without error.
	assertStatus(t, e.deleteUser(ids[0], ""), http.StatusOK)
}

// fakeStalwart answers the slice of Stalwart's admin REST API that the user
// provisioner uses, and records which principals were deleted.
type fakeStalwart struct {
	mu         sync.Mutex
	principals map[string]bool
	deleted    []string
	failDelete bool
}

func (f *fakeStalwart) server(t *testing.T) *httptest.Server {
	t.Helper()
	f.principals = map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := strings.TrimPrefix(r.URL.Path, "/api/principal/")
		switch r.Method {
		case http.MethodGet:
			if f.principals[name] {
				_, _ = w.Write([]byte(`{"data":{"id":7,"name":"` + name + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.principals[body.Name] = true
			_, _ = w.Write([]byte(`{"data":7}`))
		case http.MethodPatch:
			_, _ = w.Write([]byte(`{"data":null}`))
		case http.MethodDelete:
			if f.failDelete {
				http.Error(w, "stalwart is down", http.StatusInternalServerError)
				return
			}
			delete(f.principals, name)
			f.deleted = append(f.deleted, name)
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeStalwart) deletedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

// newAdminDeleteEnvWithMail wires the handler to a provisioner that talks to
// the fake Stalwart, and provisions a mailbox for the returned member.
func newAdminDeleteEnvWithMail(t *testing.T, fake *fakeStalwart) (e *gdprProcessEnv, userID, sessionID, slug string) {
	t.Helper()
	e = newGDPRProcessEnv(t)
	srv := fake.server(t)
	prov := brmail.NewUserProvisioner(e.db,
		brmail.NewAdminClient(srv.URL, "admin", "pw", "", zerolog.Nop()),
		nil, make([]byte, 32), "example.invalid", zerolog.Nop())
	h := NewAdminUsersHandler(e.db, &config.Config{Port: 8080}, zerolog.Nop())
	h.MailProvisioner = prov
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Delete(deleteUserPath, h.HandleDeleteUser)
	e.router = r

	var email string
	userID, email, sessionID, _ = e.seedMember(t, false)
	var err error
	if slug, err = prov.EnsureUserPrincipal(context.Background(), userID, email); err != nil {
		t.Fatalf("provision mailbox: %v", err)
	}
	if !fake.principals[slug] {
		t.Fatalf("fake Stalwart has no principal %q after provisioning", slug)
	}
	return e, userID, sessionID, slug
}

// A member with rows in every table that references users without a cascade
// (audit actor, deletion request, order) and a payment that
// would cascade away is anonymised in place: 200, every row intact. The old
// hard delete answered 500 on the first and silently dropped the payment.
func TestIntegration_AdminDeleteUserKeepsReferencingRows(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"", "?erasure=true"} {
		t.Run("query "+query, func(t *testing.T) {
			t.Parallel()
			e := newAdminDeleteEnv(t)
			ctx := context.Background()
			userID, _, _, _ := e.seedMember(t, false)

			seed := []struct{ name, sql string }{
				{"audit actor", `INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id)
				                 VALUES ($1, $2, 'test.action', 'thing', 'x')`},
				{"order", `INSERT INTO orders (club_id, user_id, total_amount, token)
				           VALUES ($1, $2, 450, 'tok-' || gen_random_uuid()::text)`},
				{"payment", `INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
				             VALUES ($1, $2, 'dues', 450, 'completed', now())`},
			}
			for _, s := range seed {
				if _, err := e.db.Exec(ctx, s.sql, e.clubID, userID); err != nil {
					t.Fatalf("seed %s: %v", s.name, err)
				}
			}

			assertStatus(t, e.deleteUser(userID, query), http.StatusOK)

			for _, c := range []struct{ name, sql string }{
				{"user row", `SELECT count(*) FROM users WHERE id = $1 AND email LIKE '%@deleted.local' AND deactivated_at IS NOT NULL`},
				{"audit actor", `SELECT count(*) FROM audit_log WHERE actor_id = $1 AND action = 'test.action'`},
				{"deletion request", `SELECT count(*) FROM deletion_requests WHERE user_id = $1`},
				{"order", `SELECT count(*) FROM orders WHERE user_id = $1`},
				{"payment", `SELECT count(*) FROM payments WHERE user_id = $1`},
			} {
				if n := gdprCount(t, e.db, c.sql, userID); n != 1 {
					t.Errorf("%s rows = %d, want 1", c.name, n)
				}
			}
		})
	}
}

// Financial records no longer decide how a delete behaves: a member with an
// in-window invoice is anonymised too (retention timing is BRY-538).
func TestIntegration_AdminDeleteUserWithInvoiceIsAnonymised(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)
	userID, _, _, _ := e.seedMember(t, true)

	assertStatus(t, e.deleteUser(userID, "?erasure=true"), http.StatusOK)

	if n := gdprCount(t, e.db, `SELECT count(*) FROM invoices WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("invoice rows = %d, want 1", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND email LIKE '%' || $2`,
		userID, accounting.ErasedEmailSuffix); n != 1 {
		t.Errorf("member not anonymised")
	}
}

// The member's mailbox is deprovisioned under the principal their credentials
// row names, and the credentials row goes with it.
func TestIntegration_AdminDeleteUserDeprovisionsMailbox(t *testing.T) {
	t.Parallel()
	fake := &fakeStalwart{}
	e, userID, _, slug := newAdminDeleteEnvWithMail(t, fake)

	assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)

	if got := fake.deletedNames(); len(got) != 1 || got[0] != slug {
		t.Errorf("deleted principals = %v, want [%s]", got, slug)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_mail_credentials WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("mail credentials left = %d, want 0", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND email LIKE '%' || $2`,
		userID, accounting.ErasedEmailSuffix); n != 1 {
		t.Errorf("member not anonymised")
	}
}

// A mailbox Stalwart cannot remove fails the call closed: 502 with a typed
// code, and the member is exactly as before (still active, session valid,
// credentials row kept so the retry knows which principal to remove). The
// retry once Stalwart is back succeeds.
func TestIntegration_AdminDeleteUserMailboxFailureRollsBack(t *testing.T) {
	t.Parallel()
	fake := &fakeStalwart{failDelete: true}
	e, userID, sessionID, slug := newAdminDeleteEnvWithMail(t, fake)
	ctx := context.Background()

	rec := e.deleteUser(userID, "")
	assertStatus(t, rec, http.StatusBadGateway)
	if !strings.Contains(rec.Body.String(), codeMailboxDeprovisionFailed) {
		t.Errorf("body should carry %s: %s", codeMailboxDeprovisionFailed, rec.Body.String())
	}
	if _, _, err := auth.NewSessionService(e.db).ValidateSession(ctx, sessionID); err != nil {
		t.Errorf("session should survive a rolled-back delete: %v", err)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL AND email NOT LIKE '%' || $2`,
		userID, accounting.ErasedEmailSuffix); n != 1 {
		t.Errorf("a rolled-back delete must leave the member untouched")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_mail_credentials WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("credentials row must survive so the retry can find the principal")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'delete_user'`, userID); n != 0 {
		t.Errorf("a rolled-back delete must not leave an audit row")
	}

	fake.mu.Lock()
	fake.failDelete = false
	fake.mu.Unlock()
	assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)
	if got := fake.deletedNames(); len(got) != 1 || got[0] != slug {
		t.Errorf("deleted principals after retry = %v, want [%s]", got, slug)
	}
}

// A principal that is already gone from Stalwart (an earlier attempt removed
// it and then failed) does not block the retry.
func TestIntegration_AdminDeleteUserMailboxAlreadyGone(t *testing.T) {
	t.Parallel()
	fake := &fakeStalwart{}
	e, userID, _, slug := newAdminDeleteEnvWithMail(t, fake)
	fake.mu.Lock()
	delete(fake.principals, slug)
	fake.mu.Unlock()

	assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)
	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_mail_credentials WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("mail credentials left = %d, want 0", n)
	}
}

// Postgres accepts uppercase and braced spellings of a UUID, so the self-action
// guard must compare canonical forms or an admin could act on their own account.
func TestAdminSelfActionRefusedForAlternateUUIDSpellings(t *testing.T) {
	const self = "6f1d5c1e-8a4b-4c1e-9d55-0c2a7e3b9a10"
	h := newTestAdminUsersHandler(t)
	routes := []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{"delete", http.MethodDelete, h.HandleDeleteUser},
		{"deactivate", http.MethodPost, h.HandleDeactivateUser},
	}
	spellings := map[string]string{
		"uppercase": strings.ToUpper(self),
		"braced":    "{" + self + "}",
		"urn":       "urn:uuid:" + self,
	}
	do := func(t *testing.T, method string, handler http.HandlerFunc, id string) *httptest.ResponseRecorder {
		t.Helper()
		r := setupRoleProtectedRouter(method, "/admin/users/{userID}", handler, "board", "admin")
		req := httptest.NewRequest(method, "/admin/users/"+id, nil)
		req.Header.Set("Authorization", authHeader(generateTestToken(self, "club-1", []string{"admin"})))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	for _, rt := range routes {
		for name, id := range spellings {
			t.Run(rt.name+"/"+name, func(t *testing.T) {
				assertStatus(t, do(t, rt.method, rt.handler, id), http.StatusBadRequest)
			})
		}
		t.Run(rt.name+"/not a uuid", func(t *testing.T) {
			assertStatus(t, do(t, rt.method, rt.handler, "not-a-uuid"), http.StatusNotFound)
		})
	}
}
