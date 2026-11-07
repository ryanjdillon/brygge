package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func TestLocalYearStart(t *testing.T) {
	now := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.Local)
	if got := localYearStart(now); got != "2026-01-01" {
		t.Errorf("localYearStart = %q, want 2026-01-01", got)
	}

	// 00:30 on 1 January in a zone an hour ahead of UTC is still 31 December
	// in UTC: the year has turned locally and not in UTC.
	cet := time.FixedZone("UTC+1", 3600)
	instant := time.Date(2027, time.January, 1, 0, 30, 0, 0, cet)
	if got := yearStartIn(instant, cet); got != "2027-01-01" {
		t.Errorf("local year start = %q, want 2027-01-01", got)
	}
	if got := yearStartIn(instant, time.UTC); got != "2026-01-01" {
		t.Errorf("UTC year start = %q, want 2026-01-01", got)
	}
}

// The join price item setting takes only an active, flat price item of the
// caller's own club; "" closes joining, an omitted field keeps it, and the
// settings read reports the join status for the caller's club.
func TestFinancialSettingsJoinPriceItem(t *testing.T) { //nolint:cyclop // one scenario walked through every refusal and the accepted values
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	other := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}

	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, req)
		return rec
	}
	get := func() clubFinancialSettings {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleGetFinancialSettings(rec, req)
		var s clubFinancialSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("get settings: %v (%s)", err, rec.Body)
		}
		return s
	}
	column := func() *string {
		t.Helper()
		var v *string
		if err := pool.QueryRow(ctx,
			`SELECT join_price_item_id::text FROM clubs WHERE id = $1`, club.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	insertItem := func(clubID, kind string, active bool) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, pricing_kind, tier_dimension)
			 VALUES ($1, 'membership', 'Synthetic join fee', 500, 'once', $2, $3,
			         CASE WHEN $3 = 'tiered' THEN 'length' END) RETURNING id`,
			clubID, active, kind,
		).Scan(&id); err != nil {
			t.Fatalf("seed price item: %v", err)
		}
		return id
	}

	good := insertItem(club.ID, "flat", true)
	inactive := insertItem(club.ID, "flat", false)
	tiered := insertItem(club.ID, "tiered", true)
	foreign := insertItem(other.ID, "flat", true)

	s := get()
	if s.JoinPriceItemID != "" || s.JoinOpen || !slices.Contains(s.JoinMissing, joinMissingPriceItem) {
		t.Errorf("default = %q open=%v missing=%v, want unset and closed on price_item_unset",
			s.JoinPriceItemID, s.JoinOpen, s.JoinMissing)
	}

	for name, id := range map[string]string{
		"another club's item": foreign,
		"inactive item":       inactive,
		"tiered item":         tiered,
		"not a uuid":          "membership",
		"unknown id":          "00000000-0000-4000-8000-000000000001",
	} {
		rec := update(`{"join_price_item_id":"` + id + `"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
			continue
		}
		var refusal struct{ Code string }
		if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || refusal.Code != codeInvalidJoinPriceItem {
			t.Errorf("%s: code %q, want %q", name, refusal.Code, codeInvalidJoinPriceItem)
		}
		if column() != nil {
			t.Fatalf("%s: a refused item was stored", name)
		}
	}

	if rec := update(`{"join_price_item_id":" ` + good + ` "}`); rec.Code != http.StatusOK {
		t.Fatalf("valid item: %d %s", rec.Code, rec.Body)
	}
	if v := column(); v == nil || *v != good {
		t.Fatalf("stored = %v, want %s", v, good)
	}
	s = get()
	if s.JoinPriceItemID != good {
		t.Errorf("read back %q, want %s", s.JoinPriceItemID, good)
	}
	// The price is set; the legal documents are not published yet.
	if s.JoinOpen || slices.Contains(s.JoinMissing, joinMissingPriceItem) || !slices.Contains(s.JoinMissing, "privacy") {
		t.Errorf("after setting the price: open=%v missing=%v, want closed on the documents only", s.JoinOpen, s.JoinMissing)
	}
	for _, doc := range joinRequiredDocs {
		seedLegalDoc(t, pool, club.ID, doc, doc+"-v1", "1 day")
	}
	if s = get(); !s.JoinOpen || len(s.JoinMissing) != 0 {
		t.Errorf("all set: open=%v missing=%v, want open", s.JoinOpen, s.JoinMissing)
	}

	if rec := update(`{"bank_auto_assign":false}`); rec.Code != http.StatusOK {
		t.Fatalf("other field: %d %s", rec.Code, rec.Body)
	}
	if v := column(); v == nil || *v != good {
		t.Errorf("an omitted field changed the item to %v", v)
	}

	if rec := update(`{"join_price_item_id":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if column() != nil {
		t.Error("empty did not close joining")
	}

	// Each accepted write is audited by field name, the settings convention,
	// and, as the one exception, with the join price item's old and new id.
	rows, err := pool.Query(ctx,
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at`,
		club.ID, audit.ActionFinancialSettingsUpdated)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type change struct{ Old, New string }
	var changes []change
	for rows.Next() {
		var d struct {
			Fields          []string
			JoinPriceItemID *change `json:"join_price_item_id"`
		}
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		named := slices.Contains(d.Fields, "join_price_item_id")
		if named != (d.JoinPriceItemID != nil) {
			t.Errorf("audit row names the field %v but carries values %v: %s", named, d.JoinPriceItemID, raw)
		}
		if d.JoinPriceItemID != nil {
			changes = append(changes, *d.JoinPriceItemID)
		}
	}
	want := []change{{Old: "", New: good}, {Old: good, New: ""}}
	if !slices.Equal(changes, want) {
		t.Errorf("join price audit values = %+v, want %+v", changes, want)
	}
}

// ?status=applicant lists the caller's club's active applicants, and
// ?status=new_this_year its active members granted membership this year.
func TestListUsersJoinFilters(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	other := fin.NewClub(t, pool)
	h := NewAdminUsersHandler(pool, &config.Config{}, zerolog.Nop())
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"board"}}

	thisYear := localYearStart(time.Now())
	lastYear := time.Now().In(time.Local).AddDate(-1, 0, 0).Format("2006") + "-06-30"

	applicant := club.NewMember(t, "applicant").ID
	goneApplicant := club.NewMember(t, "applicant").ID
	mustExec(t, pool, ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, goneApplicant)
	newMember := club.NewMember(t, "member").ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = $2::date WHERE id = $1`, newMember, thisYear)
	oldMember := club.NewMember(t, "member").ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = $2::date WHERE id = $1`, oldMember, lastYear)
	newButGone := club.NewMember(t, "member").ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = $2::date, deactivated_at = now() WHERE id = $1`, newButGone, thisYear)
	otherApplicant := other.NewMember(t, "applicant").ID
	otherNew := other.NewMember(t, "member").ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = $2::date WHERE id = $1`, otherNew, thisYear)

	type row struct {
		ID          string  `json:"id"`
		MemberSince *string `json:"member_since"`
	}
	list := func(status string) []row {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/users?status="+status, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleListUsers(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", status, rec.Code, rec.Body)
		}
		var body struct {
			Users      []row `json:"users"`
			TotalCount int   `json:"total_count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.TotalCount != len(body.Users) {
			t.Errorf("%s: total_count %d, listed %d", status, body.TotalCount, len(body.Users))
		}
		return body.Users
	}
	ids := func(rs []row) []string {
		out := []string{}
		for _, r := range rs {
			out = append(out, r.ID)
		}
		slices.Sort(out)
		return out
	}

	if got := ids(list("applicant")); !slices.Equal(got, []string{applicant}) {
		t.Errorf("applicant = %v, want only %s", got, applicant)
	}
	got := list("new_this_year")
	if !slices.Equal(ids(got), []string{newMember}) {
		t.Errorf("new_this_year = %v, want only %s", ids(got), newMember)
	}
	if len(got) == 1 && (got[0].MemberSince == nil || *got[0].MemberSince != thisYear) {
		t.Errorf("member_since = %v, want %s", got[0].MemberSince, thisYear)
	}
	for _, id := range ids(list("all")) {
		if id == otherApplicant || id == otherNew {
			t.Errorf("another club's member %s is listed", id)
		}
	}

	// The status filter ANDs with the search and the dock filter.
	newNamed := club.NewMember(t, "member").ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = $2::date, first_name = 'Zyntheticus' WHERE id = $1`, newNamed, thisYear)
	mustExec(t, pool, ctx, `UPDATE users SET first_name = 'Zyntheticus' WHERE id = $1`, oldMember)
	if got := ids(list("new_this_year&q=zynthet")); !slices.Equal(got, []string{newNamed}) {
		t.Errorf("new_this_year + q = %v, want only %s", got, newNamed)
	}
	var boatID, slipID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`,
		newMember, club.ID).Scan(&boatID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO slips (club_id, number, section) VALUES ($1, '3', 'Q') RETURNING id`, club.ID).Scan(&slipID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, boat_id, club_id, assignment_type)
		 VALUES ($1, $2, $3, $4, 'permanent')`, slipID, newMember, boatID, club.ID)
	if got := ids(list("new_this_year&dock=Q")); !slices.Equal(got, []string{newMember}) {
		t.Errorf("new_this_year + dock = %v, want only %s", got, newMember)
	}
	if got := ids(list("applicant&dock=Q")); len(got) != 0 {
		t.Errorf("applicant + dock = %v, want none", got)
	}
}

// The board's own grants of the member role (create, the roles editor and
// the CSV import) record member_since as the self-service grant does; an
// existing date and a member who already held the role are left alone.
func TestAdminMemberGrantSetsMemberSince(t *testing.T) { //nolint:cyclop // one scenario per grant path
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	h := NewAdminUsersHandler(pool, &config.Config{}, zerolog.Nop())
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"admin"}}
	today := localToday(time.Now())

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithClaims(req.Context(), claims)))
		})
	})
	r.Post("/users", h.HandleCreateUser)
	r.Put("/users/{userID}/roles", h.HandleUpdateUserRoles)
	r.Post("/users/import", h.HandleImportUsersCSV)
	call := func(method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	since := func(id string) string {
		t.Helper()
		var v *string
		if err := pool.QueryRow(ctx,
			`SELECT to_char(member_since, 'YYYY-MM-DD') FROM users WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v == nil {
			return ""
		}
		return *v
	}
	addr := func(tag string) string { return "synthetic-" + tag + "-" + testutil.RandomHex(4) + "@example.com" }

	create := func(roles string) string {
		t.Helper()
		rec := call(http.MethodPost, "/users", "application/json",
			strings.NewReader(`{"email":"`+addr("create")+`","first_name":"Synthetic","roles":`+roles+`}`))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		var out struct{ ID string }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.ID
	}
	if id := create(`["member"]`); since(id) != today {
		t.Errorf("created member: member_since = %q, want %s", since(id), today)
	}
	nonMember := create(`["board"]`)
	if since(nonMember) != "" {
		t.Errorf("created without member: member_since = %q, want none", since(nonMember))
	}

	setRoles := func(id, roles string) {
		t.Helper()
		if rec := call(http.MethodPut, "/users/"+id+"/roles", "application/json",
			strings.NewReader(`{"roles":`+roles+`}`)); rec.Code != http.StatusOK {
			t.Fatalf("roles: %d %s", rec.Code, rec.Body)
		}
	}
	setRoles(nonMember, `["board","member"]`)
	if since(nonMember) != today {
		t.Errorf("member granted by roles: member_since = %q, want %s", since(nonMember), today)
	}
	legacy := club.NewMember(t, "member").ID
	setRoles(legacy, `["member","board"]`)
	if since(legacy) != "" {
		t.Errorf("legacy member's other roles edited: member_since = %q, want none", since(legacy))
	}
	rejoined := club.NewMember(t).ID
	mustExec(t, pool, ctx, `UPDATE users SET member_since = '2015-04-01' WHERE id = $1`, rejoined)
	setRoles(rejoined, `["member"]`)
	if since(rejoined) != "2015-04-01" {
		t.Errorf("stored date overwritten: member_since = %q", since(rejoined))
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "members.csv")
	if err != nil {
		t.Fatal(err)
	}
	memberAddr, boardAddr := addr("csv-member"), addr("csv-board")
	_, _ = io.WriteString(fw, "email,first_name,roles\n"+memberAddr+",Synthetic,member;board\n"+boardAddr+",Synthetic,board\n")
	_ = mw.Close()
	if rec := call(http.MethodPost, "/users/import", mw.FormDataContentType(), &buf); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	byAddr := func(email string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE club_id = $1 AND email = $2`, club.ID, email).Scan(&id); err != nil {
			t.Fatalf("imported %s: %v", email, err)
		}
		return id
	}
	if got := since(byAddr(memberAddr)); got != today {
		t.Errorf("imported member: member_since = %q, want %s", got, today)
	}
	if got := since(byAddr(boardAddr)); got != "" {
		t.Errorf("imported non-member: member_since = %q, want none", got)
	}
}
