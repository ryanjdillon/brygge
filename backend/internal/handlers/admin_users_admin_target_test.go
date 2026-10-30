package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// The board reaches PATCH and the slips PUT, and an admin's email is their
// login: editing it would let the board take the account over. Every write on
// an admin target needs an admin caller; ordinary members stay editable.
func TestIntegration_BoardCannotEditAdminProfileOrSlips(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	ctx := context.Background()
	board, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"board"})
	target := e.seedAdmin(t)
	member, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	asBoard := []string{"board"}
	asAdmin := []string{"admin"}

	emailOf := func(id string) string {
		var email string
		if err := e.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
			t.Fatalf("read email: %v", err)
		}
		return email
	}
	before := emailOf(target)

	rec := e.call(board, asBoard, http.MethodPatch, "/admin/users/"+target, `{"email":"takeover@example.com","phone":"+4700000009"}`)
	assertStatus(t, rec, http.StatusForbidden)
	assertCode(t, rec, codeAdminRequired)
	if got := emailOf(target); got != before {
		t.Errorf("a refused PATCH changed the admin's email to %q", got)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1 AND phone = '+4700000009'`, target); n != 0 {
		t.Errorf("a refused PATCH must not change any field")
	}

	rec = e.call(board, asBoard, http.MethodPut, "/admin/users/"+target+"/slips", `{"slips":[]}`)
	assertStatus(t, rec, http.StatusForbidden)
	assertCode(t, rec, codeAdminRequired)

	// An admin may edit an admin; the board may edit a member.
	assertStatus(t, e.call(e.admin, asAdmin, http.MethodPatch, "/admin/users/"+target, `{"email":"renamed-admin@example.com"}`), http.StatusOK)
	if got := emailOf(target); got != "renamed-admin@example.com" {
		t.Errorf("admin PATCH of an admin: email = %q", got)
	}
	assertStatus(t, e.call(e.admin, asAdmin, http.MethodPut, "/admin/users/"+target+"/slips", `{"slips":[]}`), http.StatusOK)
	assertStatus(t, e.call(board, asBoard, http.MethodPatch, "/admin/users/"+member, `{"phone":"+4700000010"}`), http.StatusOK)
	assertStatus(t, e.call(board, asBoard, http.MethodPut, "/admin/users/"+member+"/slips", `{"slips":[]}`), http.StatusOK)

	// A malformed id is a 404, not a database error.
	assertStatus(t, e.call(board, asBoard, http.MethodPatch, "/admin/users/not-a-uuid", `{"phone":"1"}`), http.StatusNotFound)
}
