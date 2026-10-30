package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/roles"
)

// directoryFixture seeds one synthetic member per call and returns the id.
type directoryFixture struct {
	t      *testing.T
	db     *pgxpool.Pool
	clubID string
}

func (f directoryFixture) member(first, last, email, phone string, hide bool, roleNames ...string) string {
	f.t.Helper()
	ctx := context.Background()
	var id string
	if err := f.db.QueryRow(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, phone, hide_in_directory)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		f.clubID, email, first, last, phone, hide,
	).Scan(&id); err != nil {
		f.t.Fatalf("seeding %s %s: %v", first, last, err)
	}
	for _, r := range roleNames {
		if _, err := f.db.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, $3::user_role)`,
			id, f.clubID, r,
		); err != nil {
			f.t.Fatalf("granting %s: %v", r, err)
		}
	}
	return id
}

type directoryRow struct {
	LastName string  `json:"last_name"`
	Email    *string `json:"email"`
	Phone    *string `json:"phone"`
}

func (e *integrationEnv) getDirectory(t *testing.T, callerRoles []string) map[string]directoryRow {
	t.Helper()
	h := e.membersHandler()
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Get("/directory", h.HandleGetDirectory)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/directory", nil)
	req.Header.Set("Authorization", "Bearer "+e.generateToken("00000000-0000-0000-0000-000000000001", callerRoles))
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var body struct {
		Items []directoryRow `json:"items"`
	}
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&body); err != nil {
		t.Fatalf("decoding directory: %v", err)
	}
	if strings.Contains(rec.Body.String(), "hide_in_directory") {
		t.Fatalf("directory response leaks the hide flag: %s", rec.Body.String())
	}
	out := map[string]directoryRow{}
	for _, it := range body.Items {
		if it.Email != nil && strings.HasSuffix(*it.Email, accounting.ErasedEmailSuffix) {
			t.Fatalf("erased placeholder address listed: %s", *it.Email)
		}
		out[it.LastName] = it
	}
	return out
}

func TestIntegration_DirectoryBoardException(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	f := directoryFixture{t: t, db: env.db, clubID: env.clubID}
	ctx := context.Background()

	// One hidden holder per board role, so a role missing from the board set
	// shows up as a missing row. They carry no plain member role: the board
	// role alone must get them listed.
	for i, role := range roles.Board {
		last := "Hidden" + role
		f.member("Syn", last, role+"-hidden@example.test", "+4790000"+string(rune('0'+i))+"00", true, role)
	}
	// Holds a plain member role as well as a board role: the board role must
	// still count when another listed role sits on the same user.
	f.member("Syn", "HiddenMemberAndBoard", "hmb@example.test", "+4798888888", true, "member", "vice_chair")
	f.member("Syn", "BoardVisible", "bv@example.test", "+4791111111", false, "member", "treasurer")
	f.member("Syn", "OrdinaryHidden", "oh@example.test", "+4792222222", true, "member")
	f.member("Syn", "OrdinaryHiddenSlip", "ohs@example.test", "+4792222223", true, "slip_holder")
	f.member("Syn", "OrdinaryVisible", "ov@example.test", "+4793333333", false, "member")
	f.member("Syn", "AdminHidden", "ah@example.test", "+4794444444", true, "admin")
	f.member("Syn", "ApplicantVisible", "ap@example.test", "+4795555555", false, "applicant")

	// Never listed: deactivated (even a board member), and an anonymised row
	// that is still active, which the erased-address guard must catch.
	deact := f.member("Syn", "DeactivatedBoard", "db@example.test", "+4796666666", false, "board")
	if _, err := env.db.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, deact); err != nil {
		t.Fatalf("deactivating: %v", err)
	}
	f.member("Slettet", "ErasedBoard", "00000000-aaaa"+accounting.ErasedEmailSuffix, "", false, "board")
	f.member("Slettet", "ErasedHiddenBoard", "00000000-bbbb"+accounting.ErasedEmailSuffix, "", true, "chair")

	got := env.getDirectory(t, []string{"member"})

	for _, role := range roles.Board {
		row, ok := got["Hidden"+role]
		if !ok {
			t.Errorf("hidden %s is not listed", role)
			continue
		}
		if row.Email == nil || *row.Email != role+"-hidden@example.test" {
			t.Errorf("hidden %s: email = %v, want it shown", role, row.Email)
		}
		if row.Phone != nil {
			t.Errorf("hidden %s: phone %q shown, want it to follow the setting", role, *row.Phone)
		}
	}

	if row, ok := got["HiddenMemberAndBoard"]; !ok || row.Email == nil || row.Phone != nil {
		t.Errorf("hidden member who also holds a board role should show email and no phone, got %+v (listed %v)", row, ok)
	}
	if row, ok := got["BoardVisible"]; !ok || row.Phone == nil || *row.Phone != "+4791111111" || row.Email == nil {
		t.Errorf("visible board member should show phone and email, got %+v (listed %v)", row, ok)
	}
	if row, ok := got["OrdinaryVisible"]; !ok || row.Phone == nil || row.Email == nil {
		t.Errorf("ordinary visible member should show phone and email, got %+v (listed %v)", row, ok)
	}
	for _, name := range []string{"OrdinaryHidden", "OrdinaryHiddenSlip", "AdminHidden", "ApplicantVisible", "DeactivatedBoard", "ErasedBoard", "ErasedHiddenBoard"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s must not be listed", name)
		}
	}

	// A former board member is hidden again once the role is removed.
	if _, err := env.db.Exec(ctx,
		`DELETE FROM user_roles WHERE user_id = (SELECT id FROM users WHERE last_name = 'Hiddenchair' AND club_id = $1)`,
		env.clubID,
	); err != nil {
		t.Fatalf("removing role: %v", err)
	}
	if _, err := env.db.Exec(ctx,
		`INSERT INTO user_roles (user_id, club_id, role)
		 SELECT id, club_id, 'member' FROM users WHERE last_name = 'Hiddenchair' AND club_id = $1`,
		env.clubID,
	); err != nil {
		t.Fatalf("regranting member: %v", err)
	}
	got = env.getDirectory(t, []string{"member"})
	if _, ok := got["Hiddenchair"]; ok {
		t.Errorf("former chair who hides is still listed")
	}
	if _, ok := got["Hiddenvice_chair"]; !ok {
		t.Errorf("current vice chair dropped out")
	}
}

// The board exception is about reaching the board, not about who sits in a
// slip: a board member who hides stays off the harbor map for other members.
func TestIntegration_HarborMapHiddenBoardOccupantStaysHidden(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	f := directoryFixture{t: t, db: env.db, clubID: env.clubID}
	ctx := context.Background()

	occupy := func(number, last string, hide bool, role string) {
		uid := f.member("Syn", last, last+"@example.test", "+4797000000", hide, role)
		var slipID, boatID string
		if err := env.db.QueryRow(ctx,
			`INSERT INTO slips (club_id, number, section) VALUES ($1, $2, 'A') RETURNING id`,
			env.clubID, number,
		).Scan(&slipID); err != nil {
			t.Fatalf("seeding slip: %v", err)
		}
		if err := env.db.QueryRow(ctx,
			`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`,
			uid, env.clubID,
		).Scan(&boatID); err != nil {
			t.Fatalf("seeding boat: %v", err)
		}
		if _, err := env.db.Exec(ctx,
			`INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id) VALUES ($1, $2, $3, $4)`,
			slipID, uid, env.clubID, boatID,
		); err != nil {
			t.Fatalf("seeding assignment: %v", err)
		}
	}
	occupy("1", "HiddenBoard", true, "treasurer")
	occupy("2", "VisibleBoard", false, "treasurer")
	occupy("3", "HiddenOrdinary", true, "member")
	occupy("4", "VisibleOrdinary", false, "member")

	h := NewHarborHandler(env.db, env.cfg, env.log)
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Get("/layout", h.HandleGetLayout)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/layout", nil)
	req.Header.Set("Authorization", "Bearer "+env.generateToken("00000000-0000-0000-0000-000000000001", []string{"member"}))
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var body featureCollection
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding layout: %v", err)
	}
	if body.Mode != "member" {
		t.Fatalf("mode = %q, want member", body.Mode)
	}
	occupant := map[string]any{}
	for _, ft := range body.Features {
		if ft.Properties["kind"] == "slip" {
			occupant[ft.Properties["number"].(string)] = ft.Properties["occupant_last_name"]
		}
	}
	want := map[string]any{"1": nil, "2": "VisibleBoard", "3": nil, "4": "VisibleOrdinary"}
	for num, w := range want {
		if occupant[num] != w {
			t.Errorf("slip %s occupant = %v, want %v", num, occupant[num], w)
		}
	}
}
