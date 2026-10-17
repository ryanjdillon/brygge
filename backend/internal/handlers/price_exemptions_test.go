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
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// exemptionFixture is a club with a realistic price list (a tiered slip
// rent by beam, flat membership dues, a flat service), board members, and
// the handler wired to a real ledger so GL effects can be asserted.
type exemptionFixture struct {
	club    *fin.Club
	svc     *accounting.Service
	h       *InvoiceHandler
	mail    *email.MockSender
	dues    string // flat membership price item
	slipLow string // slip_fee tier 0–2.5 m
	slipHi  string // slip_fee tier 2.5–4 m
	service string // flat service item (not exempted anywhere)
}

func newExemptionFixture(t *testing.T) *exemptionFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	mail := &email.MockSender{}
	h := &InvoiceHandler{db: pool, config: &config.Config{}, email: mail, log: zerolog.Nop()}
	h.SetLedger(svc)
	f := &exemptionFixture{club: club, svc: svc, h: h, mail: mail}

	ctx := context.Background()
	item := func(category, name string, amount float64, kind, meta string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, description, amount, unit, is_active,
			                          pricing_kind, tier_dimension, show_in_batch, requires_boat_selection, metadata)
			 VALUES ($1, $2, $3, $3, $4, 'year', true, $5,
			         CASE WHEN $5 = 'tiered' THEN 'beam' END, true, false, $6::jsonb)
			 RETURNING id`,
			club.ID, category, name, amount, kind, meta,
		).Scan(&id); err != nil {
			t.Fatalf("price item %s: %v", name, err)
		}
		return id
	}
	f.dues = item("membership", "Medlemskontingent", 450, "flat", `{}`)
	f.slipLow = item("slip_fee", "Plassleie inntil 2,5 m", 2600, "tiered", `{"beam_min":0,"beam_max":2.5}`)
	f.slipHi = item("slip_fee", "Plassleie 2,5–4 m", 3750, "tiered", `{"beam_min":2.5,"beam_max":4}`)
	f.service = item("service", "Slipping", 800, "flat", `{}`)
	return f
}

// slipHolder seeds a member with roles, a boat of the given beam and an
// active slip assignment, so tiered slip rent resolves for them.
func (f *exemptionFixture) slipHolder(t *testing.T, beam float64, roles ...string) fin.Member {
	t.Helper()
	ctx := context.Background()
	m := f.club.NewMember(t, append([]string{"member", "slip_holder"}, roles...)...)
	var boatID, slipID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name, beam_m, length_m) VALUES ($1, $2, 'Synthetic boat', $3, 7) RETURNING id`,
		m.ID, f.club.ID, beam,
	).Scan(&boatID); err != nil {
		t.Fatalf("boat: %v", err)
	}
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO slips (club_id, section, number) VALUES ($1, 'Z', $2) RETURNING id`,
		f.club.ID, "Z"+testutil.RandomHex(3),
	).Scan(&slipID); err != nil {
		t.Fatalf("slip: %v", err)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id) VALUES ($1, $2, $3, $4)`,
		slipID, m.ID, f.club.ID, boatID,
	); err != nil {
		t.Fatalf("slip assignment: %v", err)
	}
	return m
}

func (f *exemptionFixture) call(t *testing.T, fn http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := chi.NewRouter()
	if strings.HasSuffix(path, "/send") {
		r.Method(method, "/invoices/{invoiceID}/send", fn)
	} else {
		r.Method(method, path, fn)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: f.club.Treasurer.ID, ClubID: f.club.ID, Roles: []string{"treasurer"},
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (f *exemptionFixture) configure(t *testing.T, account string, rules ...exemptionRule) {
	t.Helper()
	rec := f.call(t, f.h.HandlePutExemptions, http.MethodPut, "/settings/economy/exemptions",
		putExemptionsRequest{ExpenseAccount: account, Rules: rules})
	if rec.Code != http.StatusOK {
		t.Fatalf("configure exemptions: %d %s", rec.Code, rec.Body.String())
	}
}

func catRule(role, category string) exemptionRule {
	return exemptionRule{Role: role, Category: &category}
}

func (f *exemptionFixture) bulk(t *testing.T, req bulkInvoiceRequest) bulkInvoiceResponse {
	t.Helper()
	req.FiscalPeriodID = f.club.PeriodID
	req.DueDate = "2026-06-30"
	rec := f.call(t, f.h.HandleBulkCreateInvoices, http.MethodPost, "/invoices/bulk", req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk: %d %s", rec.Code, rec.Body.String())
	}
	var out bulkInvoiceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode bulk: %v", err)
	}
	return out
}

type storedLine struct {
	amount  float64
	role    string
	account string
	item    string
}

func (f *exemptionFixture) lines(t *testing.T, invoiceID string) (total float64, lines []storedLine) {
	t.Helper()
	ctx := context.Background()
	if err := f.club.Pool.QueryRow(ctx, `SELECT total_amount FROM invoices WHERE id = $1`, invoiceID).Scan(&total); err != nil {
		t.Fatalf("invoice total: %v", err)
	}
	rows, err := f.club.Pool.Query(ctx,
		`SELECT il.line_total, COALESCE(il.exemption_role, ''), COALESCE(a.code, ''), COALESCE(il.price_item_id::text, '')
		   FROM invoice_lines il LEFT JOIN accounts a ON a.id = il.account_id
		  WHERE il.invoice_id = $1 ORDER BY il.line_total DESC`, invoiceID)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l storedLine
		if err := rows.Scan(&l.amount, &l.role, &l.account, &l.item); err != nil {
			t.Fatalf("scan line: %v", err)
		}
		lines = append(lines, l)
	}
	return total, lines
}

func (f *exemptionFixture) createdFor(t *testing.T, resp bulkInvoiceResponse, userID string) string {
	t.Helper()
	for _, c := range resp.Created {
		if c.UserID == userID {
			return c.InvoiceID
		}
	}
	t.Fatalf("no invoice created for %s: %+v", userID, resp)
	return ""
}

// netByAccount sums debit minus credit per account over posted and voided
// entries (bookedEntryFilter semantics) for the club.
func (f *exemptionFixture) netByAccount(t *testing.T) map[string]float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT a.code, SUM(jl.debit) - SUM(jl.credit)
		   FROM journal_lines jl
		   JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.status IN ('posted', 'voided')
		  GROUP BY a.code`, f.club.ID)
	if err != nil {
		t.Fatalf("net by account: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var code string
		var net float64
		if err := rows.Scan(&code, &net); err != nil {
			t.Fatalf("scan net: %v", err)
		}
		out[code] = net
	}
	return out
}

func TestExemptionsNoConfigBillsEveryoneInFull(t *testing.T) {
	f := newExemptionFixture(t)
	treasurer := f.slipHolder(t, 2.2, "treasurer")

	resp := f.bulk(t, bulkInvoiceRequest{
		UserIDs: []string{treasurer.ID}, PriceItemIDs: []string{f.dues}, BeamCategories: []string{"slip_fee"},
	})
	if len(resp.Exemptions) != 0 || len(resp.Warnings) != 0 {
		t.Fatalf("no exemptions configured, got %+v / %v", resp.Exemptions, resp.Warnings)
	}
	total, lines := f.lines(t, f.createdFor(t, resp, treasurer.ID))
	if total != 3050 || len(lines) != 2 {
		t.Errorf("total %.2f with %d lines, want 3050 over dues + slip rent", total, len(lines))
	}
}

// A board member whose role is exempt from slip rent gets a faktura that
// bills dues in full and offsets the slip rent; it books gross, never owes
// anything on the slip, and a fully exempt faktura settles on issue.
func TestExemptionsOffsetSlipRentForBoardRole(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", catRule("treasurer", "slip_fee"), catRule("chair", "slip_fee"))

	treasurer := f.slipHolder(t, 3.1, "treasurer")
	chair := f.slipHolder(t, 2.2, "chair")
	ordinary := f.slipHolder(t, 2.2)

	// Preview lists the exemptions without creating anything.
	preview := f.bulk(t, bulkInvoiceRequest{
		UserIDs:        []string{treasurer.ID, chair.ID, ordinary.ID},
		PriceItemIDs:   []string{f.dues},
		BeamCategories: []string{"slip_fee"},
		Preview:        true,
	})
	if len(preview.Created) != 0 || len(preview.Exemptions) != 2 {
		t.Fatalf("preview = %+v, want 2 exemptions and nothing created", preview)
	}
	var invoices int
	_ = f.club.Pool.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE club_id = $1`, f.club.ID).Scan(&invoices)
	if invoices != 0 {
		t.Fatalf("preview created %d invoices", invoices)
	}

	// Mixed faktura: dues billed, slip rent offset.
	resp := f.bulk(t, bulkInvoiceRequest{
		UserIDs:        []string{treasurer.ID, chair.ID, ordinary.ID},
		PriceItemIDs:   []string{f.dues},
		BeamCategories: []string{"slip_fee"},
	})
	total, lines := f.lines(t, f.createdFor(t, resp, treasurer.ID))
	if total != 450 || len(lines) != 3 {
		t.Fatalf("treasurer faktura total %.2f with %d lines, want 450 over 3 lines", total, len(lines))
	}
	exempt := lines[len(lines)-1]
	if exempt.amount != -3750 || exempt.role != "treasurer" || exempt.account != "5000" || exempt.item != f.slipHi {
		t.Errorf("exemption line = %+v, want -3750 for the 2.5–4 m tier on 5000 by treasurer", exempt)
	}
	if total, _ := f.lines(t, f.createdFor(t, resp, ordinary.ID)); total != 3050 {
		t.Errorf("ordinary member total %.2f, want 3050", total)
	}

	// Send the treasurer's faktura: it books gross. Dues hit 1500 and 3100;
	// the slip rent is credited to 3120 and debited to 5000 with no 1500.
	inv := f.createdFor(t, resp, treasurer.ID)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET pdf_data = '\x25504446'::bytea WHERE id = $1`, inv); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	if rec := f.call(t, f.h.HandleSendInvoice, http.MethodPost, "/invoices/"+inv+"/send", nil); rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	net := f.netByAccount(t)
	if net["1500"] != 450 || net["3100"] != -450 || net["3120"] != -3750 || net["5000"] != 3750 {
		t.Errorf("GL nets = %v, want 1500 +450, 3100 -450, 3120 -3750, 5000 +3750", net)
	}
	var debit, credit float64
	_ = f.club.Pool.QueryRow(context.Background(),
		`SELECT SUM(jl.debit), SUM(jl.credit) FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
		  WHERE je.club_id = $1`, f.club.ID).Scan(&debit, &credit)
	if debit != credit {
		t.Errorf("trial balance off: debit %.2f credit %.2f", debit, credit)
	}
}

func TestExemptionsFullyExemptFakturaSettlesOnIssue(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", catRule("harbor_master", "slip_fee"))
	hm := f.slipHolder(t, 2.2, "harbor_master")

	resp := f.bulk(t, bulkInvoiceRequest{UserIDs: []string{hm.ID}, BeamCategories: []string{"slip_fee"}})
	inv := f.createdFor(t, resp, hm.ID)
	total, _ := f.lines(t, inv)
	if total != 0 {
		t.Fatalf("total %.2f, want 0", total)
	}
	ctx := context.Background()
	if _, err := f.club.Pool.Exec(ctx, `UPDATE invoices SET pdf_data = '\x25504446'::bytea WHERE id = $1`, inv); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	if rec := f.call(t, f.h.HandleSendInvoice, http.MethodPost, "/invoices/"+inv+"/send", nil); rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}

	// Never owed: not overdue, not in reminders or payables, no Vipps link.
	var owed, vippsAllowed bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT `+finance.OwedByMemberSQL("i")+`, `+vippsAllowedExpr+`
		   FROM invoices i JOIN clubs c ON c.id = i.club_id WHERE i.id = $1`, inv,
	).Scan(&owed, &vippsAllowed); err != nil {
		t.Fatalf("owed check: %v", err)
	}
	if owed || vippsAllowed {
		t.Errorf("fully exempt faktura owed=%v vipps=%v, want neither", owed, vippsAllowed)
	}
	if got := f.h.ensureFakturaOrderToken(ctx, f.club.ID, inv, true); got != "" {
		t.Errorf("Vipps order token minted for a zero faktura")
	}

	// The email asks for nothing and has no KID.
	if len(f.mail.Calls) != 1 {
		t.Fatalf("emails sent = %d, want 1", len(f.mail.Calls))
	}
	body := f.mail.Calls[0].HTML
	if !strings.Contains(body, "ikke betale noe") || strings.Contains(body, "KID") {
		t.Errorf("zero faktura email should say nothing is due and carry no KID")
	}

	// Booked gross with no receivable.
	net := f.netByAccount(t)
	if net["1500"] != 0 || net["3120"] != -2600 || net["5000"] != 2600 {
		t.Errorf("GL nets = %v, want 3120 -2600, 5000 +2600 and no 1500", net)
	}
}

func TestExemptionsMultipleRolesAndUntickedPreview(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", catRule("treasurer", "slip_fee"), catRule("board", "slip_fee"),
		catRule("treasurer", "membership"))
	both := f.slipHolder(t, 2.2, "treasurer", "board")
	unticked := f.slipHolder(t, 2.2, "board")

	resp := f.bulk(t, bulkInvoiceRequest{
		UserIDs: []string{both.ID, unticked.ID}, PriceItemIDs: []string{f.dues}, BeamCategories: []string{"slip_fee"},
		SkipExemptions: []string{unticked.ID + ":" + f.slipLow},
	})
	// A member holding two exempting roles is exempted once per line, under
	// the first role in board order, and both lines net to zero.
	total, lines := f.lines(t, f.createdFor(t, resp, both.ID))
	if total != 0 || len(lines) != 4 {
		t.Fatalf("two-role member total %.2f over %d lines, want 0 over 4", total, len(lines))
	}
	for _, l := range lines {
		if l.role != "" && l.role != "treasurer" {
			t.Errorf("exemption role %q, want treasurer (first in board order)", l.role)
		}
	}
	// The unticked exemption is billed in full.
	if total, _ := f.lines(t, f.createdFor(t, resp, unticked.ID)); total != 3050 {
		t.Errorf("unticked exemption total %.2f, want 3050 billed", total)
	}
}

func TestExemptionsFollowCurrentRoles(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", catRule("secretary", "slip_fee"))
	sec := f.slipHolder(t, 2.2, "secretary")

	first := f.bulk(t, bulkInvoiceRequest{UserIDs: []string{sec.ID}, BeamCategories: []string{"slip_fee"}})
	if total, _ := f.lines(t, f.createdFor(t, first, sec.ID)); total != 0 {
		t.Fatalf("secretary total %.2f, want 0", total)
	}

	// The role is removed; the next run bills normally. A new period avoids
	// the per-line duplicate check.
	ctx := context.Background()
	if _, err := f.club.Pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1 AND role = 'secretary'`, sec.ID); err != nil {
		t.Fatalf("remove role: %v", err)
	}
	var next string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2027, '2027-01-01', '2027-12-31', 'open') RETURNING id`, f.club.ID).Scan(&next); err != nil {
		t.Fatalf("next period: %v", err)
	}
	req := bulkInvoiceRequest{UserIDs: []string{sec.ID}, BeamCategories: []string{"slip_fee"}, FiscalPeriodID: next, DueDate: "2027-06-30"}
	rec := f.call(t, f.h.HandleBulkCreateInvoices, http.MethodPost, "/invoices/bulk", req)
	var out bulkInvoiceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if total, _ := f.lines(t, f.createdFor(t, out, sec.ID)); total != 2600 {
		t.Errorf("after role removal total %.2f, want 2600", total)
	}
}

func TestExemptionsSingleFakturaAndSkip(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", exemptionRule{Role: "treasurer", PriceItemID: &f.service})
	treasurer := f.club.NewMember(t, "member", "treasurer")

	create := func(skip bool) (float64, []string) {
		rec := f.call(t, f.h.HandleCreateInvoice, http.MethodPost, "/invoices/full", createInvoiceFullRequest{
			UserID: treasurer.ID, DueDate: "2026-06-30", FiscalPeriodID: &f.club.PeriodID,
			SkipExemptions: skip,
			Lines: []invoiceLineInput{
				{Description: "Slipping", Quantity: 1, UnitPrice: 800, PriceItemID: &f.service},
				{Description: "Medlemskontingent", Quantity: 1, UnitPrice: 450, PriceItemID: &f.dues},
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var out invoiceResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.TotalAmount, out.Exemptions
	}
	if total, ex := create(false); total != 450 || len(ex) != 1 || ex[0] != "treasurer" {
		t.Errorf("single faktura total %.2f exemptions %v, want 450 with the slipping exempted", total, ex)
	}
	if total, ex := create(true); total != 1250 || len(ex) != 0 {
		t.Errorf("skip_exemptions total %.2f exemptions %v, want 1250 billed in full", total, ex)
	}
}

func TestExemptionsConfigValidationAndIsolation(t *testing.T) {
	f := newExemptionFixture(t)

	bad := []putExemptionsRequest{
		{ExpenseAccount: "5000", Rules: []exemptionRule{catRule("member", "slip_fee")}},    // not a board role
		{ExpenseAccount: "5000", Rules: []exemptionRule{catRule("treasurer", "nonsense")}}, // unknown category
		{ExpenseAccount: "", Rules: []exemptionRule{catRule("treasurer", "slip_fee")}},     // no account
		{ExpenseAccount: "3100", Rules: []exemptionRule{catRule("treasurer", "slip_fee")}}, // revenue, not expense
		{ExpenseAccount: "5000", Rules: []exemptionRule{{Role: "treasurer"}}},              // no target
	}
	for i, req := range bad {
		if rec := f.call(t, f.h.HandlePutExemptions, http.MethodPut, "/settings/economy/exemptions", req); rec.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, want 400 (%s)", i, rec.Code, rec.Body.String())
		}
	}

	f.configure(t, "5000", catRule("treasurer", "slip_fee"))

	// Another club with an identically roled member is unaffected.
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
	otherCfg, err := loadExemptionConfig(context.Background(), f.club.Pool, other.ID)
	if err != nil {
		t.Fatalf("load other club: %v", err)
	}
	if otherCfg.active() || len(otherCfg.rules) != 0 {
		t.Errorf("other club sees exemptions: %+v", otherCfg.rules)
	}

	// Reading back returns exactly what was saved.
	rec := f.call(t, f.h.HandleGetExemptions, http.MethodGet, "/settings/economy/exemptions", nil)
	var got exemptionsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ExpenseAccount != "5000" || len(got.Rules) != 1 || got.Rules[0].Role != "treasurer" || len(got.BoardRoles) == 0 {
		t.Errorf("GET = %+v", got)
	}

	// Clearing the rules and the account is allowed.
	f.configure(t, "")
	cfg, _ := loadExemptionConfig(context.Background(), f.club.Pool, f.club.ID)
	if len(cfg.rules) != 0 || cfg.expenseAccountCode != "" {
		t.Errorf("after clearing: %+v", cfg)
	}
}

func TestExemptionsWithoutValidAccountWarnAndBillInFull(t *testing.T) {
	f := newExemptionFixture(t)
	f.configure(t, "5000", catRule("treasurer", "slip_fee"))
	// The account is deactivated after the config was saved.
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE accounts SET is_active = false WHERE club_id = $1 AND code = '5000'`, f.club.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	treasurer := f.slipHolder(t, 2.2, "treasurer")
	resp := f.bulk(t, bulkInvoiceRequest{UserIDs: []string{treasurer.ID}, BeamCategories: []string{"slip_fee"}})
	if len(resp.Warnings) == 0 || len(resp.Exemptions) != 0 {
		t.Fatalf("want a warning and no exemptions, got %+v", resp)
	}
	if total, _ := f.lines(t, f.createdFor(t, resp, treasurer.ID)); total != 2600 {
		t.Errorf("total %.2f, want 2600 billed in full", total)
	}
}
