package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
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

// A hard delete cascades sessions and passkeys, but developer tokens only lose
// their creator, so access revocation must still run before the row goes.
func TestIntegration_AdminDeleteUserHardDeleteRevokesDeveloperTokens(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)
	ctx := context.Background()
	userID, _, sessionID, _ := e.seedMember(t, false)
	// The seeded deletion request references the user without a cascade.
	if _, err := e.db.Exec(ctx, `DELETE FROM deletion_requests WHERE user_id = $1`, userID); err != nil {
		t.Fatalf("clear deletion request: %v", err)
	}

	assertStatus(t, e.deleteUser(userID, "?erasure=true"), http.StatusOK)

	if n := gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1`, userID); n != 0 {
		t.Errorf("user row should be hard-deleted")
	}
	if _, _, err := auth.NewSessionService(e.db).ValidateSession(ctx, sessionID); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("ValidateSession after hard delete = %v, want ErrSessionNotFound", err)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM developer_tokens WHERE club_id = $1 AND revoked_at IS NULL`, e.clubID); n != 0 {
		t.Errorf("developer tokens left active = %d, want 0", n)
	}
	if revoked := e.deleteAuditSessionsRevoked(t, userID); revoked != 1 {
		t.Errorf("audit sessions_revoked = %d, want 1", revoked)
	}
}

// The retention refusal runs before anything is revoked or written.
func TestIntegration_AdminDeleteUserErasureRefusedLeavesAccessIntact(t *testing.T) {
	t.Parallel()
	e := newAdminDeleteEnv(t)
	ctx := context.Background()
	userID, _, sessionID, _ := e.seedMember(t, true)

	rec := e.deleteUser(userID, "?erasure=true")
	assertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "§13") {
		t.Errorf("body should name the retention rule: %s", rec.Body.String())
	}
	if _, _, err := auth.NewSessionService(e.db).ValidateSession(ctx, sessionID); err != nil {
		t.Errorf("session should survive a refused erasure: %v", err)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL`, userID); n != 1 {
		t.Errorf("a refused erasure must not deactivate the member")
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
