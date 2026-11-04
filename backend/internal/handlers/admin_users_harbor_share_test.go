package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Harbor share ownership (BRD-14) is the harbor_share_owner flag; the paid
// date is optional. The list filter combines with ?spot= so the board can
// see owners with and without an active slip.
func TestIntegration_AdminUsersHarborShare(t *testing.T) { //nolint:gocognit,cyclop // one scenario walked through list, get, export and PATCH
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	ownerWithSlip, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	ownerNoDate, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	nonOwner, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})

	mustExec(t, e.db, ctx,
		`UPDATE users SET harbor_share_owner = true, harbor_share_paid_on = '2026-03-15' WHERE id = $1`, ownerWithSlip)
	mustExec(t, e.db, ctx, `UPDATE users SET harbor_share_owner = true WHERE id = $1`, ownerNoDate)

	var boatID, slipID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`,
		ownerWithSlip, e.clubID).Scan(&boatID); err != nil {
		t.Fatalf("seed boat: %v", err)
	}
	if err := e.db.QueryRow(ctx,
		`INSERT INTO slips (club_id, number, section) VALUES ($1, '7', 'Z') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
		t.Fatalf("seed slip: %v", err)
	}
	mustExec(t, e.db, ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, boat_id, club_id, assignment_type)
		 VALUES ($1, $2, $3, $4, 'permanent')`, slipID, ownerWithSlip, boatID, e.clubID)

	type listedUser struct {
		ID                string  `json:"id"`
		HarborShareOwner  bool    `json:"harbor_share_owner"`
		HarborSharePaidOn *string `json:"harbor_share_paid_on"`
	}
	list := func(t *testing.T, query string) []listedUser {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/users", "/users?"+query, h.HandleListUsers, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list ?%s = %d; body: %s", query, rec.Code, rec.Body.String())
		}
		var body struct {
			Users []listedUser `json:"users"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return body.Users
	}
	ids := func(us []listedUser) []string {
		out := []string{}
		for _, u := range us {
			if u.ID == ownerWithSlip || u.ID == ownerNoDate || u.ID == nonOwner {
				out = append(out, u.ID)
			}
		}
		return out
	}
	sameSet := func(got, want []string) bool {
		a, b := slices.Clone(got), slices.Clone(want)
		slices.Sort(a)
		slices.Sort(b)
		return slices.Equal(a, b)
	}

	t.Run("list filter combines with spot", func(t *testing.T) {
		for _, c := range []struct {
			query string
			want  []string
		}{
			{"harbor_share=owner", []string{ownerWithSlip, ownerNoDate}},
			{"harbor_share=not_owner", []string{nonOwner}},
			{"harbor_share=owner&spot=none", []string{ownerNoDate}},
			{"harbor_share=owner&spot=permanent", []string{ownerWithSlip}},
			{"harbor_share=owner&spot=seasonal", []string{}},
			{"harbor_share=bogus", []string{ownerWithSlip, ownerNoDate, nonOwner}},
		} {
			if got := ids(list(t, c.query)); !sameSet(got, c.want) {
				t.Errorf("?%s = %v, want %v", c.query, got, c.want)
			}
		}
	})

	t.Run("list exposes both fields", func(t *testing.T) {
		for _, u := range list(t, "") {
			switch u.ID {
			case ownerWithSlip:
				if !u.HarborShareOwner || u.HarborSharePaidOn == nil || *u.HarborSharePaidOn != "2026-03-15" {
					t.Errorf("owner with date = %+v", u)
				}
			case ownerNoDate:
				if !u.HarborShareOwner || u.HarborSharePaidOn != nil {
					t.Errorf("owner without date = %+v", u)
				}
			case nonOwner:
				if u.HarborShareOwner || u.HarborSharePaidOn != nil {
					t.Errorf("non-owner = %+v", u)
				}
			}
		}
	})

	t.Run("sort orders owners by date, unknown dates after, non-owners last", func(t *testing.T) {
		if got, want := ids(list(t, "sort=harbor_share")), []string{ownerWithSlip, ownerNoDate, nonOwner}; !slices.Equal(got, want) {
			t.Errorf("sort=harbor_share = %v, want %v", got, want)
		}
		// Owners stay one block in both directions; only the date order flips.
		if got, want := ids(list(t, "sort=-harbor_share")), []string{ownerWithSlip, ownerNoDate, nonOwner}; !slices.Equal(got, want) {
			t.Errorf("sort=-harbor_share = %v, want %v", got, want)
		}
	})

	t.Run("export carries both columns", func(t *testing.T) {
		rec := e.do(t, http.MethodGet, "/users/export", "/users/export", h.HandleExportUsersCSV, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("export = %d", rec.Code)
		}
		records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("parse export: %v", err)
		}
		header := records[0]
		ownerCol, dateCol := slices.Index(header, "harbor_share_owner"), slices.Index(header, "harbor_share_paid_on")
		if ownerCol < 0 || dateCol < 0 {
			t.Fatalf("export header %v lacks harbor share columns", header)
		}
		var emailWithSlip string
		if err := e.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, ownerWithSlip).Scan(&emailWithSlip); err != nil {
			t.Fatalf("read email: %v", err)
		}
		found := false
		for _, r := range records[1:] {
			if r[0] == emailWithSlip {
				found = true
				if r[ownerCol] != "true" || r[dateCol] != "2026-03-15" {
					t.Errorf("export row = owner %q date %q", r[ownerCol], r[dateCol])
				}
			}
		}
		if !found {
			t.Error("owner missing from export")
		}
	})

	read := func(t *testing.T, id string) (bool, *string) {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/users/{userID}", "/users/"+id, h.HandleGetUser, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get = %d; body: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			User struct {
				HarborShareOwner  bool    `json:"harbor_share_owner"`
				HarborSharePaidOn *string `json:"harbor_share_paid_on"`
			} `json:"user"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode get: %v", err)
		}
		return body.User.HarborShareOwner, body.User.HarborSharePaidOn
	}
	patch := func(t *testing.T, id, body string) int {
		t.Helper()
		return e.do(t, http.MethodPatch, "/users/{userID}", "/users/"+id, h.HandleUpdateUser, body).Code
	}

	t.Run("PATCH set, clear and remove", func(t *testing.T) {
		target, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})

		if code := patch(t, target, `{"harbor_share_paid_on":"2026-01-02"}`); code != http.StatusBadRequest {
			t.Errorf("date for a non-owner = %d, want 400", code)
		}
		if code := patch(t, target, `{"harbor_share_owner":false,"harbor_share_paid_on":"2026-01-02"}`); code != http.StatusBadRequest {
			t.Errorf("date with owner=false = %d, want 400", code)
		}
		if code := patch(t, target, `{"harbor_share_owner":true,"harbor_share_paid_on":"02.01.2026"}`); code != http.StatusBadRequest {
			t.Errorf("non-ISO date = %d, want 400", code)
		}
		if owner, d := read(t, target); owner || d != nil {
			t.Fatalf("refused PATCHes wrote (%v, %v)", owner, d)
		}

		if code := patch(t, target, `{"harbor_share_owner":true}`); code != http.StatusOK {
			t.Fatalf("set owner = %d", code)
		}
		if owner, d := read(t, target); !owner || d != nil {
			t.Errorf("after owner=true: (%v, %v), want (true, nil)", owner, d)
		}

		if code := patch(t, target, `{"harbor_share_paid_on":"2026-01-02"}`); code != http.StatusOK {
			t.Fatalf("set date = %d", code)
		}
		if owner, d := read(t, target); !owner || d == nil || *d != "2026-01-02" {
			t.Errorf("after date: (%v, %v), want (true, 2026-01-02)", owner, d)
		}

		if code := patch(t, target, `{"harbor_share_paid_on":""}`); code != http.StatusOK {
			t.Fatalf("clear date = %d", code)
		}
		if owner, d := read(t, target); !owner || d != nil {
			t.Errorf("after clearing date: (%v, %v), want (true, nil)", owner, d)
		}

		mustExec(t, e.db, ctx, `UPDATE users SET harbor_share_paid_on = '2026-01-02' WHERE id = $1`, target)
		if code := patch(t, target, `{"harbor_share_owner":false}`); code != http.StatusOK {
			t.Fatalf("remove owner = %d", code)
		}
		if owner, d := read(t, target); owner || d != nil {
			t.Errorf("after owner=false: (%v, %v), want (false, nil)", owner, d)
		}

		// The audit row names the fields, never the values.
		var details string
		if err := e.db.QueryRow(ctx,
			`SELECT details::text FROM audit_log
			  WHERE action = 'update_user' AND resource_id = $1
			  ORDER BY created_at DESC LIMIT 1`, target).Scan(&details); err != nil {
			t.Fatalf("read audit: %v", err)
		}
		// Removing ownership clears the date, so the row names both columns.
		var audit struct {
			Fields []string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(details), &audit); err != nil {
			t.Fatalf("decode audit details %s: %v", details, err)
		}
		if want := []string{"harbor_share_owner", "harbor_share_paid_on"}; !slices.Equal(audit.Fields, want) {
			t.Errorf("audit fields = %v, want %v", audit.Fields, want)
		}
		if strings.Contains(details, "2026") {
			t.Errorf("audit details carry a value: %s", details)
		}
	})
}

func TestUpdatedFields_HarborShare(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	date, empty := "2026-01-02", ""
	for _, c := range []struct {
		name        string
		req         adminUserUpdateRequest
		writesDate  bool
		wantUpdated []string
	}{
		{"empty", adminUserUpdateRequest{}, false, []string{}},
		{"owner=true", adminUserUpdateRequest{HarborShareOwner: &yes}, false, []string{"harbor_share_owner"}},
		{"owner=false clears date", adminUserUpdateRequest{HarborShareOwner: &no}, true,
			[]string{"harbor_share_owner", "harbor_share_paid_on"}},
		{"date only", adminUserUpdateRequest{HarborSharePaidOn: &date}, true, []string{"harbor_share_paid_on"}},
		{"clear date", adminUserUpdateRequest{HarborSharePaidOn: &empty}, true, []string{"harbor_share_paid_on"}},
		{"owner=true with date", adminUserUpdateRequest{HarborShareOwner: &yes, HarborSharePaidOn: &date}, true,
			[]string{"harbor_share_owner", "harbor_share_paid_on"}},
		{"other fields only", adminUserUpdateRequest{City: &date, Phone: &date}, false, []string{"city", "phone"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := writesHarborSharePaidOn(c.req); got != c.writesDate {
				t.Errorf("writesHarborSharePaidOn = %v, want %v", got, c.writesDate)
			}
			if got := updatedFields(c.req); !slices.Equal(got, c.wantUpdated) {
				t.Errorf("updatedFields = %v, want %v", got, c.wantUpdated)
			}
		})
	}
}
