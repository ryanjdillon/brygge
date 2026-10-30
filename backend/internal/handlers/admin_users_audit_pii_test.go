package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

const (
	piiName  = "Ingrid Testdottir"
	piiFirst = "Ingrid"
	piiLast  = "Testdottir"
)

// auditRowsNaming counts the audit rows, in any club, whose details mention
// the synthetic name or address.
func auditRowsNaming(t *testing.T, e *gdprProcessEnv, email string) int {
	t.Helper()
	return gdprCount(t, e.db,
		`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'
		                                  OR details::text LIKE '%' || $2 || '%'
		                                  OR details::text LIKE '%' || $3 || '%'`,
		email, piiLast, piiFirst)
}

// seedLegacyAuditRows writes the rows older versions left about a member:
// identity in the details of the delete, deactivate, reactivate, create and
// update actions, and under a user_id key on a faktura row.
func seedLegacyAuditRows(t *testing.T, e *gdprProcessEnv, userID, email string) {
	t.Helper()
	ctx := context.Background()
	for _, row := range []struct{ action, resource, id, details string }{
		{"delete_user", "user", userID, `{"old_email":"` + email + `","old_full_name":"` + piiName + `","erasure":false}`},
		{"user.deactivated", "user", userID, `{"email":"` + email + `","full_name":"` + piiName + `"}`},
		{"user.reactivated", "user", userID, `{"email":"` + email + `","full_name":"` + piiName + `"}`},
		{"create_user", "user", userID, `{"email":"` + email + `","first_name":"` + piiFirst + `","last_name":"` + piiLast + `","roles":["member"]}`},
		{"update_user", "user", userID, `{"first_name":"` + piiFirst + `","phone":"+4700000000"}`},
		{"invoice.created", "invoice", "inv-" + userID, `{"user_id":"` + userID + `","email":"` + email + `","total":450}`},
	} {
		if _, err := e.db.Exec(ctx,
			`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
			e.clubID, e.admin, row.action, row.resource, row.id, row.details); err != nil {
			t.Fatalf("seed %s: %v", row.action, err)
		}
	}
}

// A member's name and address must not outlive the member in the audit log,
// whichever path erases them. The rows stay; the identity in them goes.
func TestIntegration_AuditLogScrubbedWhenMemberErased(t *testing.T) {
	t.Parallel()

	t.Run("admin delete", func(t *testing.T) {
		t.Parallel()
		e := newAdminDeleteEnv(t)
		userID, email, _, _ := e.seedMember(t, false)
		seedLegacyAuditRows(t, e, userID, email)
		if n := auditRowsNaming(t, e, email); n == 0 {
			t.Fatalf("test seed should name the member")
		}

		assertStatus(t, e.deleteUser(userID, ""), http.StatusOK)

		if n := auditRowsNaming(t, e, email); n != 0 {
			t.Errorf("%d audit rows still name the erased member", n)
		}
		if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'delete_user'`, userID); n != 2 {
			t.Errorf("delete_user rows = %d, want the legacy row and the new one", n)
		}
		if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE details->>'total' = '450'`); n != 1 {
			t.Errorf("the faktura audit row lost its non-identity keys")
		}
	})

	t.Run("gdpr immediate erasure", func(t *testing.T) {
		t.Parallel()
		e := newGDPRProcessEnv(t)
		userID, email, _, requestID := e.seedMember(t, false)
		seedLegacyAuditRows(t, e, userID, email)

		assertStatus(t, e.process(requestID), http.StatusOK)

		if n := auditRowsNaming(t, e, email); n != 0 {
			t.Errorf("%d audit rows still name the erased member", n)
		}
	})

	// Identity erasure is deferred while invoices are in retention, so the
	// member is still named there; the purge sweep erases it later.
	t.Run("gdpr deferred erasure keeps the rows until the purge", func(t *testing.T) {
		t.Parallel()
		e := newGDPRProcessEnv(t)
		userID, email, _, requestID := e.seedMember(t, true)
		seedLegacyAuditRows(t, e, userID, email)

		assertStatus(t, e.process(requestID), http.StatusOK)

		if n := auditRowsNaming(t, e, email); n == 0 {
			t.Errorf("a deferred erasure must not scrub yet")
		}
	})
}

// New audit rows about a member hold ids only: creating, editing, deactivating,
// reactivating and deleting a member leave no name, address or phone in the log
// even before they are erased.
func TestIntegration_AdminUserAuditRowsHoldNoIdentity(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	h := NewAdminUsersHandler(e.db, &config.Config{Port: 8080}, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/admin/users/", h.HandleCreateUser)
	r.Patch("/admin/users/{userID}", h.HandleUpdateUser)
	r.Post("/admin/users/{userID}/deactivate", h.HandleDeactivateUser)
	r.Post("/admin/users/{userID}/reactivate", h.HandleReactivateUser)
	r.Delete("/admin/users/{userID}", h.HandleDeleteUser)
	e.router = r
	g := &guardEnv{e}
	admin := []string{"admin"}
	email := "ingrid." + testutil.RandomHex(4) + "@example.com"

	rec := g.call(e.admin, admin, http.MethodPost, "/admin/users/",
		`{"email":"`+email+`","first_name":"`+piiFirst+`","last_name":"`+piiLast+`","phone":"+4700000001","roles":["member"]}`)
	assertStatus(t, rec, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id := created.ID

	assertStatus(t, g.call(e.admin, admin, http.MethodPatch, "/admin/users/"+id,
		`{"first_name":"`+piiFirst+`","phone":"+4700000002","address_line":"Synthetic Road 1"}`), http.StatusOK)
	assertStatus(t, g.call(e.admin, admin, http.MethodPost, "/admin/users/"+id+"/deactivate", ""), http.StatusOK)
	assertStatus(t, g.call(e.admin, admin, http.MethodPost, "/admin/users/"+id+"/reactivate", ""), http.StatusOK)

	if n := auditRowsNaming(t, e, email); n != 0 {
		t.Fatalf("%d audit rows name the member while they are still active", n)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND details::text ~ '(4700000|Synthetic Road)'`, id); n != 0 {
		t.Errorf("%d audit rows carry the member's phone or address", n)
	}
	for _, action := range []string{"create_user", "update_user", "user.deactivated", "user.reactivated"} {
		if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = $2`, id, action); n != 1 {
			t.Errorf("%s rows = %d, want 1", action, n)
		}
	}

	assertStatus(t, g.call(e.admin, admin, http.MethodDelete, "/admin/users/"+id, ""), http.StatusOK)
	if n := auditRowsNaming(t, e, email); n != 0 {
		t.Errorf("%d audit rows name the member after the delete", n)
	}
}
