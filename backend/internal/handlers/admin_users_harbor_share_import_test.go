package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestReadCSVUpload(t *testing.T) {
	t.Parallel()

	t.Run("BOM is stripped before the header is read", func(t *testing.T) {
		u, err := readCSVUpload(strings.NewReader("\xEF\xBB\xBFemail,name\na@example.com,A\n"))
		if err != nil {
			t.Fatalf("readCSVUpload: %v", err)
		}
		if !u.has("email") {
			t.Errorf("email column not found; cols = %v", u.cols)
		}
	})

	t.Run("semicolon delimiter is sniffed from the header", func(t *testing.T) {
		u, err := readCSVUpload(strings.NewReader("email;name;note\na@example.com;Ada Synthetic;1,5\n"))
		if err != nil {
			t.Fatalf("readCSVUpload: %v", err)
		}
		rec, err := u.Read()
		if err != nil {
			t.Fatalf("read row: %v", err)
		}
		if got := u.get(rec, "name"); got != "Ada Synthetic" {
			t.Errorf("name = %q", got)
		}
		if got := u.get(rec, "note"); got != "1,5" {
			t.Errorf("note = %q, want the comma kept inside the field", got)
		}
	})

	t.Run("Windows-1252 is rejected", func(t *testing.T) {
		// "Bjørn" with ø as the single cp1252 byte 0xF8.
		_, err := readCSVUpload(strings.NewReader("name\nBj\xF8rn\n"))
		if !errors.Is(err, errCSVNotUTF8) {
			t.Errorf("err = %v, want errCSVNotUTF8", err)
		}
	})

	t.Run("delimiters inside quotes do not vote", func(t *testing.T) {
		u, err := readCSVUpload(strings.NewReader("\"Navn, fullt, med komma\";email;note\nAda Synthetic;a@example.com;x\n"))
		if err != nil {
			t.Fatalf("readCSVUpload: %v", err)
		}
		if u.Comma != ';' {
			t.Fatalf("delimiter = %q, want ';'", u.Comma)
		}
		if !u.has("navn, fullt, med komma") || !u.has("email") {
			t.Errorf("cols = %v", u.cols)
		}
	})

	t.Run("a quoted newline does not end the header", func(t *testing.T) {
		u, err := readCSVUpload(strings.NewReader("\"a,\nb,c\";email;name\nx;a@example.com;A\n"))
		if err != nil {
			t.Fatalf("readCSVUpload: %v", err)
		}
		if u.Comma != ';' || !u.has("name") {
			t.Errorf("delimiter = %q, cols = %v", u.Comma, u.cols)
		}
	})

	t.Run("lone CR line endings", func(t *testing.T) {
		u, err := readCSVUpload(strings.NewReader("email,name\ra@example.com,Ada\rb@example.com,Bo\r"))
		if err != nil {
			t.Fatalf("readCSVUpload: %v", err)
		}
		var names []string
		for {
			rec, err := u.Read()
			if err != nil {
				break
			}
			names = append(names, u.get(rec, "name"))
		}
		if strings.Join(names, "|") != "Ada|Bo" {
			t.Errorf("rows = %q, want two rows", names)
		}
	})

	t.Run("empty upload", func(t *testing.T) {
		if _, err := readCSVUpload(strings.NewReader("")); !errors.Is(err, errCSVNoHeader) {
			t.Errorf("err = %v, want errCSVNoHeader", err)
		}
	})
}

func TestHarborShareParsers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"2026-03-15": "2026-03-15",
		"15.03.2026": "2026-03-15",
		"5.3.2026":   "2026-03-05",
	} {
		got, ok := parseHarborSharePaidOn(in)
		if !ok || got == nil || *got != want {
			t.Errorf("parseHarborSharePaidOn(%q) = %v, %v; want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"2026-13-01", "15/03/2026", "soon"} {
		if _, ok := parseHarborSharePaidOn(in); ok {
			t.Errorf("parseHarborSharePaidOn(%q) accepted", in)
		}
	}
	for in, want := range map[string]bool{"Ja": true, "x": true, "1": true, "NEI": false, "": false, "no": false} {
		got, ok := parseHarborShareOwner(in)
		if !ok || got != want {
			t.Errorf("parseHarborShareOwner(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := parseHarborShareOwner("maybe"); ok {
		t.Error("parseHarborShareOwner accepted maybe")
	}
	if a, b := normaliseMemberName("  BJØRN   Øvre "), normaliseMemberName("bjørn øvre"); a != b {
		t.Errorf("normalised names differ: %q vs %q", a, b)
	}
	// å as a + combining ring (as macOS may write it) matches the composed form.
	if a, b := normaliseMemberName("Åsa Synthetic"), normaliseMemberName("Åsa synthetic"); a != b {
		t.Errorf("NFC: %q vs %q", a, b)
	}
}

func readHarborShareCSV(t *testing.T, csv string) ([]harborShareInput, error) {
	t.Helper()
	u, err := readCSVUpload(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("readCSVUpload: %v", err)
	}
	return readHarborShareInputs(u)
}

func TestReadHarborShareInputs(t *testing.T) {
	t.Parallel()

	t.Run("a date on a row that is not an owner is an error", func(t *testing.T) {
		inputs, err := readHarborShareCSV(t, "name,harbor_share_owner,harbor_share_paid_on\nAda Synthetic,nei,2026-03-15\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(inputs) != 1 || inputs[0].err == "" {
			t.Fatalf("inputs = %+v, want one error row", inputs)
		}
		plan, err := planHarborShareImport(inputs, nil, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if plan.rows[0].Outcome != harborShareError {
			t.Errorf("outcome = %q, want error", plan.rows[0].Outcome)
		}
	})

	t.Run("the row cap", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("name,harbor_share_owner\n")
		for range harborShareMaxRows {
			b.WriteString("Ada Synthetic,ja\n")
		}
		if _, err := readHarborShareCSV(t, b.String()); err != nil {
			t.Fatalf("%d rows refused: %v", harborShareMaxRows, err)
		}
		b.WriteString("Ada Synthetic,ja\n")
		if _, err := readHarborShareCSV(t, b.String()); err == nil || !strings.Contains(err.Error(), "5000") {
			t.Errorf("%d rows: err = %v, want the cap named", harborShareMaxRows+1, err)
		}
	})
}

func TestPlanHarborShareImport(t *testing.T) {
	t.Parallel()
	member := func(id, email, name string) harborShareMember {
		return harborShareMember{id: id, email: email, name: name, nameKey: normaliseMemberName(name)}
	}
	members := []harborShareMember{
		member("u-ada", "ada@example.com", "Ada Synthetic"),
		member("u-bo1", "Shared@example.com", "Bo Synthetic"),
		member("u-bo2", "shared@example.com", "Bo Annen"),
		member("u-ola1", "ola1@example.com", "Ola Nordmann"),
		member("u-ola2", "ola2@example.com", "Ola Nordmann"),
	}
	owner := func(row int, email, name string) harborShareInput {
		return harborShareInput{row: row, email: email, name: name, owner: true}
	}
	isResolutionErr := func(err error) bool {
		var re *errHarborShareResolution
		return errors.As(err, &re)
	}

	t.Run("an email two members share is ambiguous between them", func(t *testing.T) {
		plan, err := planHarborShareImport([]harborShareInput{owner(2, "SHARED@example.com", "Bo Synthetic")}, members, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		r := plan.rows[0]
		if r.Outcome != harborShareAmbiguous || len(r.Candidates) != 2 {
			t.Fatalf("row = %+v, want ambiguous with two candidates", r)
		}
		got := map[string]bool{r.Candidates[0].UserID: true, r.Candidates[1].UserID: true}
		if !got["u-bo1"] || !got["u-bo2"] {
			t.Errorf("candidates = %+v, want the two members sharing the email", r.Candidates)
		}
		plan, err = planHarborShareImport([]harborShareInput{owner(2, "shared@example.com", "")}, members, map[int]string{2: "u-bo2"}, false)
		if err != nil || plan.rows[0].UserID != "u-bo2" || len(plan.writes) != 1 {
			t.Errorf("resolved shared email = %+v, %v", plan, err)
		}
	})

	t.Run("a pick for a row that matches by itself", func(t *testing.T) {
		inputs := []harborShareInput{owner(2, "", "Ada Synthetic")}
		picks := map[int]string{2: "u-ola1"}
		plan, err := planHarborShareImport(inputs, members, picks, true)
		if err != nil {
			t.Fatalf("dry run: %v", err)
		}
		r := plan.rows[0]
		if !r.ResolutionDropped || r.Resolved || r.UserID != "u-ada" || r.Message != msgHarborSharePickDropped {
			t.Errorf("dry-run row = %+v, want the pick dropped and the automatic match kept", r)
		}
		without, err := planHarborShareImport(inputs, members, nil, true)
		if err != nil || without.token != plan.token {
			t.Errorf("dropping the pick changes the plan token (%v)", err)
		}
		if _, err := planHarborShareImport(inputs, members, picks, false); !isResolutionErr(err) {
			t.Errorf("apply err = %v, want a resolution error", err)
		}
	})

	t.Run("a pick for an error row", func(t *testing.T) {
		inputs := []harborShareInput{{row: 2, name: "Ada Synthetic", err: "bad date"}}
		for _, dryRun := range []bool{true, false} {
			if _, err := planHarborShareImport(inputs, members, map[int]string{2: "u-ada"}, dryRun); !isResolutionErr(err) {
				t.Errorf("dry_run=%v: err = %v, want a resolution error", dryRun, err)
			}
		}
	})

	t.Run("rows that agree on a member write once", func(t *testing.T) {
		plan, err := planHarborShareImport([]harborShareInput{
			owner(2, "ada@example.com", ""), owner(3, "", "Ada Synthetic"),
		}, members, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.writes) != 1 || plan.rows[0].Outcome != harborShareSet || plan.rows[1].Outcome != harborShareDuplicate {
			t.Errorf("writes = %+v, rows = %+v", plan.writes, plan.rows)
		}
	})

	t.Run("a pick that names a member another row already sets", func(t *testing.T) {
		paid := "2026-03-15"
		ada := owner(2, "ada@example.com", "")
		ada.paidOn = &paid
		plan, err := planHarborShareImport([]harborShareInput{ada, owner(3, "", "Ola Nordmann")}, members,
			map[int]string{3: "u-ada"}, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range plan.rows {
			if r.Outcome != harborShareDuplicate {
				t.Errorf("row %d outcome = %q, want duplicate", r.Row, r.Outcome)
			}
		}
		if len(plan.writes) != 0 {
			t.Errorf("writes = %+v, want none for contradicting rows", plan.writes)
		}
	})
}

// harborShareImportEnv is a club with one synthetic member per import outcome.
type harborShareImportEnv struct {
	*systemActorEnv
	h       *AdminUsersHandler
	ids     map[string]string
	otherID string // a member of another club
}

func newHarborShareImportEnv(t *testing.T) *harborShareImportEnv {
	t.Helper()
	e := &harborShareImportEnv{systemActorEnv: newSystemActorEnv(t), ids: map[string]string{}}
	e.h = NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())
	ctx := context.Background()

	add := func(key, first, last, clubID string) string {
		id, _ := testutil.SeedUser(t, e.db, clubID, []string{"member"})
		mustExec(t, e.db, ctx, `UPDATE users SET first_name = $2, last_name = $3, email = $4 WHERE id = $1`,
			id, first, last, key+"-"+testutil.RandomHex(3)+"@example.com")
		e.ids[key] = id
		return id
	}
	add("alice", "Alice", "Synthetic", e.clubID)
	add("bjorn", "Bjørn", "Øvre", e.clubID)
	add("carl", "Carl", "Synthetic", e.clubID)
	add("dina", "Dina", "Synthetic", e.clubID)
	add("erik", "Erik", "Synthetic", e.clubID)
	add("frida", "Frida", "Synthetic", e.clubID)
	add("gina", "Gina", "Synthetic", e.clubID)
	add("harald", "Harald", "Synthetic", e.clubID)
	add("kari", "Kari", "Synthetic", e.clubID)
	add("ola1", "Ola", "Nordmann", e.clubID)
	add("ola2", "Ola", "Nordmann", e.clubID)
	add("ivar", "Ivar", "Erased", e.clubID)
	otherClub := testutil.SeedClub(t, e.db)
	e.otherID = add("jon", "Jon", "Annenklubb", otherClub)

	mustExec(t, e.db, ctx, `UPDATE users SET harbor_share_owner = true WHERE id = ANY($1)`,
		[]string{e.ids["bjorn"], e.ids["erik"]})
	mustExec(t, e.db, ctx, `UPDATE users SET harbor_share_owner = true, harbor_share_paid_on = '2020-01-01' WHERE id = ANY($1)`,
		[]string{e.ids["carl"], e.ids["dina"]})
	mustExec(t, e.db, ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, e.ids["harald"])
	// An erased member keeps a name a sheet could still carry; the import
	// must not find them by it.
	mustExec(t, e.db, ctx, `UPDATE users SET email = gen_random_uuid()::text || $2 WHERE id = $1`,
		e.ids["ivar"], accounting.ErasedEmailSuffix)

	var boatID, slipID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`,
		e.ids["ola1"], e.clubID).Scan(&boatID); err != nil {
		t.Fatalf("seed boat: %v", err)
	}
	if err := e.db.QueryRow(ctx,
		`INSERT INTO slips (club_id, number, section) VALUES ($1, '10', 'A') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
		t.Fatalf("seed slip: %v", err)
	}
	mustExec(t, e.db, ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, boat_id, club_id, assignment_type)
		 VALUES ($1, $2, $3, $4, 'permanent')`, slipID, e.ids["ola1"], boatID, e.clubID)
	return e
}

func (e *harborShareImportEnv) email(t *testing.T, key string) string {
	t.Helper()
	var email string
	if err := e.db.QueryRow(context.Background(), `SELECT email FROM users WHERE id = $1`, e.ids[key]).Scan(&email); err != nil {
		t.Fatalf("email of %s: %v", key, err)
	}
	return email
}

// sheet is the club's member sheet in Excel's "CSV UTF-8" shape: a BOM,
// ';' between columns and Norwegian dates. Row numbers count the header as 1.
func (e *harborShareImportEnv) sheet(t *testing.T) string {
	t.Helper()
	lines := []string{
		"\xEF\xBB\xBFEmail;Name;harbor_share_owner;harbor_share_paid_on", // 1
		e.email(t, "alice") + ";;ja;15.03.2026",                          //  2 set (by email)
		";  bjørn   ØVRE ;ja;2026-04-01",                                 //  3 date_added (by name)
		";Carl Synthetic;ja;01.01.2020",                                  //  4 unchanged
		";Dina Synthetic;ja;2021-05-05",                                  //  5 conflict
		";Erik Synthetic;nei;",                                           //  6 not_owner_but_set
		";Frida Synthetic;no;",                                           //  7 skipped
		";Gina Synthetic;ja;",                                            //  8 duplicate
		";gina synthetic;ja;2026-01-01",                                  //  9 duplicate (disagrees with 8)
		";Harald Synthetic;x;",                                           // 10 set, deactivated
		e.email(t, "kari") + ";Frida Synthetic;ja;",                      // 11 set: email beats name
		";Ola Nordmann;ja;",                                              // 12 ambiguous
		";Nobody Synthetic;ja;",                                          // 13 unmatched
		";Ivar Erased;ja;",                                               // 14 unmatched: erased
		";Jon Annenklubb;ja;",                                            // 15 unmatched: other club
		";Carl Synthetic;ja;31.02.2026",                                  // 16 error: bad date
		";;;",                                                            // blank, dropped
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

type harborShareImportResp struct {
	DryRun    bool                   `json:"dry_run"`
	Counts    map[string]int         `json:"counts"`
	Rows      []harborShareImportRow `json:"rows"`
	PlanToken string                 `json:"plan_token"`
	Code      string                 `json:"code"`
	Error     string                 `json:"error"`
}

func (e *harborShareImportEnv) post(t *testing.T, csv []byte, fields map[string]string) (int, harborShareImportResp) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "moloandel.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(csv)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()

	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/import", e.h.HandleImportHarborShares)
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", authHeader(generateTestToken(e.adminID, e.clubID, []string{"admin"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp harborShareImportResp
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// state reads every seeded member's harbor share fields.
func (e *harborShareImportEnv) state(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for key, id := range e.ids {
		var owner bool
		var paidOn *string
		if err := e.db.QueryRow(context.Background(),
			`SELECT harbor_share_owner, to_char(harbor_share_paid_on, 'YYYY-MM-DD') FROM users WHERE id = $1`, id,
		).Scan(&owner, &paidOn); err != nil {
			t.Fatalf("state of %s: %v", key, err)
		}
		s := "no"
		if owner {
			s = "yes"
		}
		if paidOn != nil {
			s += " " + *paidOn
		}
		out[key] = s
	}
	return out
}

func (e *harborShareImportEnv) importAuditRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND action IN ($2, $3)`,
		e.clubID, audit.ActionUserHarborShareSet, audit.ActionUserHarborSharesImported,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func rowByNumber(t *testing.T, resp harborShareImportResp, n int) harborShareImportRow {
	t.Helper()
	for _, r := range resp.Rows {
		if r.Row == n {
			return r
		}
	}
	t.Fatalf("no row %d in %+v", n, resp.Rows)
	return harborShareImportRow{}
}

func TestIntegration_HarborShareImport(t *testing.T) { //nolint:gocognit,cyclop // one fixture walked through dry run, resolution, apply and races
	t.Parallel()

	wantOutcomes := map[int]string{
		2: harborShareSet, 3: harborShareDateAdded, 4: harborShareUnchanged, 5: harborShareConflict,
		6: harborShareNotOwnerBut, 7: harborShareSkipped, 8: harborShareDuplicate, 9: harborShareDuplicate,
		10: harborShareSet, 11: harborShareSet, 12: harborShareAmbiguous, 13: harborShareUnmatched,
		14: harborShareUnmatched, 15: harborShareUnmatched, 16: harborShareError,
	}

	t.Run("dry run reports every outcome and writes nothing", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		before := e.state(t)
		code, resp := e.post(t, []byte(e.sheet(t)), nil)
		if code != http.StatusOK {
			t.Fatalf("dry run = %d: %+v", code, resp)
		}
		if !resp.DryRun || resp.PlanToken == "" {
			t.Errorf("dry_run = %v, plan_token = %q", resp.DryRun, resp.PlanToken)
		}
		if len(resp.Rows) != len(wantOutcomes) {
			t.Errorf("rows = %d, want %d (blank row dropped)", len(resp.Rows), len(wantOutcomes))
		}
		for n, want := range wantOutcomes {
			if got := rowByNumber(t, resp, n); got.Outcome != want {
				t.Errorf("row %d outcome = %q (%s), want %q", n, got.Outcome, got.Message, want)
			}
		}
		if r := rowByNumber(t, resp, 2); r.UserID != e.ids["alice"] || r.Proposed == nil || *r.Proposed.PaidOn != "2026-03-15" {
			t.Errorf("row 2 = %+v", r)
		}
		if r := rowByNumber(t, resp, 10); !r.Deactivated {
			t.Errorf("row 10 deactivated = false")
		}
		if r := rowByNumber(t, resp, 11); r.UserID != e.ids["kari"] || r.Name != "Frida Synthetic" {
			t.Errorf("row 11 = %+v, want kari matched by email, uploaded name echoed", r)
		}
		amb := rowByNumber(t, resp, 12)
		if len(amb.Candidates) != 2 {
			t.Fatalf("row 12 candidates = %+v", amb.Candidates)
		}
		for _, c := range amb.Candidates {
			if c.UserID == e.ids["ola1"] && (len(c.Slips) != 1 || c.Slips[0] != "A10") {
				t.Errorf("ola1 slips = %v, want [A10]", c.Slips)
			}
		}
		if resp.Counts[harborShareSet] != 3 || resp.Counts[harborShareUnmatched] != 3 || resp.Counts[harborShareDuplicate] != 2 {
			t.Errorf("counts = %v", resp.Counts)
		}
		for _, r := range resp.Rows {
			if r.Email != "" && r.Row != 2 && r.Row != 11 {
				t.Errorf("row %d echoes an email the file did not carry: %q", r.Row, r.Email)
			}
		}
		if after := e.state(t); !maps.Equal(before, after) {
			t.Errorf("dry run changed members:\nbefore %v\nafter  %v", before, after)
		}
		if n := e.importAuditRows(t); n != 0 {
			t.Errorf("dry run wrote %d audit rows", n)
		}
	})

	t.Run("apply writes the plan with value-free audit rows", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sheet := []byte(e.sheet(t))
		_, preview := e.post(t, sheet, nil)
		code, resp := e.post(t, sheet, map[string]string{"dry_run": "false", "plan_token": preview.PlanToken})
		if code != http.StatusOK || resp.DryRun {
			t.Fatalf("apply = %d: %+v", code, resp)
		}
		got := e.state(t)
		for key, want := range map[string]string{
			"alice": "yes 2026-03-15", "bjorn": "yes 2026-04-01", "carl": "yes 2020-01-01",
			"dina": "yes 2020-01-01", "erik": "yes", "frida": "no", "gina": "no",
			"harald": "yes", "kari": "yes", "ola1": "no", "ola2": "no", "ivar": "no", "jon": "no",
		} {
			if got[key] != want {
				t.Errorf("%s = %q, want %q", key, got[key], want)
			}
		}

		rows, err := e.db.Query(context.Background(),
			`SELECT action, COALESCE(resource_id, ''), details::text FROM audit_log
			  WHERE club_id = $1 AND action IN ($2, $3)`,
			e.clubID, audit.ActionUserHarborShareSet, audit.ActionUserHarborSharesImported)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		perUser, summaries := map[string]bool{}, 0
		for rows.Next() {
			var action, resourceID, details string
			if err := rows.Scan(&action, &resourceID, &details); err != nil {
				t.Fatal(err)
			}
			switch action {
			case audit.ActionUserHarborShareSet:
				perUser[resourceID] = true
				if details != `{"source": "import"}` {
					t.Errorf("per-member audit details = %s", details)
				}
			case audit.ActionUserHarborSharesImported:
				summaries++
				var counts map[string]any
				if err := json.Unmarshal([]byte(details), &counts); err != nil {
					t.Fatalf("summary details: %v", err)
				}
				for k, v := range counts {
					if _, isNum := v.(float64); !isNum {
						t.Errorf("summary %s = %v, want counts only", k, v)
					}
				}
			}
		}
		for _, key := range []string{"alice", "bjorn", "harald", "kari"} {
			if !perUser[e.ids[key]] {
				t.Errorf("no %s row for %s", audit.ActionUserHarborShareSet, key)
			}
		}
		if len(perUser) != 4 || summaries != 1 {
			t.Errorf("audit rows: %d per member, %d summaries", len(perUser), summaries)
		}
	})

	t.Run("resolutions", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sheet := []byte(e.sheet(t))
		pick := func(row, userID string) map[string]string {
			return map[string]string{"resolutions": `{"` + row + `":"` + userID + `"}`}
		}

		code, resp := e.post(t, sheet, pick("12", e.ids["ola2"]))
		if code != http.StatusOK {
			t.Fatalf("resolved dry run = %d: %+v", code, resp)
		}
		if r := rowByNumber(t, resp, 12); r.Outcome != harborShareSet || r.UserID != e.ids["ola2"] || !r.Resolved {
			t.Errorf("resolved row 12 = %+v", r)
		}
		code, resp = e.post(t, sheet, pick("13", e.ids["ola1"]))
		if r := rowByNumber(t, resp, 13); code != http.StatusOK || r.Outcome != harborShareSet {
			t.Errorf("unmatched row resolved by search = %d %+v", code, r)
		}

		for name, fields := range map[string]map[string]string{
			"another club's member": pick("12", e.otherID),
			"an erased member":      pick("12", e.ids["ivar"]),
			"a row not in the file": pick("99", e.ids["frida"]),
			"an error row":          pick("16", e.ids["carl"]),
			"not JSON":              {"resolutions": "12=ola"},
		} {
			if code, resp := e.post(t, sheet, fields); code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400 (%+v)", name, code, resp)
			}
		}

		// A pick for a row that matches by itself is dropped from a dry run
		// (a 409 re-preview can make an earlier pick moot) and refused on apply.
		code, resp = e.post(t, sheet, pick("2", e.ids["frida"]))
		if r := rowByNumber(t, resp, 2); code != http.StatusOK || !r.ResolutionDropped || r.UserID != e.ids["alice"] || r.Message == "" {
			t.Errorf("dry run with a moot pick = %d %+v", code, r)
		}
		moot := pick("2", e.ids["frida"])
		moot["dry_run"], moot["plan_token"] = "false", resp.PlanToken
		if code, _ := e.post(t, sheet, moot); code != http.StatusBadRequest {
			t.Errorf("apply with a moot pick = %d, want 400", code)
		}

		resolution := pick("12", e.ids["ola2"])
		_, preview := e.post(t, sheet, resolution)
		resolution["dry_run"] = "false"
		resolution["plan_token"] = preview.PlanToken
		if code, resp := e.post(t, sheet, resolution); code != http.StatusOK {
			t.Fatalf("apply with resolution = %d: %+v", code, resp)
		}
		if got := e.state(t); got["ola2"] != "yes" || got["ola1"] != "no" {
			t.Errorf("after resolved apply ola1 = %q, ola2 = %q", got["ola1"], got["ola2"])
		}
	})

	t.Run("a PATCH between preview and apply is a 409 and changes nothing", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sheet := []byte(e.sheet(t))
		_, preview := e.post(t, sheet, nil)

		rec := e.do(t, http.MethodPatch, "/users/{userID}", "/users/"+e.ids["alice"], e.h.HandleUpdateUser,
			`{"harbor_share_owner": true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d: %s", rec.Code, rec.Body.String())
		}
		before := e.state(t)

		code, resp := e.post(t, sheet, map[string]string{"dry_run": "false", "plan_token": preview.PlanToken})
		if code != http.StatusConflict || resp.Code != codeHarborSharePlanStale {
			t.Fatalf("apply after PATCH = %d %+v, want 409 plan_changed", code, resp)
		}
		if after := e.state(t); !maps.Equal(before, after) {
			t.Errorf("a refused apply changed members:\nbefore %v\nafter  %v", before, after)
		}
		if n := e.importAuditRows(t); n != 0 {
			t.Errorf("a refused apply wrote %d audit rows", n)
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		for name, c := range map[string]struct {
			csv    string
			fields map[string]string
		}{
			"cp1252":                {"name;harbor_share_owner\nBj\xF8rn \xD8vre;ja\n", nil},
			"no owner column":       {"name\nAlice Synthetic\n", nil},
			"no member column":      {"harbor_share_owner\nja\n", nil},
			"apply without a token": {"name,harbor_share_owner\nAlice Synthetic,ja\n", map[string]string{"dry_run": "false"}},
			"bad dry_run":           {"name,harbor_share_owner\nAlice Synthetic,ja\n", map[string]string{"dry_run": "maybe"}},
			"over the row cap":      {"name,harbor_share_owner\n" + strings.Repeat("Alice Synthetic,ja\n", harborShareMaxRows+1), nil},
		} {
			if code, resp := e.post(t, []byte(c.csv), c.fields); code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400 (%+v)", name, code, resp)
			}
		}
	})

	t.Run("a write that misses mid-transaction rolls back the whole apply", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sheet := []byte(e.sheet(t))
		_, preview := e.post(t, sheet, nil)

		// Kari is the plan's last write (row 11). A trigger that skips her
		// UPDATE stands in for a member changed by another transaction after
		// this one read them: alice, bjorn and harald are already written.
		ctx := context.Background()
		mustExec(t, e.db, ctx, `CREATE FUNCTION skip_kari() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				IF OLD.id = '`+e.ids["kari"]+`'::uuid THEN RETURN NULL; END IF;
				RETURN NEW;
			END $$`)
		mustExec(t, e.db, ctx, `CREATE TRIGGER skip_kari BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION skip_kari()`)
		before := e.state(t)

		code, resp := e.post(t, sheet, map[string]string{"dry_run": "false", "plan_token": preview.PlanToken})
		if code != http.StatusConflict || resp.Code != codeHarborSharePlanStale {
			t.Fatalf("apply = %d %+v, want 409 plan_changed", code, resp)
		}
		if after := e.state(t); !maps.Equal(before, after) {
			t.Errorf("a missed write left earlier writes behind:\nbefore %v\nafter  %v", before, after)
		}
		if n := e.importAuditRows(t); n != 0 {
			t.Errorf("a rolled-back apply left %d audit rows", n)
		}
	})

	t.Run("rows that agree on a member write and audit once", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sheet := []byte("email;name;harbor_share_owner;harbor_share_paid_on\n" +
			e.email(t, "alice") + ";;ja;15.03.2026\n" +
			";Alice Synthetic;ja;2026-03-15\n")
		_, preview := e.post(t, sheet, nil)
		if r := rowByNumber(t, preview, 3); r.Outcome != harborShareDuplicate {
			t.Fatalf("row 3 = %+v, want duplicate of row 2", r)
		}
		if code, resp := e.post(t, sheet, map[string]string{"dry_run": "false", "plan_token": preview.PlanToken}); code != http.StatusOK {
			t.Fatalf("apply = %d %+v", code, resp)
		}
		var n int
		if err := e.db.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE club_id = $1 AND action = $2 AND resource_id = $3`,
			e.clubID, audit.ActionUserHarborShareSet, e.ids["alice"]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("alice has %d per-member audit rows, want 1", n)
		}
		if got := e.state(t)["alice"]; got != "yes 2026-03-15" {
			t.Errorf("alice = %q", got)
		}
	})

	t.Run("a system actor is never matched", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		sysEmail := "system-" + testutil.RandomHex(3) + "@example.com"
		mustExec(t, e.db, context.Background(),
			`UPDATE users SET first_name = 'Sys', last_name = 'Synthetic', email = $2 WHERE id = $1`, e.systemID, sysEmail)
		code, resp := e.post(t, []byte("email,name,harbor_share_owner\n"+sysEmail+",,ja\n,Sys Synthetic,ja\n"), nil)
		if code != http.StatusOK {
			t.Fatalf("dry run = %d %+v", code, resp)
		}
		for _, n := range []int{2, 3} {
			if r := rowByNumber(t, resp, n); r.Outcome != harborShareUnmatched || r.UserID != "" {
				t.Errorf("row %d = %+v, want unmatched", n, r)
			}
		}
		if code, _ := e.post(t, []byte("name,harbor_share_owner\nNobody Synthetic,ja\n"),
			map[string]string{"resolutions": `{"2":"` + e.systemID + `"}`}); code != http.StatusBadRequest {
			t.Errorf("resolving to the system actor = %d, want 400", code)
		}
	})

	t.Run("first_name and last_name columns match like name", func(t *testing.T) {
		t.Parallel()
		e := newHarborShareImportEnv(t)
		code, resp := e.post(t, []byte("first_name,last_name,harbor_share_owner\nalice,SYNTHETIC,yes\n"), nil)
		if r := rowByNumber(t, resp, 2); code != http.StatusOK || r.Outcome != harborShareSet || r.UserID != e.ids["alice"] {
			t.Errorf("first/last row = %d %+v", code, r)
		}
	})
}

// The user import shares readCSVUpload, so Excel's "CSV UTF-8" (BOM, ';')
// no longer loses its first column.
func TestIntegration_UserImportReadsExcelCSV(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "members.csv")
	_, _ = fw.Write([]byte("\xEF\xBB\xBFemail;first_name;last_name\r\nbom-" + testutil.RandomHex(3) + "@example.com;Åse;Synthetic\r\n"))
	_ = mw.Close()

	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/import", h.HandleImportUsersCSV)
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", authHeader(generateTestToken(e.adminID, e.clubID, []string{"admin"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Created int `json:"created"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body.Created != 1 {
		t.Errorf("import = %d %s, want one member created", rec.Code, rec.Body.String())
	}
}

// The import writes ownership for the whole club, so it is admin-only behind
// a fresh TOTP, inside /users's own board-or-admin gate. cmd/api mounts the
// route inline, so this composes the same gates in the same order; the
// handler's 400 on the non-multipart body shows the gates let it through.
func TestHarborShareImportRouteGates(t *testing.T) {
	t.Parallel()
	h := NewAdminUsersHandler(nil, &config.Config{}, zerolog.Nop())
	r := chi.NewRouter()
	r.Route("/admin", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(testSessionState)
		r.Use(middleware.RequireAdminTOTP(nil))
		r.Use(panicAsTeapot)
		r.Route("/users", func(r chi.Router) {
			r.Use(middleware.RequireRoleOrBoardRead("board", "admin"))
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.With(middleware.RequireFreshTOTPDefault()).
					Post("/harbor-shares/import", h.HandleImportHarborShares)
			})
		})
	})
	c := routeCall{http.MethodPost, "/admin/users/harbor-shares/import", true}

	if rec := serveWaitingList(r, c, nil, sessFresh); rec.Code != http.StatusUnauthorized {
		t.Errorf("without auth: %d, want 401", rec.Code)
	}
	for _, role := range []string{"board", "treasurer", "member"} {
		if rec := serveWaitingList(r, c, []string{role}, sessFresh); rec.Code != http.StatusForbidden {
			t.Errorf("as %s with a fresh TOTP: %d %s, want 403", role, rec.Code, rec.Body.String())
		}
	}
	if rec := serveWaitingList(r, c, []string{"admin"}, sessStepUp); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), `"totp_fresh_required"`) {
		t.Errorf("as admin with a stale TOTP: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body.String())
	}
	if rec := serveWaitingList(r, c, []string{"admin"}, sessFresh); !handlerReached(rec.Code) {
		t.Errorf("as admin with a fresh TOTP: %d %s, want the handler reached", rec.Code, rec.Body.String())
	}
}
