package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// refEnv mounts the period toggle behind the money gate (and once past it),
// plus the booking routes a reference year refuses.
func newRefEnv(t *testing.T) (*refEnv, *accounting.Service, string, int) {
	t.Helper()
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		bh := NewBankRowsHandler(s, as, zerolog.Nop())
		gate := []func(http.Handler) http.Handler{
			middleware.RequireRole("treasurer", "admin"),
			middleware.RequireFreshTOTPDefault(),
		}
		r.With(gate...).Post("/gated/periods/{periodID}/reference", h.HandleSetPeriodReference)
		r.Post("/periods/{periodID}/reference", h.HandleSetPeriodReference)
		r.Post("/periods/{periodID}/close", h.HandleClosePeriod)
		r.Post("/periods/{periodID}/reopen", h.HandleReopenPeriod)
		r.Post("/journal", h.HandleCreateJournalEntry)
		r.Post("/journal/{entryID}/post", h.HandlePostJournalEntry)
		r.Post("/journal/{entryID}/void", h.HandleVoidJournalEntry)
		r.Post("/bank-rows/{rowID}/assign-account", bh.HandleAssignAccount)
		r.Post("/bank-rows/{rowID}/unassign", bh.HandleUnassign)
		r.Post("/bank-rows/{rowID}/undismiss", bh.HandleUndismiss)
		r.Post("/bank-rows/{rowID}/dismiss", bh.HandleDismiss)
		r.Get("/bank-rows/unmatched", bh.HandleListUnmatched)
		r.Get("/bank-rows/unmatched/count", bh.HandleCountUnmatched)
		r.Post("/bank-import/{importID}/rows/{rowID}/match", h.HandleMatchBankRow)
		r.Patch("/bank-import/{importID}/account", h.HandleReassignBankImport)
	})
	re := &refEnv{aliasTestEnv: env, nowYear: time.Now().Year()}
	re.currentPeriod = re.period(t, re.nowYear)
	year := re.nowYear - 1
	return re, svc, re.period(t, year), year
}

// refEnv knows the calendar year, because the year covering today can't be
// marked and the club's own default year is fixed.
type refEnv struct {
	*aliasTestEnv
	nowYear       int
	currentPeriod string
}

// period returns the club's period for the year, creating it when missing.
func (e *refEnv) period(t *testing.T, year int) string {
	t.Helper()
	var id string
	if err := e.club.Pool.QueryRow(context.Background(),
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'open')
		 ON CONFLICT (club_id, year) DO UPDATE SET year = EXCLUDED.year RETURNING id`,
		e.club.ID, year,
	).Scan(&id); err != nil {
		t.Fatalf("period %d: %v", year, err)
	}
	return id
}

type refResp struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	accounting.FiscalPeriod
}

func decodeRef(t *testing.T, rec *httptest.ResponseRecorder) refResp {
	t.Helper()
	var out refResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

// Marking a year needs the treasurer/admin role and a fresh TOTP, is audited
// once per change, and its refusals are 409 with a code.
func TestSetPeriodReferenceRouteGatedAndAudited(t *testing.T) {
	env, _, periodID, year := newRefEnv(t)
	path := "/periods/" + periodID + "/reference"
	on := map[string]any{"reference_only": true}

	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated"+path, on); rec.Code != http.StatusForbidden {
		t.Errorf("POST as board: %d, want 403", rec.Code)
	}
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated"+path, on)
	if rec.Code != http.StatusForbidden || decodeRef(t, rec).Error != "totp_fresh_required" {
		t.Errorf("POST as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, path, map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Errorf("POST without a value: %d, want 400", rec.Code)
	}

	// A draft left in place is counted in the audit row.
	if _, err := env.club.Pool.Exec(context.Background(),
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, make_date($3, 3, 1), 'Synthetic draft', $4)`,
		env.club.ID, periodID, year, env.club.Treasurer.ID); err != nil {
		t.Fatalf("draft: %v", err)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPost, path, on)
	if got := decodeRef(t, rec); rec.Code != http.StatusOK || !got.ReferenceOnly || got.Year != year {
		t.Fatalf("mark: %d %s", rec.Code, rec.Body)
	}
	set := env.audit(audit.ActionAccountingPeriodReferenceSet)
	if len(set) != 1 || set[0]["year"] != float64(year) || set[0]["status"] != "open" || set[0]["drafts"] != float64(1) {
		t.Errorf("set audit = %v", set)
	}

	if rec := env.serve([]string{"treasurer"}, http.MethodPost, path, on); rec.Code != http.StatusOK {
		t.Errorf("mark again: %d", rec.Code)
	}
	if n := len(env.audit(audit.ActionAccountingPeriodReferenceSet)); n != 1 {
		t.Errorf("a no-op was audited: %d set rows", n)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPost, path, map[string]any{"reference_only": false})
	if got := decodeRef(t, rec); rec.Code != http.StatusOK || got.ReferenceOnly {
		t.Fatalf("unmark: %d %s", rec.Code, rec.Body)
	}
	if n := len(env.audit(audit.ActionAccountingPeriodReferenceCleared)); n != 1 {
		t.Errorf("%d cleared audit rows, want 1", n)
	}
}

func TestSetPeriodReferenceRouteRefusals(t *testing.T) {
	env, _, periodID, _ := newRefEnv(t)
	conflict := func(path string, on bool, wantStatus int, wantCode string) {
		t.Helper()
		rec := env.serve([]string{"treasurer"}, http.MethodPost, path, map[string]any{"reference_only": on})
		if got := decodeRef(t, rec); rec.Code != wantStatus || got.Code != wantCode {
			t.Errorf("POST %s %v: %d %s, want %d %q", path, on, rec.Code, rec.Body, wantStatus, wantCode)
		}
	}

	conflict("/periods/"+env.currentPeriod+"/reference", true, http.StatusConflict, "reference_current_year")

	opening := env.nowYear - 2
	obPeriod := env.period(t, opening)
	if _, err := env.club.Pool.Exec(context.Background(),
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, source, created_by)
		 VALUES ($1, $2, 1, make_date($3, 12, 31), 'Synthetic opening', 'opening_balance', $4)`,
		env.club.ID, obPeriod, opening, env.club.Treasurer.ID); err != nil {
		t.Fatalf("opening balance: %v", err)
	}
	conflict("/periods/"+obPeriod+"/reference", true, http.StatusConflict, "reference_has_opening_balance")

	conflict("/periods/00000000-0000-0000-0000-000000000001/reference", true, http.StatusNotFound, "")

	conflict("/periods/"+periodID+"/reference", true, http.StatusOK, "")
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/periods/"+periodID+"/close", nil); rec.Code != http.StatusOK {
		t.Fatalf("close the reference year: %d %s", rec.Code, rec.Body)
	}
	conflict("/periods/"+periodID+"/reference", false, http.StatusConflict, "period_closed")
}

// A booking a reference year refuses answers 409 reference_period on every
// route that can reach the guard.
func TestReferencePeriodBookingsAnswerCode(t *testing.T) {
	env, svc, periodID, year := newRefEnv(t)
	club := env.club
	ctx := context.Background()

	imp := club.NewBankImport(t)
	deposit := club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC), Cents: 50000})
	outgoing := club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 2, 0, 0, 0, 0, time.UTC), Cents: -20000})

	draft, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: periodID, EntryDate: time.Date(year, 6, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description: "Synthetic bilag", CreatedBy: club.Treasurer.ID, ClubID: club.ID,
		Lines: []accounting.CreateJournalLineInput{{AccountCode: "1920", Debit: 10}, {AccountCode: "3100", Credit: 10}},
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	receivable, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: periodID, EntryDate: time.Date(year, 6, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description: "Synthetic bilag", CreatedBy: club.Treasurer.ID, ClubID: club.ID,
		Lines: []accounting.CreateJournalLineInput{{AccountCode: "1920", Debit: 10}, {AccountCode: "1500", Credit: 10}},
	})
	if err != nil {
		t.Fatalf("receivable: %v", err)
	}
	if err := svc.PostJournalEntry(ctx, club.ID, receivable.ID, club.Treasurer.ID); err != nil {
		t.Fatalf("post receivable: %v", err)
	}
	if _, _, err := svc.SetPeriodReference(ctx, club.ID, periodID, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	want := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		var body map[string]string
		if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
			body["code"] != "reference_period" || body["error"] != accounting.ErrReferencePeriod.Error() {
			t.Errorf("%s: %d %s, want 409 reference_period with the Norwegian message", name, rec.Code, rec.Body)
		}
	}

	want("assign an incoming reference row to revenue", env.serve([]string{"treasurer"}, http.MethodPost,
		"/bank-rows/"+deposit+"/assign-account", map[string]any{"account_code": "3100", "kind": "revenue", "confirm_alone": true}))
	want("create a revenue entry", env.serve([]string{"treasurer"}, http.MethodPost, "/journal", map[string]any{
		"fiscal_period_id": periodID, "entry_date": time.Date(year, 7, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		"description": "Synthetic bilag",
		"lines":       []map[string]any{{"account_code": "1920", "debit": 5}, {"account_code": "3100", "credit": 5}},
	}))
	want("post a non-expense draft", env.serve([]string{"treasurer"}, http.MethodPost, "/journal/"+draft.ID+"/post", nil))
	want("void a posted receivable entry", env.serve([]string{"treasurer"}, http.MethodPost, "/journal/"+receivable.ID+"/void", nil))

	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+outgoing+"/assign-account",
		map[string]any{"account_code": "6200", "kind": "expense", "confirm_alone": true})
	if rec.Code != http.StatusOK {
		t.Errorf("book an outgoing reference row to a cost account: %d %s, want 200", rec.Code, rec.Body)
	}
}

// The cash-flow chart counts drafts, so a reference year would show the Vipps
// drafts imported for it as income: its entries are left out.
func TestCashFlowLeavesOutReferenceYear(t *testing.T) {
	env, svc, periodID, year := newRefEnv(t)
	club := env.club
	ctx := context.Background()
	date := time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	if _, err := svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: periodID, EntryDate: date, Description: "Synthetic Vipps draft", Source: "vipps",
		CreatedBy: club.Treasurer.ID, ClubID: club.ID,
		Lines: []accounting.CreateJournalLineInput{{AccountCode: "1920", Debit: 800}, {AccountCode: "3100", Credit: 800}},
	}); err != nil {
		t.Fatalf("draft: %v", err)
	}

	h := NewFinancialsHandler(club.Pool, nil, nil, zerolog.Nop())
	income := func() float64 {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/cash-flow?year="+strconv.Itoa(year), nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		h.HandleGetCashFlow(rec, req)
		var body struct {
			Buckets []cashFlowMonthRow `json:"buckets"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("cash flow: %d %s", rec.Code, rec.Body)
		}
		var total float64
		for _, b := range body.Buckets {
			total += b.Income
		}
		return total
	}

	if got := income(); got != 800 {
		t.Fatalf("unmarked year income = %v, want 800", got)
	}
	if _, _, err := svc.SetPeriodReference(ctx, club.ID, periodID, true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := income(); got != 0 {
		t.Errorf("reference year income = %v, want 0", got)
	}
}

// The bank-account reassign rewrites posted journal lines directly, so it
// refuses a reference year itself (and an entry dated in one), and a manual
// match answers the guard's 409.
func TestReferenceYearRefusesReassignAndMatch(t *testing.T) {
	env, svc, periodID, year := newRefEnv(t)
	club := env.club
	ctx := context.Background()
	imp := club.NewBankImport(t)
	row := club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 2, 0, 0, 0, 0, time.UTC), Cents: -20000})
	deposit := club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 3, 0, 0, 0, 0, time.UTC), Cents: 30000})

	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-account",
		map[string]any{"account_code": "6200", "kind": "expense", "confirm_alone": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("book the outgoing row: %d %s", rec.Code, rec.Body)
	}
	if _, err := club.Pool.Exec(ctx, `INSERT INTO accounts (club_id, code, name, account_type, is_system)
		VALUES ($1, '1925', 'Synthetic bank', 'asset', false) ON CONFLICT (club_id, code) DO NOTHING`, club.ID); err != nil {
		t.Fatalf("account: %v", err)
	}
	if _, _, err := svc.SetPeriodReference(ctx, club.ID, periodID, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPatch, "/bank-import/"+imp+"/account",
		map[string]any{"bank_account_code": "1925"})
	if rec.Code != http.StatusConflict {
		t.Errorf("reassign an import with reference-year bookings: %d %s, want 409", rec.Code, rec.Body)
	}
	var bank string
	if err := club.Pool.QueryRow(ctx, `SELECT bank_account_code FROM bank_imports WHERE id = $1`, imp).Scan(&bank); err != nil || bank != "1920" {
		t.Errorf("import's bank account = %q (%v), want it unchanged", bank, err)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/bank-import/"+imp+"/rows/"+deposit+"/match", map[string]any{
		"period_id": env.currentPeriod, "debit_account_code": "1920", "credit_account_code": "3100",
	})
	var body map[string]string
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != "reference_period" {
		t.Errorf("match a reference deposit into the current year: %d %s, want 409 reference_period", rec.Code, rec.Body)
	}
}

// The Referanseår chip lists and counts the outgoing reference rows through
// kind=reference; combined with dismissed it is a 400, not a server error.
func TestUnmatchedKindReference(t *testing.T) {
	env, svc, periodID, year := newRefEnv(t)
	club := env.club
	imp := club.NewBankImport(t)
	club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC), Cents: 40000})
	out := club.NewBankRow(t, imp, fin.BankRowOpts{Date: time.Date(year, 6, 2, 0, 0, 0, 0, time.UTC), Cents: -15000})
	if _, _, err := svc.SetPeriodReference(context.Background(), club.ID, periodID, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	var count struct {
		Count int `json:"count"`
	}
	rec := env.serve([]string{"treasurer"}, http.MethodGet, "/bank-rows/unmatched/count?kind=reference", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &count) != nil || count.Count != 1 {
		t.Fatalf("reference count: %d %s, want 1", rec.Code, rec.Body)
	}
	rec = env.serve([]string{"treasurer"}, http.MethodGet, "/bank-rows/unmatched/count", nil)
	if json.Unmarshal(rec.Body.Bytes(), &count) != nil || count.Count != 0 {
		t.Errorf("default count = %d, want the reference year out of it", count.Count)
	}

	var list struct {
		Items []accounting.BankRowSummary `json:"items"`
	}
	rec = env.serve([]string{"treasurer"}, http.MethodGet, "/bank-rows/unmatched?kind=reference", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil ||
		len(list.Items) != 1 || list.Items[0].ID != out || !list.Items[0].ReferencePeriod {
		t.Fatalf("reference list: %d %s, want only the outgoing row flagged reference_period", rec.Code, rec.Body)
	}

	for _, path := range []string{"/bank-rows/unmatched?kind=bogus", "/bank-rows/unmatched/count?kind=reference,bogus"} {
		if rec := env.serve([]string{"treasurer"}, http.MethodGet, path, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400", path, rec.Code)
		}
	}

	// A mistaken dismissal is undone from the dismissed reference list, audited.
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+out+"/dismiss", map[string]string{"reason": "internal_transfer"}); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	rec = env.serve([]string{"treasurer"}, http.MethodGet, "/bank-rows/unmatched?kind=reference,dismissed", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].ID != out {
		t.Fatalf("dismissed reference list: %d %s, want the dismissed row", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+out+"/undismiss", nil); rec.Code != http.StatusOK {
		t.Fatalf("undismiss: %d %s", rec.Code, rec.Body)
	}
	undone := env.audit(audit.ActionBankRowUndismissed)
	if len(undone) != 1 || undone[0]["prior_reason"] != "internal_transfer" ||
		undone[0]["prior_dismissed_by"] != env.club.Treasurer.ID || undone[0]["prior_dismissed_at"] == nil {
		t.Errorf("undismiss audit = %v, want the prior reason, who and when", undone)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/not-a-uuid/undismiss", nil); rec.Code != http.StatusNotFound {
		t.Errorf("undismiss a malformed row id: %d, want 404", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+out+"/undismiss", nil); rec.Code != http.StatusConflict {
		t.Errorf("undismiss a row that is not dismissed: %d, want 409", rec.Code)
	}
}

// A period id that is not a uuid is not found, like any unknown id.
func TestSetPeriodReferenceMalformedIDIsNotFound(t *testing.T) {
	env, _, _, _ := newRefEnv(t)
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/periods/not-a-uuid/reference", map[string]any{"reference_only": true})
	if rec.Code != http.StatusNotFound {
		t.Errorf("malformed period id: %d %s, want 404", rec.Code, rec.Body)
	}
}

// A negative mva_amount on a journal line is a typed 400.
func TestCreateJournalEntryNegativeMVAIsBadRequest(t *testing.T) {
	env, _, _, _ := newRefEnv(t)
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/journal", map[string]any{
		"fiscal_period_id": env.currentPeriod, "entry_date": time.Now().Format("2006-01-02"), "description": "Synthetic bilag",
		"lines": []map[string]any{
			{"account_code": "6200", "debit": 100, "mva_amount": -20}, {"account_code": "1920", "credit": 100},
		},
	})
	var body map[string]string
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != "negative_mva" {
		t.Errorf("negative MVA: %d %s, want 400 negative_mva", rec.Code, rec.Body)
	}
}
