package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type aliasTestEnv struct {
	club  *fin.Club
	r     chi.Router
	audit func(action string) []map[string]any
}

func newAliasTestEnv(t *testing.T, mount func(r chi.Router, svc *accounting.Service, as *audit.Service)) *aliasTestEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	as := audit.NewService(pool, zerolog.Nop())
	svc := accounting.NewService(pool, as, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	r := chi.NewRouter()
	mount(r, svc, as)
	env := &aliasTestEnv{club: club, r: r}
	env.audit = func(action string) []map[string]any {
		t.Helper()
		rows, err := pool.Query(context.Background(),
			`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at`, club.ID, action)
		if err != nil {
			t.Fatalf("audit: %v", err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var d map[string]any
			if err := rows.Scan(&d); err != nil {
				t.Fatalf("audit scan: %v", err)
			}
			out = append(out, d)
		}
		return out
	}
	return env
}

func (e *aliasTestEnv) serve(roles []string, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	claims := &auth.Claims{UserID: e.club.Treasurer.ID, ClubID: e.club.ID, Roles: roles}
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func (e *aliasTestEnv) member(t *testing.T, last string) fin.Member {
	t.Helper()
	m := e.club.NewMember(t, "member")
	if _, err := e.club.Pool.Exec(context.Background(),
		`UPDATE users SET first_name = 'Synthetic', last_name = $2 WHERE id = $1`, m.ID, last); err != nil {
		t.Fatalf("name member: %v", err)
	}
	return m
}

// The Vipps queue's alias writes need the treasurer/admin role and a fresh
// TOTP, refuse a user outside the club, and are audited.
func TestCustomerAliasRoutesGatedAndAudited(t *testing.T) {
	var h *AccountingHandler
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h = NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountCustomerAliasRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		// Past the gate, as a stepped-up treasurer reaches the handlers.
		r.Route("/open", func(r chi.Router) { h.MountCustomerAliasRoutes(r) })
	})
	m := env.member(t, "Fortoy")
	body := map[string]string{"customer_name": "SYN FORTOY HOLDING", "user_id": m.ID}

	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated/", body); rec.Code != http.StatusForbidden {
		t.Errorf("POST as board: status %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"board"}, http.MethodDelete, "/gated/00000000-0000-0000-0000-000000000001", nil); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE as board: status %d, want 403", rec.Code)
	}
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated/", body)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"board"}, http.MethodGet, "/gated/", nil); rec.Code != http.StatusOK {
		t.Errorf("GET as board: status %d, want 200", rec.Code)
	}

	other := fin.NewClub(t, env.club.Pool)
	stranger := other.NewMember(t, "member")
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/", map[string]string{"customer_name": "SYN PAYER", "user_id": stranger.ID})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST for another club's user: %d %s, want 400", rec.Code, rec.Body)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/", body)
	var a accounting.VippsCustomerAlias
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &a) != nil || a.UserID != m.ID {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	taught := env.audit(audit.ActionPayerAliasTaught)
	if len(taught) != 1 || taught[0]["user_id"] != m.ID || taught[0]["source"] != accounting.PayerAliasSourceVippsQueue || taught[0]["override"] != false {
		t.Errorf("taught audit = %v", taught)
	}
	if _, named := taught[0]["customer_name"]; named {
		t.Errorf("taught audit carries the payer name: %v", taught[0])
	}

	if rec := env.serve([]string{"treasurer"}, http.MethodDelete, "/open/"+a.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodDelete, "/open/"+a.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second DELETE: status %d, want 404", rec.Code)
	}
	removed := env.audit(audit.ActionPayerAliasRemoved)
	if len(removed) != 1 || removed[0]["user_id"] != m.ID || removed[0]["source"] != accounting.PayerAliasSourceVippsQueue {
		t.Errorf("removed audit = %v", removed)
	}
}

// Tildel's assign answers 409 alias_conflict, booking nothing, for another
// member's name, and books and audits the alias once the override is
// confirmed.
func TestAssignInvoiceRemembersPayer(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/assign-invoice", h.HandleAssignInvoice)
		r.Post("/bank-rows/assign-invoice-multi", h.HandleAssignInvoiceMulti)
	})
	club := env.club
	m := env.member(t, "Fortoy")
	env.member(t, "Kaiplass")
	imp := club.NewBankImport(t)
	invoice := func(cents int64) fin.Invoice {
		return club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "membership", Cents: cents}}})
	}
	deposit := func(payer string, cents int64) string {
		return club.NewBankRow(t, imp, fin.BankRowOpts{Cents: cents, Counterpart: payer, FromAccount: "12345600009"})
	}
	booked := func(rowID string) bool {
		var b bool
		if err := club.Pool.QueryRow(context.Background(),
			`SELECT journal_entry_id IS NOT NULL FROM bank_import_rows WHERE id = $1`, rowID).Scan(&b); err != nil {
			t.Fatalf("row: %v", err)
		}
		return b
	}

	inv := invoice(30000)
	row := deposit("Synthetic Kaiplass", 30000)
	req := map[string]any{"invoice_id": inv.ID, "remember_payer": true}
	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice", req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"alias_conflict"`) {
		t.Fatalf("conflicting assign: %d %s, want 409 alias_conflict", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "Kaiplass") {
		t.Errorf("409 names the other member: %s", rec.Body)
	}
	if booked(row) || len(env.audit(audit.ActionBankRowAssignedInvoice)) != 0 {
		t.Fatalf("a refused alias booked or audited the assign")
	}

	req["confirm_alias_override"] = true
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+row+"/assign-invoice", req); rec.Code != http.StatusOK || !booked(row) {
		t.Fatalf("override assign: %d %s", rec.Code, rec.Body)
	}
	taught := env.audit(audit.ActionPayerAliasTaught)
	if len(taught) != 1 || taught[0]["user_id"] != m.ID || taught[0]["source"] != accounting.PayerAliasSourceBankTildel || taught[0]["override"] != true {
		t.Errorf("taught audit = %v", taught)
	}

	multi := invoice(30000)
	rows := []string{deposit("SYN FORTOY HOLDING", 10000), deposit("SYN FORTOY HOLDING", 20000)}
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/assign-invoice-multi",
		map[string]any{"row_ids": rows, "invoice_id": multi.ID, "remember_payer": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("multi assign: %d %s", rec.Code, rec.Body)
	}
	if taught := env.audit(audit.ActionPayerAliasTaught); len(taught) != 2 || taught[1]["override"] != false {
		t.Errorf("taught audit after multi = %v", taught)
	}
}
