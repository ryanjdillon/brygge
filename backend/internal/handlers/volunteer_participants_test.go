package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func (e *integrationEnv) getTaskParticipants(t *testing.T, taskID, callerID string, callerRoles []string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewVolunteerHandler(e.db, e.cfg, e.log)
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Get("/tasks/{taskID}/participants", h.HandleListTaskParticipants)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/participants", nil)
	req.Header.Set("Authorization", "Bearer "+e.generateToken(callerID, callerRoles))
	r.ServeHTTP(rec, req)
	return rec
}

func seedTask(t *testing.T, e *integrationEnv, clubID, createdBy string, participants ...string) string {
	t.Helper()
	ctx := context.Background()
	var projectID, taskID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO projects (club_id, name, created_by) VALUES ($1, 'Synthetic project', $2) RETURNING id`,
		clubID, createdBy,
	).Scan(&projectID); err != nil {
		t.Fatalf("seeding project: %v", err)
	}
	if err := e.db.QueryRow(ctx,
		`INSERT INTO tasks (project_id, club_id, title, created_by, max_collaborators)
		 VALUES ($1, $2, 'Synthetic task', $3, 20) RETURNING id`,
		projectID, clubID, createdBy,
	).Scan(&taskID); err != nil {
		t.Fatalf("seeding task: %v", err)
	}
	for _, uid := range participants {
		if _, err := e.db.Exec(ctx,
			`INSERT INTO task_participants (task_id, user_id, role) VALUES ($1, $2, 'collaborator')`,
			taskID, uid,
		); err != nil {
			t.Fatalf("seeding participant: %v", err)
		}
	}
	return taskID
}

func TestIntegration_TaskParticipantsCrossClub(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	f := directoryFixture{t: t, db: env.db, clubID: env.clubID}
	caller := f.member("Syn", "Caller", "caller@example.test", "", false, "member", "admin")

	other := testutil.SeedClub(t, env.db)
	foreign := directoryFixture{t: t, db: env.db, clubID: other}
	foreignUser := foreign.member("Syn", "ForeignParticipant", "foreign@example.test", "", false, "member")
	foreignTask := seedTask(t, env, other, foreignUser, foreignUser)

	for name, id := range map[string]string{
		"another club's task": foreignTask,
		"unknown task":        "00000000-0000-0000-0000-0000000000aa",
		"malformed id":        "not-a-uuid",
	} {
		rec := env.getTaskParticipants(t, id, caller, []string{"member", "admin"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404; body: %s", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "ForeignParticipant") {
			t.Errorf("%s: response names another club's participant: %s", name, rec.Body.String())
		}
	}
}

func TestIntegration_TaskParticipantsHonourHideInDirectory(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	f := directoryFixture{t: t, db: env.db, clubID: env.clubID}

	visible := f.member("Syn", "Visible", "visible@example.test", "", false, "member")
	hidden := f.member("Syn", "Hidden", "hidden@example.test", "", true, "member")
	hiddenAdmin := f.member("Syn", "HiddenAdmin", "hiddenadmin@example.test", "", true, "admin")
	hiddenBoard := f.member("Syn", "HiddenBoard", "hiddenboard@example.test", "", true, "member", "treasurer")
	self := f.member("Syn", "HiddenSelf", "self@example.test", "", true, "member")
	var nameless string
	if err := env.db.QueryRow(context.Background(),
		`INSERT INTO users (club_id, email) VALUES ($1, 'nameless@example.test') RETURNING id`, env.clubID,
	).Scan(&nameless); err != nil {
		t.Fatalf("seeding nameless member: %v", err)
	}
	task := seedTask(t, env, env.clubID, visible, visible, hidden, hiddenAdmin, hiddenBoard, self, nameless)

	names := func(callerID string, callerRoles []string) map[string]string {
		t.Helper()
		rec := env.getTaskParticipants(t, task, callerID, callerRoles)
		assertStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), "hide_in_directory") {
			t.Fatalf("participants response leaks the hide flag: %s", rec.Body.String())
		}
		rows := decodeJSON[[]taskParticipant](t, rec)
		out := map[string]string{}
		for _, p := range rows {
			out[p.UserID] = p.Name
		}
		if len(out) != 6 {
			t.Fatalf("got %d participants, want 6: %v", len(out), out)
		}
		return out
	}

	t.Run("member sees hidden participants without a name", func(t *testing.T) {
		got := names(self, []string{"member"})
		want := map[string]string{
			visible:     "Syn Visible",
			hidden:      "",
			hiddenAdmin: "",
			hiddenBoard: "Syn HiddenBoard",
			self:        "Syn HiddenSelf",
			nameless:    "",
		}
		for id, w := range want {
			if got[id] != w {
				t.Errorf("participant %s name = %q, want %q", id, got[id], w)
			}
		}
	})

	for _, callerRoles := range [][]string{{"member", "treasurer"}, {"admin"}} {
		t.Run("privileged "+strings.Join(callerRoles, "+")+" sees every name", func(t *testing.T) {
			got := names(visible, callerRoles)
			want := map[string]string{
				visible:     "Syn Visible",
				hidden:      "Syn Hidden",
				hiddenAdmin: "Syn HiddenAdmin",
				hiddenBoard: "Syn HiddenBoard",
				self:        "Syn HiddenSelf",
				nameless:    "nameless@example.test",
			}
			for id, w := range want {
				if got[id] != w {
					t.Errorf("participant %s name = %q, want %q", id, got[id], w)
				}
			}
		})
	}
}
