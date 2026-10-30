package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// guardEnv mounts the admin-user lifecycle routes behind the same gates as
// cmd/api/main.go: the board may reach the group's writes, erasing a member is
// admin-only, and the handlers refuse an admin target for a non-admin.
type guardEnv struct {
	*gdprProcessEnv
}

func newGuardEnv(t *testing.T) *guardEnv {
	t.Helper()
	e := newGDPRProcessEnv(t)
	h := NewAdminUsersHandler(e.db, &config.Config{Port: 8080}, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Route("/admin/users", func(r chi.Router) {
		r.Use(middleware.RequireRoleOrBoardRead("board", "admin"))
		r.Post("/", h.HandleCreateUser)
		r.Patch("/{userID}", h.HandleUpdateUser)
		r.Put("/{userID}/slips", h.HandleSetUserSlips)
		r.Put("/{userID}/roles", h.HandleUpdateUserRoles)
		r.With(middleware.RequireRole("admin")).Delete("/{userID}", h.HandleDeleteUser)
		r.Post("/{userID}/deactivate", h.HandleDeactivateUser)
		r.Post("/{userID}/reactivate", h.HandleReactivateUser)
	})
	r.Post(processDeletionPath, e.h.HandleProcessDeletion)
	e.router = r
	return &guardEnv{e}
}

// call sends a request as callerID carrying the given role claims.
func (e *guardEnv) call(callerID string, roles []string, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", authHeader(generateTestToken(callerID, e.clubID, roles)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// staleAdmin seeds a member whose session still carries the admin role while
// the database no longer grants it, so a request can act on the club's last
// active admin without being that admin.
func (e *guardEnv) staleAdmin(t *testing.T) string {
	t.Helper()
	id, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	return id
}

func (e *guardEnv) seedAdmin(t *testing.T) string {
	t.Helper()
	id, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"admin"})
	return id
}

func (e *guardEnv) active(t *testing.T, userID string) bool {
	t.Helper()
	return gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL`, userID) == 1
}

func (e *guardEnv) activeAdmins(t *testing.T) int {
	t.Helper()
	return gdprCount(t, e.db,
		`SELECT count(*) FROM user_roles ur JOIN users u ON u.id = ur.user_id
		  WHERE ur.club_id = $1 AND ur.role = 'admin' AND u.deactivated_at IS NULL`, e.clubID)
}

func assertCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	if !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Errorf("body should carry code %q: %s", code, rec.Body.String())
	}
}

func (e *guardEnv) seedDevToken(t *testing.T, userID string) {
	t.Helper()
	if _, err := e.db.Exec(context.Background(),
		`INSERT INTO developer_tokens (club_id, name, token_prefix, token_hash, created_by, expires_at)
		 VALUES ($1, 'test token', 'bdt_test', $2, $3, now() + interval '1 day')`,
		e.clubID, "hash-"+testutil.RandomHex(8), userID,
	); err != nil {
		t.Fatalf("seed developer token: %v", err)
	}
}

func (e *guardEnv) activeTokens(t *testing.T, userID string) int {
	t.Helper()
	return gdprCount(t, e.db,
		`SELECT count(*) FROM developer_tokens WHERE created_by = $1 AND revoked_at IS NULL`, userID)
}

func rolesBody(roles ...string) string {
	return `{"roles":["` + strings.Join(roles, `","`) + `"]}`
}

// A board member with a fresh TOTP can reach the group's writes, but not the
// people above them: no delete, deactivate, reactivate or role change on an
// admin, and no granting the admin role.
func TestIntegration_BoardCannotActOnAdmin(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	board, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"board"})
	target := e.seedAdmin(t) // a second admin, so the last-admin guard is not what refuses
	member, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	asBoard := []string{"board"}

	assertStatus(t, e.call(board, asBoard, http.MethodDelete, "/admin/users/"+target, ""), http.StatusForbidden)

	rec := e.call(board, asBoard, http.MethodPost, "/admin/users/"+target+"/deactivate", "")
	assertStatus(t, rec, http.StatusForbidden)
	assertCode(t, rec, codeAdminRequired)

	rec = e.call(board, asBoard, http.MethodPut, "/admin/users/"+target+"/roles", rolesBody("member"))
	assertStatus(t, rec, http.StatusForbidden)
	assertCode(t, rec, codeAdminRequired)

	// Promoting a member, or oneself, to admin is refused for the same reason.
	assertStatus(t, e.call(board, asBoard, http.MethodPut, "/admin/users/"+member+"/roles", rolesBody("member", "admin")), http.StatusForbidden)
	assertStatus(t, e.call(board, asBoard, http.MethodPut, "/admin/users/"+board+"/roles", rolesBody("board", "admin")), http.StatusForbidden)
	assertStatus(t, e.call(board, asBoard, http.MethodPost, "/admin/users/",
		`{"email":"new-admin@example.com","first_name":"New","last_name":"Admin","roles":["admin"]}`), http.StatusForbidden)

	// A deactivated admin cannot be brought back by the board either.
	if _, err := e.db.Exec(context.Background(), `UPDATE users SET deactivated_at = now() WHERE id = $1`, target); err != nil {
		t.Fatalf("deactivate target: %v", err)
	}
	assertStatus(t, e.call(board, asBoard, http.MethodPost, "/admin/users/"+target+"/reactivate", ""), http.StatusForbidden)

	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_roles WHERE user_id = $1 AND role = 'admin'`, target); n != 1 {
		t.Errorf("the admin role must be untouched")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM user_roles WHERE role = 'admin' AND user_id IN ($1, $2)`, member, board); n != 0 {
		t.Errorf("the board must not have promoted anyone to admin")
	}
}

// The board keeps what it had: deactivating and reactivating an ordinary
// member, and editing their roles.
func TestIntegration_BoardCanDeactivateAndReactivateMember(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	board, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"board"})
	member, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	asBoard := []string{"board"}

	assertStatus(t, e.call(board, asBoard, http.MethodPost, "/admin/users/"+member+"/deactivate", ""), http.StatusOK)
	if e.active(t, member) {
		t.Fatalf("member should be deactivated")
	}
	assertStatus(t, e.call(board, asBoard, http.MethodPost, "/admin/users/"+member+"/reactivate", ""), http.StatusOK)
	if !e.active(t, member) {
		t.Fatalf("member should be active again")
	}
	assertStatus(t, e.call(board, asBoard, http.MethodPut, "/admin/users/"+member+"/roles", rolesBody("member", "slip_holder")), http.StatusOK)
	// Erasing is the admin's alone.
	assertStatus(t, e.call(board, asBoard, http.MethodDelete, "/admin/users/"+member, ""), http.StatusForbidden)
}

// The club's only active admin cannot be deleted, deactivated or demoted, not
// even by another admin; a second active admin makes each possible, and a
// deactivated admin does not count as one.
func TestIntegration_LastAdminIsProtected(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	last := e.admin
	caller := e.staleAdmin(t)
	asAdmin := []string{"admin"}

	rec := e.call(caller, asAdmin, http.MethodDelete, "/admin/users/"+last, "")
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)

	rec = e.call(caller, asAdmin, http.MethodPost, "/admin/users/"+last+"/deactivate", "")
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)

	rec = e.call(caller, asAdmin, http.MethodPut, "/admin/users/"+last+"/roles", rolesBody("board"))
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)

	// The admin cannot demote themselves out of the role either.
	rec = e.call(last, asAdmin, http.MethodPut, "/admin/users/"+last+"/roles", rolesBody("member"))
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)

	if !e.active(t, last) || e.activeAdmins(t) != 1 {
		t.Fatalf("the last admin must be untouched")
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action IN ('delete_user','user.deactivated','update_roles')`, last); n != 0 {
		t.Errorf("a refused change must not be audited, got %d rows", n)
	}

	// A second admin makes the first removable...
	second := e.seedAdmin(t)
	assertStatus(t, e.call(caller, asAdmin, http.MethodPost, "/admin/users/"+last+"/deactivate", ""), http.StatusOK)
	// ...and leaves them the last.
	rec = e.call(caller, asAdmin, http.MethodPost, "/admin/users/"+second+"/deactivate", "")
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)
	rec = e.call(caller, asAdmin, http.MethodDelete, "/admin/users/"+second, "")
	assertStatus(t, rec, http.StatusConflict)

	// The deactivated admin does not count: bringing them back is allowed and
	// then the second can be demoted.
	assertStatus(t, e.call(caller, asAdmin, http.MethodPost, "/admin/users/"+last+"/reactivate", ""), http.StatusOK)
	assertStatus(t, e.call(caller, asAdmin, http.MethodPut, "/admin/users/"+second+"/roles", rolesBody("member")), http.StatusOK)
	if e.activeAdmins(t) != 1 {
		t.Errorf("active admins = %d, want 1", e.activeAdmins(t))
	}
}

// With a second admin an admin can be deleted (anonymised) like any member.
func TestIntegration_AdminCanDeleteAnotherAdmin(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	second := e.seedAdmin(t)

	assertStatus(t, e.call(e.admin, []string{"admin"}, http.MethodDelete, "/admin/users/"+second, ""), http.StatusOK)
	if e.activeAdmins(t) != 1 {
		t.Errorf("active admins = %d, want 1", e.activeAdmins(t))
	}
}

// Two admins removed at the same time must leave one. The call that arrives
// while another holds the club's admin lock waits for it, then sees the
// other admin already gone and is refused.
func TestIntegration_LastAdminGuardWaitsForTheClubLock(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	ctx := context.Background()
	if _, err := e.db.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, e.admin); err != nil {
		t.Fatalf("clear seeded admin: %v", err)
	}
	x, y := e.seedAdmin(t), e.seedAdmin(t)
	caller := e.staleAdmin(t)

	// A competing change holds the lock and removes y, uncommitted.
	tx, err := e.db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := lockClubAdmins(ctx, tx, e.clubID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.call(caller, []string{"admin"}, http.MethodPost, "/admin/users/"+x+"/deactivate", "")
	}()
	select {
	case rec := <-done:
		t.Fatalf("deactivate did not wait for the club admin lock: %d %s", rec.Code, rec.Body.String())
	case <-time.After(500 * time.Millisecond):
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, y); err != nil {
		t.Fatalf("deactivate y: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rec := <-done
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)
	if !e.active(t, x) {
		t.Errorf("x must remain active")
	}
}

// Whatever the interleaving, two simultaneous removals of the only two admins
// leave exactly one.
func TestIntegration_ConcurrentAdminRemovalLeavesOne(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"deactivate", "delete", "demote"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			e := newGuardEnv(t)
			if _, err := e.db.Exec(context.Background(), `DELETE FROM user_roles WHERE user_id = $1`, e.admin); err != nil {
				t.Fatalf("clear seeded admin: %v", err)
			}
			targets := []string{e.seedAdmin(t), e.seedAdmin(t)}
			caller := e.staleAdmin(t)

			remove := func(id string) *httptest.ResponseRecorder {
				asAdmin := []string{"admin"}
				switch how {
				case "delete":
					return e.call(caller, asAdmin, http.MethodDelete, "/admin/users/"+id, "")
				case "demote":
					return e.call(caller, asAdmin, http.MethodPut, "/admin/users/"+id+"/roles", rolesBody("member"))
				}
				return e.call(caller, asAdmin, http.MethodPost, "/admin/users/"+id+"/deactivate", "")
			}

			var wg sync.WaitGroup
			start := make(chan struct{})
			codes := make([]int, len(targets))
			for i, id := range targets {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					codes[i] = remove(id).Code
				}()
			}
			close(start)
			wg.Wait()

			ok, refused := 0, 0
			for _, c := range codes {
				switch c {
				case http.StatusOK:
					ok++
				case http.StatusConflict:
					refused++
				}
			}
			if ok != 1 || refused != 1 {
				t.Errorf("statuses = %v, want one 200 and one 409", codes)
			}
			if n := e.activeAdmins(t); n != 1 {
				t.Errorf("active admins = %d, want 1", n)
			}
		})
	}
}

// Processing the deletion request of the club's only active admin is refused,
// the request stays pending, and it goes through once another admin exists.
func TestIntegration_GDPRProcessRefusesLastAdmin(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	ctx := context.Background()
	var requestID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO deletion_requests (user_id, club_id, grace_end)
		 VALUES ($1, $2, now() - interval '1 day') RETURNING id`, e.admin, e.clubID,
	).Scan(&requestID); err != nil {
		t.Fatalf("seed deletion request: %v", err)
	}
	caller := e.staleAdmin(t)
	process := func() *httptest.ResponseRecorder {
		return e.call(caller, []string{"admin"}, http.MethodPost,
			"/admin/gdpr/deletion-requests/"+requestID+"/process", "")
	}

	rec := process()
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, codeLastAdmin)
	if n := gdprCount(t, e.db, `SELECT count(*) FROM deletion_requests WHERE id = $1 AND status = 'pending'`, requestID); n != 1 {
		t.Errorf("the request must stay pending")
	}
	if !e.active(t, e.admin) {
		t.Errorf("the admin must stay active")
	}

	e.seedAdmin(t)
	assertStatus(t, process(), http.StatusOK)
	if e.active(t, e.admin) {
		t.Errorf("the admin should be deactivated once another admin exists")
	}
}

// Deactivating a member or taking the admin role from them revokes the
// developer tokens they minted, and reactivating or regranting does not bring
// them back.
func TestIntegration_DeactivateAndDemoteRevokeDeveloperTokens(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	asAdmin := []string{"admin"}

	t.Run("deactivate then reactivate", func(t *testing.T) {
		target := e.seedAdmin(t)
		e.seedDevToken(t, target)
		assertStatus(t, e.call(e.admin, asAdmin, http.MethodPost, "/admin/users/"+target+"/deactivate", ""), http.StatusOK)
		if n := e.activeTokens(t, target); n != 0 {
			t.Errorf("tokens active after deactivate = %d, want 0", n)
		}
		assertStatus(t, e.call(e.admin, asAdmin, http.MethodPost, "/admin/users/"+target+"/reactivate", ""), http.StatusOK)
		if n := e.activeTokens(t, target); n != 0 {
			t.Errorf("reactivation must not resurrect tokens, active = %d", n)
		}
	})

	t.Run("demote then regrant", func(t *testing.T) {
		target := e.seedAdmin(t)
		e.seedDevToken(t, target)
		assertStatus(t, e.call(e.admin, asAdmin, http.MethodPut, "/admin/users/"+target+"/roles", rolesBody("member")), http.StatusOK)
		if n := e.activeTokens(t, target); n != 0 {
			t.Errorf("tokens active after losing admin = %d, want 0", n)
		}
		assertStatus(t, e.call(e.admin, asAdmin, http.MethodPut, "/admin/users/"+target+"/roles", rolesBody("admin")), http.StatusOK)
		if n := e.activeTokens(t, target); n != 0 {
			t.Errorf("regranting must not resurrect tokens, active = %d", n)
		}
	})

	t.Run("a role change that keeps admin keeps the tokens", func(t *testing.T) {
		target := e.seedAdmin(t)
		e.seedDevToken(t, target)
		assertStatus(t, e.call(e.admin, asAdmin, http.MethodPut, "/admin/users/"+target+"/roles", rolesBody("admin", "board")), http.StatusOK)
		if n := e.activeTokens(t, target); n != 1 {
			t.Errorf("tokens active = %d, want 1", n)
		}
	})
}
