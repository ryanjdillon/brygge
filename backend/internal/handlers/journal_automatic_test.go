package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A bilag the club's system actor created reads automatic=true in the list and
// the detail response; a person's reads false. The flag is derived from
// users.kind, and the response carries the actor's id only as created_by, as it
// does for a person: no name field is added for either.
func TestJournalEntriesExposeAutomatic(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewAccountingHandler(svc, nil, nil, nil, nil, zerolog.Nop())

	systemID, err := svc.SystemActor(ctx, club.ID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	book := func(createdBy, description string) string {
		t.Helper()
		e, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
			FiscalPeriodID: club.PeriodID,
			EntryDate:      time.Date(club.Year, 4, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			Description:    description,
			CreatedBy:      createdBy,
			ClubID:         club.ID,
			Lines: []accounting.CreateJournalLineInput{
				{AccountCode: "1920", Debit: 200},
				{AccountCode: "3900", Credit: 200},
			},
		})
		if err != nil {
			t.Fatalf("entry %q: %v", description, err)
		}
		if want := createdBy == systemID; e.Automatic != want {
			t.Errorf("create response for %q: automatic = %v, want %v", description, e.Automatic, want)
		}
		return e.ID
	}
	personEntryID := book(club.Treasurer.ID, "Synthetic person bilag")
	systemEntryID := book(systemID, "Synthetic night bilag")

	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := chi.NewRouter()
		r.Get("/journal", h.HandleListJournalEntries)
		r.Get("/journal/{entryID}", h.HandleGetJournalEntry)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d (%s)", path, rec.Code, rec.Body.String())
		}
		return rec
	}
	// The system actor's display name and address must not appear in either
	// response: a name label would identify it as a member.
	assertNoActorIdentity := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		body := rec.Body.String()
		for _, leak := range []string{systemActorName, accounting.SystemActorEmail, "Automatisk"} {
			if strings.Contains(body, leak) {
				t.Errorf("response exposes %q: %s", leak, body)
			}
		}
	}

	want := map[string]bool{personEntryID: false, systemEntryID: true}

	list := call("/journal")
	var entries []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(entries) != len(want) {
		t.Fatalf("list returned %d entries, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		id, _ := e["id"].(string)
		got, ok := e["automatic"].(bool)
		if !ok {
			t.Fatalf("list entry %s has no boolean automatic field", id)
		}
		if got != want[id] {
			t.Errorf("list: entry %s automatic = %v, want %v", id, got, want[id])
		}
	}
	assertNoActorIdentity(list)

	for id, wantAuto := range want {
		rec := call("/journal/" + id)
		var e map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		if got, ok := e["automatic"].(bool); !ok || got != wantAuto {
			t.Errorf("detail: entry %s automatic = %v, want %v", id, e["automatic"], wantAuto)
		}
		assertNoActorIdentity(rec)
	}

	// A void is a second insert. The automatic passes void as the system actor,
	// so its reversal reads automatic=true; a person's void reads false.
	voidAs := func(actorID, description string) {
		t.Helper()
		id := book(club.Treasurer.ID, description)
		if err := svc.PostJournalEntry(ctx, club.ID, id, club.Treasurer.ID); err != nil {
			t.Fatalf("post %q: %v", description, err)
		}
		reversal, err := svc.VoidJournalEntry(ctx, club.ID, id, actorID)
		if err != nil {
			t.Fatalf("void %q: %v", description, err)
		}
		wantAuto := actorID == systemID
		if reversal.Automatic != wantAuto {
			t.Errorf("reversal by %s: automatic = %v in the void result, want %v", actorID, reversal.Automatic, wantAuto)
		}
		rec := call("/journal/" + reversal.ID)
		var e map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Fatalf("decode reversal: %v", err)
		}
		if got, ok := e["automatic"].(bool); !ok || got != wantAuto {
			t.Errorf("reversal detail: automatic = %v, want %v", e["automatic"], wantAuto)
		}
	}
	voidAs(systemID, "Synthetic bilag voided by the system")
	voidAs(club.Treasurer.ID, "Synthetic bilag voided by a person")
}
