package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Post and void map a foreign entry to 404 and a closed period to 409
// period_closed, so the SPA can tell the operator to reopen or rebook.
func TestJournalPostVoidScopingAndClosedPeriod(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	other := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewAccountingHandler(svc, nil, nil, nil, nil, zerolog.Nop())

	e, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: club.PeriodID,
		EntryDate:      time.Date(club.Year, 4, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description:    "Synthetic bilag",
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
		Lines: []accounting.CreateJournalLineInput{
			{AccountCode: "1920", Debit: 200},
			{AccountCode: "3900", Credit: 200},
		},
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}

	call := func(fn http.HandlerFunc, path string, c *fin.Club) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Post("/journal/{entryID}/post", fn)
		r.Post("/journal/{entryID}/void", fn)
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: c.Treasurer.ID, ClubID: c.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(h.HandlePostJournalEntry, "/journal/"+e.ID+"/post", other); rec.Code != http.StatusNotFound {
		t.Errorf("post foreign entry: status %d, want 404", rec.Code)
	}
	if rec := call(h.HandlePostJournalEntry, "/journal/"+e.ID+"/post", club); rec.Code != http.StatusOK {
		t.Fatalf("post own entry: status %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := call(h.HandleVoidJournalEntry, "/journal/"+e.ID+"/void", other); rec.Code != http.StatusNotFound {
		t.Errorf("void foreign entry: status %d, want 404", rec.Code)
	}

	if _, err := pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, club.PeriodID); err != nil {
		t.Fatalf("close period: %v", err)
	}
	rec := call(h.HandleVoidJournalEntry, "/journal/"+e.ID+"/void", club)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body.Code != "period_closed" {
		t.Errorf("void in closed period: status %d code %q, want 409 period_closed", rec.Code, body.Code)
	}
}
