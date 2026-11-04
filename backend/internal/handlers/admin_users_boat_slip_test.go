package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// PUT /admin/users/{userID}/boats/{boatID}/slip is the one way an admin
// assigns a slip. An active assignment must carry a boat (CHECK
// active_assignment_has_boat), so the boat in the path must be the member's
// own boat in this club, and the row written must record it.
func TestIntegration_SetUserBoatSlip(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())
	const pattern = "/users/{userID}/boats/{boatID}/slip"

	member, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	neighbour, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	other := testutil.SeedClub(t, e.db)
	foreignUser, _ := testutil.SeedUser(t, e.db, other, []string{"member"})

	seedBoat := func(userID, clubID string) string {
		t.Helper()
		var id string
		if err := e.db.QueryRow(ctx,
			`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`,
			userID, clubID).Scan(&id); err != nil {
			t.Fatalf("seed boat: %v", err)
		}
		return id
	}
	boat := seedBoat(member, e.clubID)
	neighbourBoat := seedBoat(neighbour, e.clubID)
	foreignBoat := seedBoat(foreignUser, other)

	var slipID string
	if err := e.db.QueryRow(ctx, `INSERT INTO slips (club_id, number) VALUES ($1, 'B7') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
		t.Fatalf("seed slip: %v", err)
	}
	body := `{"slip_id":"` + slipID + `","assignment_type":"seasonal"}`

	activeRows := func() int {
		t.Helper()
		var n int
		if err := e.db.QueryRow(ctx,
			`SELECT count(*) FROM slip_assignments WHERE slip_id = $1 AND released_at IS NULL`, slipID).Scan(&n); err != nil {
			t.Fatalf("count assignments: %v", err)
		}
		return n
	}

	for _, c := range []struct{ name, boatID string }{
		{"another member's boat", neighbourBoat},
		{"a boat of another club", foreignBoat},
	} {
		rec := e.do(t, http.MethodPut, pattern, "/users/"+member+"/boats/"+c.boatID+"/slip", h.HandleSetUserBoatSlip, body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: assign = %d, want 404; body: %s", c.name, rec.Code, rec.Body.String())
		}
	}
	if n := activeRows(); n != 0 {
		t.Fatalf("active assignments after refused requests = %d, want 0", n)
	}

	rec := e.do(t, http.MethodPut, pattern, "/users/"+member+"/boats/"+boat+"/slip", h.HandleSetUserBoatSlip, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var gotUser, gotBoat, gotType, status string
	if err := e.db.QueryRow(ctx,
		`SELECT sa.user_id, sa.boat_id, sa.assignment_type, s.status
		   FROM slip_assignments sa JOIN slips s ON s.id = sa.slip_id
		  WHERE sa.slip_id = $1 AND sa.released_at IS NULL`, slipID,
	).Scan(&gotUser, &gotBoat, &gotType, &status); err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	if gotUser != member || gotBoat != boat || gotType != "seasonal" || status != "occupied" {
		t.Errorf("assignment = (user %s, boat %s, type %s, slip %s); want (%s, %s, seasonal, occupied)",
			gotUser, gotBoat, gotType, status, member, boat)
	}

	// The slip is taken now; the neighbour's own boat cannot be put on it.
	rec = e.do(t, http.MethodPut, pattern, "/users/"+neighbour+"/boats/"+neighbourBoat+"/slip", h.HandleSetUserBoatSlip, body)
	if rec.Code != http.StatusConflict {
		t.Errorf("assign to an occupied slip = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
	if n := activeRows(); n != 1 {
		t.Errorf("active assignments = %d, want 1", n)
	}
}
