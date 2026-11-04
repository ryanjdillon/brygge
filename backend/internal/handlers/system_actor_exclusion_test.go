package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/passkey"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// The club's system actor (ACC-13) is a users row that is not a person. These
// tests drive the sites that list, match, load or change users and assert it is
// never returned, matched or changed. Each case fails without the
// kind = 'person' filter or the loadPersonUser call at its site, with these
// exceptions, which pin the outcome only:
//   - magic-link verify: SessionService.CreateSession refuses the system actor
//     too, so a login stops there even without the lookup filter;
//   - demo login: it accepts only the fixed demo addresses, so the filter in its
//     lookup is defence in depth and has no test of its own.

const systemActorName = "Automatisk bokføring"

type systemActorEnv struct {
	db       *pgxpool.Pool
	cfg      *config.Config
	clubID   string
	adminID  string
	systemID string
}

func newSystemActorEnv(t *testing.T) *systemActorEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, db)
	adminID, _ := testutil.SeedUser(t, db, clubID, []string{"admin"})
	systemID, err := accounting.NewService(db, nil, zerolog.Nop()).SystemActor(context.Background(), clubID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	var slug string
	if err := db.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("club slug: %v", err)
	}
	return &systemActorEnv{db: db, cfg: &config.Config{Port: 8080, ClubSlug: slug}, clubID: clubID, adminID: adminID, systemID: systemID}
}

// do sends one request as the club's admin through a router that mounts h at
// pattern.
func (e *systemActorEnv) do(t *testing.T, method, pattern, target string, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Method(method, pattern, h)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", authHeader(generateTestToken(e.adminID, e.clubID, []string{"admin"})))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (e *systemActorEnv) systemRow(t *testing.T) (email, first, last string, deactivated bool, roles int) {
	t.Helper()
	if err := e.db.QueryRow(context.Background(),
		`SELECT email, first_name, last_name, deactivated_at IS NOT NULL,
		        (SELECT count(*) FROM user_roles WHERE user_id = u.id)
		   FROM users u WHERE id = $1`, e.systemID,
	).Scan(&email, &first, &last, &deactivated, &roles); err != nil {
		t.Fatalf("read system actor: %v", err)
	}
	return
}

func (e *systemActorEnv) assertSystemUntouched(t *testing.T) {
	t.Helper()
	email, first, last, deactivated, roles := e.systemRow(t)
	if email != accounting.SystemActorEmail || first != "Automatisk" || last != "bokføring" || deactivated || roles != 0 {
		t.Errorf("system actor changed: %q %q %q deactivated=%v roles=%d", email, first, last, deactivated, roles)
	}
}

func TestIntegration_SystemActorHiddenFromAdminLists(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	person, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	t.Run("list and count", func(t *testing.T) {
		for _, status := range []string{"", "all", "deactivated"} {
			rec := e.do(t, http.MethodGet, "/admin/users", "/admin/users?status="+status, h.HandleListUsers, "")
			assertStatus(t, rec, http.StatusOK)
			var body struct {
				Users []struct {
					ID string `json:"id"`
				} `json:"users"`
				Total int `json:"total_count"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantTotal := 2 // the admin and the member
			if status == "deactivated" {
				wantTotal = 0
			}
			if body.Total != wantTotal || len(body.Users) != wantTotal {
				t.Errorf("status=%q: total %d, %d rows; want %d", status, body.Total, len(body.Users), wantTotal)
			}
			for _, u := range body.Users {
				if u.ID == e.systemID {
					t.Errorf("status=%q: the system actor is listed", status)
				}
			}
			if strings.Contains(rec.Body.String(), accounting.SystemActorEmail) {
				t.Errorf("status=%q: the system actor's address is in the response", status)
			}
		}
	})

	t.Run("a search for its name finds nobody", func(t *testing.T) {
		rec := e.do(t, http.MethodGet, "/admin/users", "/admin/users?status=all&search=Automatisk", h.HandleListUsers, "")
		assertStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), e.systemID) {
			t.Errorf("a name search lists the system actor: %s", rec.Body.String())
		}
	})

	t.Run("csv export", func(t *testing.T) {
		rec := e.do(t, http.MethodGet, "/admin/users/export.csv", "/admin/users/export.csv", h.HandleExportUsersCSV, "")
		assertStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), accounting.SystemActorEmail) || strings.Contains(rec.Body.String(), "Automatisk") {
			t.Errorf("the system actor is in the export: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Test") {
			t.Errorf("the export lost the real members: %s", rec.Body.String())
		}
	})
	_ = person
}

// The directory joins user_roles, so the trigger alone keeps the system actor
// out. The test switches the trigger off to hand it a role, and the kind
// filter must still hide it.
func TestIntegration_SystemActorHiddenFromDirectory(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	if _, err := e.db.Exec(ctx, `ALTER TABLE user_roles DISABLE TRIGGER user_roles_refuse_system_user`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := e.db.Exec(ctx,
		`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'member'::user_role)`,
		e.systemID, e.clubID); err != nil {
		t.Fatalf("force a role past the trigger: %v", err)
	}

	h := NewMembersHandler(e.db, e.cfg, zerolog.Nop())
	rec := e.do(t, http.MethodGet, "/directory", "/directory", h.HandleGetDirectory, "")
	assertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Automatisk") || strings.Contains(rec.Body.String(), accounting.SystemActorEmail) {
		t.Errorf("the system actor is in the directory: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Test") {
		t.Errorf("the directory lost the real member: %s", rec.Body.String())
	}
}

func TestIntegration_SystemActorHiddenFromVolunteerReport(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	h := NewVolunteerHandler(e.db, e.cfg, zerolog.Nop())
	rec := e.do(t, http.MethodGet, "/volunteer/hours", "/volunteer/hours", h.HandleListAllVolunteerHours, "")
	assertStatus(t, rec, http.StatusOK)
	var rows []struct {
		UserID string `json:"user_id"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(rows) != 1 || rows[0].UserID != e.adminID {
		t.Errorf("volunteer report rows = %+v, want only the admin", rows)
	}
}

func TestIntegration_SystemActorGetsNoMagicLinkOrSession(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	mock := &email.MockSender{}
	h := NewMagicLinkHandler(e.db, e.cfg, mock, auth.NewSessionService(e.db), zerolog.Nop())

	rec := e.do(t, http.MethodPost, "/auth/magic-link", "/auth/magic-link", h.HandleRequestMagicLink,
		`{"email":"`+accounting.SystemActorEmail+`"}`)
	assertStatus(t, rec, http.StatusOK)
	var links int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM magic_links WHERE email = $1`, accounting.SystemActorEmail).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 || len(mock.Calls) != 0 {
		t.Errorf("magic link requested for the system actor: %d rows, %d mails", links, len(mock.Calls))
	}

	sessions := auth.NewSessionService(e.db)
	if _, err := sessions.CreateSession(ctx, e.systemID, e.clubID, "198.51.100.4:5555", "test-agent"); !errors.Is(err, auth.ErrNotAPerson) {
		t.Errorf("CreateSession(system actor) = %v, want ErrNotAPerson", err)
	}
	// A session row that exists anyway (written outside CreateSession) does not validate.
	if _, err := e.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, club_id, expires_at) VALUES ('forced-system-session', $1, $2, now() + interval '1 hour')`,
		e.systemID, e.clubID); err != nil {
		t.Fatalf("force a session row: %v", err)
	}
	if _, _, err := sessions.ValidateSession(ctx, "forced-system-session"); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("ValidateSession(system actor) = %v, want ErrSessionNotFound", err)
	}

	// A person still gets one.
	personID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	if _, err := sessions.CreateSession(ctx, personID, e.clubID, "198.51.100.4:5555", "test-agent"); err != nil {
		t.Errorf("CreateSession(person) = %v", err)
	}
}

// Every by-id admin endpoint answers 404 for the system actor, as for a
// missing user, and changes nothing.
func TestIntegration_SystemActorAnswers404OnByIDAdminEndpoints(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())
	totp := NewTOTPHandler(e.db, e.cfg, auth.NewSessionService(e.db), nil, zerolog.Nop())
	passkeys := NewPasskeyHandler(nil, passkey.NewStore(e.db), nil, auth.NewSessionService(e.db), nil, e.cfg, zerolog.Nop())
	id := e.systemID
	pattern := "/admin/users/{userID}"
	base := "/admin/users/" + id
	// A boat the system actor owns, so the boat endpoints are refused for the
	// user, not merely for a boat that does not exist.
	var boatID string
	if err := e.db.QueryRow(context.Background(),
		`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic boat') RETURNING id`,
		id, e.clubID).Scan(&boatID); err != nil {
		t.Fatalf("seed a boat owned by the system actor: %v", err)
	}
	// A vacant slip to put that boat on: releasing a boat that has no slip is a
	// 404 by itself, so the case must try to assign one.
	var slipID string
	if err := e.db.QueryRow(context.Background(),
		`INSERT INTO slips (club_id, number) VALUES ($1, 'B7') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
		t.Fatalf("seed a slip: %v", err)
	}

	// "delete boat" stays last: when the guard is missing it would remove the
	// boat the cases before it need.
	cases := []struct {
		name, method, pattern, target, body string
		h                                   http.HandlerFunc
	}{
		{"get", http.MethodGet, pattern, base, "", h.HandleGetUser},
		{"list boats", http.MethodGet, pattern + "/boats", base + "/boats", "", h.HandleListUserBoats},
		{"patch", http.MethodPatch, pattern, base, `{"first_name":"Renamed"}`, h.HandleUpdateUser},
		{"roles", http.MethodPut, pattern + "/roles", base + "/roles", `{"roles":["admin"]}`, h.HandleUpdateUserRoles},
		{"slips", http.MethodPut, pattern + "/slips", base + "/slips", `{"slips":[]}`, h.HandleSetUserSlips},
		{"create boat", http.MethodPost, pattern + "/boats", base + "/boats", `{"name":"Synthetic"}`, h.HandleCreateUserBoat},
		{"update boat", http.MethodPut, pattern + "/boats/{boatID}", base + "/boats/" + boatID, `{"name":"Renamed boat"}`, h.HandleUpdateUserBoat},
		{"boat slip", http.MethodPut, pattern + "/boats/{boatID}/slip", base + "/boats/" + boatID + "/slip", `{"slip_id":"` + slipID + `"}`, h.HandleSetUserBoatSlip},
		{"deactivate", http.MethodPost, pattern + "/deactivate", base + "/deactivate", "", h.HandleDeactivateUser},
		{"erase", http.MethodDelete, pattern, base, "", h.HandleDeleteUser},
		{"totp disable", http.MethodPost, pattern + "/totp/disable", base + "/totp/disable", "", totp.HandleAdminDisableTOTP},
		{"passkey reset", http.MethodDelete, pattern + "/passkeys", base + "/passkeys", "", passkeys.HandleAdminReset},
		{"delete boat", http.MethodDelete, pattern + "/boats/{boatID}", base + "/boats/" + boatID, "", h.HandleDeleteUserBoat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.do(t, c.method, c.pattern, c.target, c.h, c.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404; body: %s", c.method, c.target, rec.Code, rec.Body.String())
			}
		})
	}
	e.assertSystemUntouched(t)
	var boats int
	var boatName string
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(max(name), '') FROM boats WHERE id = $1`, boatID).Scan(&boats, &boatName); err != nil ||
		boats != 1 || boatName != "Synthetic boat" {
		t.Errorf("the system actor's boat after the refused calls: %d rows, name %q, %v", boats, boatName, err)
	}
	var assigned int
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*) FROM slip_assignments WHERE boat_id = $1`, boatID).Scan(&assigned); err != nil || assigned != 0 {
		t.Errorf("slip assignments for the system actor's boat = %d, %v; want 0", assigned, err)
	}
	var audited int
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE resource_id = $1`, id).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("audit rows about the system actor = %d, %v; want 0", audited, err)
	}
}

func TestIntegration_LoadPersonUser(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	person, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	otherClub := testutil.SeedClub(t, e.db)

	for _, c := range []struct {
		name, club, user string
		want             error
	}{
		{"person", e.clubID, person, nil},
		{"system actor", e.clubID, e.systemID, errUserNotFound},
		{"person of another club", otherClub, person, errUserNotFound},
		{"unknown id", e.clubID, "00000000-0000-4000-8000-000000000000", errUserNotFound},
		{"not a uuid", e.clubID, "not-a-uuid", errUserNotFound},
	} {
		if err := loadPersonUser(ctx, e.db, c.club, c.user); !errors.Is(err, c.want) {
			t.Errorf("%s: loadPersonUser = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestIntegration_SystemActorWaitingListAndInvoiceSites(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)

	t.Run("waiting list enroll", func(t *testing.T) {
		h := NewWaitingListHandler(e.db, nil, e.cfg, zerolog.Nop())
		rec := e.do(t, http.MethodPost, "/enroll", "/enroll", h.HandleAdminEnrollMember, `{"user_id":"`+e.systemID+`"}`)
		assertStatus(t, rec, http.StatusNotFound)
		var n int
		if err := e.db.QueryRow(context.Background(),
			`SELECT count(*) FROM waiting_list_entries WHERE user_id = $1`, e.systemID).Scan(&n); err != nil || n != 0 {
			t.Errorf("waiting list entries for the system actor = %d, %v", n, err)
		}
	})

	t.Run("manual payment for a user id", func(t *testing.T) {
		h := NewFinancialsHandler(e.db, e.cfg, nil, zerolog.Nop())
		rec := e.do(t, http.MethodPost, "/payments", "/payments", h.HandleGenerateInvoice,
			`{"user_id":"`+e.systemID+`","type":"membership","amount":100,"due_date":"2031-01-31"}`)
		assertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("invoice for a user id", func(t *testing.T) {
		h := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
		rec := e.do(t, http.MethodPost, "/invoices", "/invoices", h.HandleCreateInvoice,
			`{"user_id":"`+e.systemID+`","due_date":"2031-01-31","lines":[{"description":"Synthetic","quantity":1,"unit_price":100}]}`)
		assertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("bulk invoice selection", func(t *testing.T) {
		h := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
		rec := e.do(t, http.MethodPost, "/invoices/bulk", "/invoices/bulk", h.HandleBulkCreateInvoices,
			`{"user_ids":["`+e.systemID+`"],"fiscal_period_id":"00000000-0000-4000-8000-000000000000","due_date":"2031-01-31"}`)
		assertStatus(t, rec, http.StatusNotFound)
	})
}

// A faktura export from the old system whose customer is called "Automatisk
// bokføring" must not link to the system actor.
func TestIntegration_SystemActorNotLinkedByInvoiceImport(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	h := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())

	csvBody := "fakturanr,kunde_navn,dato,forfall,status,nettosum\n" +
		"9001," + systemActorName + ",01.03.2031,31.03.2031,Utestående,100\n"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "import.csv")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	_, _ = part.Write([]byte(csvBody))
	_ = mw.Close()

	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/import", h.HandleImportUni24Invoices)
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", authHeader(generateTestToken(e.adminID, e.clubID, []string{"admin"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var linked *string
	var rows int
	if err := e.db.QueryRow(ctx,
		`SELECT count(*), max(user_id::text) FROM invoices WHERE club_id = $1 AND external_id = '9001'`, e.clubID,
	).Scan(&rows, &linked); err != nil {
		t.Fatalf("read imported invoice: %v", err)
	}
	if rows != 1 {
		t.Fatalf("imported invoices = %d, want 1; body: %s", rows, rec.Body.String())
	}
	if linked != nil {
		t.Errorf("the imported invoice links to user %s, want no link", *linked)
	}
}

// No path that sets users.email takes a .invalid address, so no person can
// hold the system actor's address.
func TestIntegration_InvalidEmailRefusedForPeople(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	for _, addr := range []string{"x@system.invalid", "x@Other.INVALID", "Someone <x@sub.domain.invalid>"} {
		body, _ := json.Marshal(map[string]any{"email": addr, "first_name": "Synthetic", "last_name": "Person"})
		rec := e.do(t, http.MethodPost, "/admin/users", "/admin/users", h.HandleCreateUser, string(body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create with %q = %d, want 400; body: %s", addr, rec.Code, rec.Body.String())
		}
	}

	person, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	body := `{"email":"` + accounting.SystemActorEmail + `"}`
	rec := e.do(t, http.MethodPatch, "/admin/users/{userID}", "/admin/users/"+person, h.HandleUpdateUser, body)
	assertStatus(t, rec, http.StatusBadRequest)

	var n int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE email LIKE '%.invalid%' AND kind = 'person'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("people holding a .invalid address = %d, %v", n, err)
	}

	// An ordinary address still works.
	ok, _ := json.Marshal(map[string]any{"email": "valid.person@example.com", "first_name": "Synthetic", "last_name": "Person"})
	assertStatus(t, e.do(t, http.MethodPost, "/admin/users", "/admin/users", h.HandleCreateUser, string(ok)), http.StatusCreated)
}

// The database refuses a role for the system actor even from a path that does
// not know about it.
func TestIntegration_SystemActorRoleTriggerRefuses(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	_, err := e.db.Exec(context.Background(),
		`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'admin'::user_role)`, e.systemID, e.clubID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("role for the system actor: %v, want check_violation (23514)", err)
	}
}

// Last-admin and erasure interplay: the system actor never counts as an admin,
// and neither erasure path can run on it.
func TestIntegration_SystemActorLastAdminAndErasure(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()

	tx, err := e.db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // test cleanup
	if err := ensureNotLastActiveAdmin(ctx, tx, e.clubID, e.adminID); !errors.Is(err, errLastAdmin) {
		t.Errorf("ensureNotLastActiveAdmin(only admin, plus a system actor) = %v, want errLastAdmin", err)
	}
	if err := ensureNotLastActiveAdmin(ctx, tx, e.clubID, e.systemID); err != nil {
		t.Errorf("ensureNotLastActiveAdmin(system actor) = %v, want nil (not an admin)", err)
	}

	if _, err := revokeMemberAccess(ctx, tx, e.clubID, e.systemID, e.adminID); err == nil {
		t.Error("revokeMemberAccess accepted the system actor")
	}
	_ = tx.Rollback(ctx)
	e.assertSystemUntouched(t)
}

// Reactivate is only a no-op for a user who is not deactivated, so the system
// actor is deactivated first (a state it never reaches by itself): without
// loadPersonUser the call would bring it back and answer 200.
func TestIntegration_SystemActorReactivateAnswers404(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	if _, err := e.db.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, e.systemID); err != nil {
		t.Fatalf("force deactivation: %v", err)
	}
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())
	rec := e.do(t, http.MethodPost, "/admin/users/{userID}/reactivate", "/admin/users/"+e.systemID+"/reactivate", h.HandleReactivateUser, "")
	assertStatus(t, rec, http.StatusNotFound)
	var stillDeactivated bool
	if err := e.db.QueryRow(ctx, `SELECT deactivated_at IS NOT NULL FROM users WHERE id = $1`, e.systemID).Scan(&stillDeactivated); err != nil || !stillDeactivated {
		t.Errorf("the system actor was reactivated: %v, %v", stillDeactivated, err)
	}
}

// The durable pay link mints a token for the user it is asked about; the
// system actor gets none.
func TestIntegration_SystemActorGetsNoPayLink(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	h := &InvoiceHandler{
		db:        e.db,
		config:    &config.Config{FrontendURL: "https://example.org"},
		providers: payments.NewRegistry(vipps.NewPaymentProvider(vipps.New("", "", "", "", "", zerolog.Nop()))),
		log:       zerolog.Nop(),
	}
	rec := e.do(t, http.MethodGet, "/users/{userID}/pay-link", "/users/"+e.systemID+"/pay-link", h.HandleGetUserPayLink, "")
	assertStatus(t, rec, http.StatusNotFound)
	var token *string
	if err := e.db.QueryRow(context.Background(), `SELECT payables_token FROM users WHERE id = $1`, e.systemID).Scan(&token); err != nil || token != nil {
		t.Errorf("the system actor holds a payables token: %v, %v", token, err)
	}

	// Control: a person gets one, so the 404 above is the filter and not a broken fixture.
	personID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	assertStatus(t, e.do(t, http.MethodGet, "/users/{userID}/pay-link", "/users/"+personID+"/pay-link", h.HandleGetUserPayLink, ""), http.StatusOK)
}

// A magic link row that names the system address (written outside the request
// handler, which never creates one) does not log anyone in: no session is made.
// The request handler's lookup filter and CreateSession both stop it, so this
// pins the outcome, not the filter alone.
func TestIntegration_SystemActorMagicLinkVerifyIssuesNoSession(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	if _, err := e.db.Exec(ctx,
		`INSERT INTO magic_links (token, email, club_id, expires_at) VALUES ('synthetic-system-token', $1, $2, now() + interval '10 minutes')`,
		accounting.SystemActorEmail, e.clubID); err != nil {
		t.Fatalf("seed magic link: %v", err)
	}
	h := NewMagicLinkHandler(e.db, e.cfg, &email.MockSender{}, auth.NewSessionService(e.db), zerolog.Nop())
	rec := e.do(t, http.MethodGet, "/auth/verify", "/auth/verify?token=synthetic-system-token", h.HandleVerifyMagicLink, "")
	if rec.Code < 400 {
		t.Errorf("verify for the system actor answered %d, want an error; body: %s", rec.Code, rec.Body.String())
	}
	var sessions int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, e.systemID).Scan(&sessions); err != nil || sessions != 0 {
		t.Errorf("sessions for the system actor = %d, %v; want 0", sessions, err)
	}
}

// Every place that takes a user id from the request and stores it: the system
// actor and a user of another club are both refused with 404, and nothing is
// written. Each case ends with a control showing a person of the club is accepted.
func TestIntegration_AssigneeIDsMustBePeopleOfTheClub(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	other := testutil.SeedClub(t, e.db)
	foreignUser, _ := testutil.SeedUser(t, e.db, other, []string{"member"})
	person, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	refused := []struct{ name, id string }{{"system actor", e.systemID}, {"user of another club", foreignUser}}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := e.db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	t.Run("slip assignment", func(t *testing.T) {
		h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())
		var slipID, boatID string
		if err := e.db.QueryRow(ctx, `INSERT INTO slips (club_id, number) VALUES ($1, 'A1') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
			t.Fatalf("seed slip: %v", err)
		}
		if err := e.db.QueryRow(ctx, `INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`, person, e.clubID).Scan(&boatID); err != nil {
			t.Fatalf("seed boat: %v", err)
		}
		const pattern = "/users/{userID}/boats/{boatID}/slip"
		body := `{"slip_id":"` + slipID + `"}`
		for _, r := range refused {
			rec := e.do(t, http.MethodPut, pattern, "/users/"+r.id+"/boats/"+boatID+"/slip", h.HandleSetUserBoatSlip, body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: assign = %d, want 404; body: %s", r.name, rec.Code, rec.Body.String())
			}
		}
		if n := count(`SELECT count(*) FROM slip_assignments WHERE slip_id = $1`, slipID); n != 0 {
			t.Errorf("slip assignments written for a refused user: %d", n)
		}
		if n := count(`SELECT count(*) FROM slips WHERE id = $1 AND status = 'vacant'`, slipID); n != 1 {
			t.Errorf("the slip left the vacant state")
		}
		if rec := e.do(t, http.MethodPut, pattern, "/users/"+person+"/boats/"+boatID+"/slip", h.HandleSetUserBoatSlip, body); rec.Code != http.StatusOK {
			t.Errorf("person: assign = %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
	})

	var projectID, taskID, foreignTaskID string
	if err := e.db.QueryRow(ctx, `INSERT INTO projects (club_id, name, created_by) VALUES ($1, 'Synthetic project', $2) RETURNING id`, e.clubID, e.adminID).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := e.db.QueryRow(ctx, `INSERT INTO tasks (project_id, club_id, title, created_by) VALUES ($1, $2, 'Synthetic task', $3) RETURNING id`, projectID, e.clubID, e.adminID).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	var foreignProject string
	if err := e.db.QueryRow(ctx, `INSERT INTO projects (club_id, name, created_by) VALUES ($1, 'Foreign project', $2) RETURNING id`, other, foreignUser).Scan(&foreignProject); err != nil {
		t.Fatalf("seed foreign project: %v", err)
	}
	if err := e.db.QueryRow(ctx, `INSERT INTO tasks (project_id, club_id, title, created_by) VALUES ($1, $2, 'Foreign task', $3) RETURNING id`, foreignProject, other, foreignUser).Scan(&foreignTaskID); err != nil {
		t.Fatalf("seed foreign task: %v", err)
	}

	t.Run("volunteer task assignment", func(t *testing.T) {
		h := NewVolunteerHandler(e.db, e.cfg, zerolog.Nop())
		for _, r := range refused {
			rec := e.do(t, http.MethodPost, "/tasks/{taskID}/assign", "/tasks/"+taskID+"/assign", h.HandleAssignTask,
				`{"user_id":"`+r.id+`","role":"responsible"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: assign = %d, want 404; body: %s", r.name, rec.Code, rec.Body.String())
			}
		}
		if n := count(`SELECT count(*) FROM task_participants WHERE task_id = $1`, taskID); n != 0 {
			t.Errorf("participants written for a refused user: %d", n)
		}
		if n := count(`SELECT count(*) FROM tasks WHERE id = $1 AND responsible_id IS NOT NULL`, taskID); n != 0 {
			t.Errorf("responsible_id set for a refused user")
		}

		// A task of another club is not reachable with a person of this club.
		rec := e.do(t, http.MethodPost, "/tasks/{taskID}/assign", "/tasks/"+foreignTaskID+"/assign", h.HandleAssignTask,
			`{"user_id":"`+person+`","role":"responsible"}`)
		assertStatus(t, rec, http.StatusNotFound)
		if n := count(`SELECT count(*) FROM task_participants WHERE task_id = $1`, foreignTaskID); n != 0 {
			t.Errorf("a participant was added to another club's task")
		}
		if n := count(`SELECT count(*) FROM tasks WHERE id = $1 AND responsible_id IS NOT NULL`, foreignTaskID); n != 0 {
			t.Errorf("another club's task got a responsible user")
		}

		assertStatus(t, e.do(t, http.MethodPost, "/tasks/{taskID}/assign", "/tasks/"+taskID+"/assign", h.HandleAssignTask,
			`{"user_id":"`+person+`","role":"responsible"}`), http.StatusOK)
	})

	t.Run("project task assignee", func(t *testing.T) {
		h := NewProjectsHandler(e.db, e.cfg, zerolog.Nop())
		before := count(`SELECT count(*) FROM tasks WHERE club_id = $1`, e.clubID)
		for _, r := range refused {
			rec := e.do(t, http.MethodPost, "/projects/{projectID}/tasks", "/projects/"+projectID+"/tasks", h.HandleCreateTask,
				`{"title":"New task","assignee_id":"`+r.id+`"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: create = %d, want 404; body: %s", r.name, rec.Code, rec.Body.String())
			}
			rec = e.do(t, http.MethodPatch, "/tasks/{taskID}", "/tasks/"+taskID, h.HandleUpdateTask, `{"assignee_id":"`+r.id+`"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: update = %d, want 404; body: %s", r.name, rec.Code, rec.Body.String())
			}
		}
		if n := count(`SELECT count(*) FROM tasks WHERE club_id = $1`, e.clubID); n != before {
			t.Errorf("tasks created for a refused assignee: %d -> %d", before, n)
		}
		if n := count(`SELECT count(*) FROM tasks WHERE id = $1 AND assignee_id IS NOT NULL`, taskID); n != 0 {
			t.Errorf("the task got a refused assignee")
		}
		assertStatus(t, e.do(t, http.MethodPost, "/projects/{projectID}/tasks", "/projects/"+projectID+"/tasks", h.HandleCreateTask,
			`{"title":"New task","assignee_id":"`+person+`"}`), http.StatusCreated)
		assertStatus(t, e.do(t, http.MethodPatch, "/tasks/{taskID}", "/tasks/"+taskID, h.HandleUpdateTask, `{"assignee_id":"`+person+`"}`), http.StatusOK)
	})
}
