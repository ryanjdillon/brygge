package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// clubs.automatic_booking (ACC-13) is off for a new club, is read by the
// settings GET, is written only when the PATCH names it, and an unrelated PATCH
// leaves it alone. Each PATCH that names it audits the old and new value.
func TestIntegration_AutomaticBookingSetting(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	club := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	call := func(method, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	enabled := func() bool {
		t.Helper()
		rec := call(http.MethodGet, "", h.HandleGetFinancialSettings)
		if rec.Code != http.StatusOK {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		v, ok := got["automatic_booking"].(bool)
		if !ok {
			t.Fatalf("automatic_booking missing from the settings response: %s", rec.Body)
		}
		return v
	}
	patch := func(body string) {
		t.Helper()
		if rec := call(http.MethodPatch, body, h.HandleUpdateFinancialSettings); rec.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if enabled() {
		t.Fatal("a new club has automatic booking on")
	}
	patch(`{"automatic_booking":true}`)
	if !enabled() {
		t.Error("automatic_booking:true was not stored")
	}
	patch(`{"phone":"+4700000002"}`)
	if !enabled() {
		t.Error("an unrelated PATCH switched automatic booking off")
	}
	patch(`{"automatic_booking":false}`)
	if enabled() {
		t.Error("automatic_booking:false was not stored")
	}
	patch(`{"automatic_booking":false}`)

	// The audit row carries the switch's old and new value (BRY-568), and only
	// when the PATCH names it.
	rows, err := pool.Query(context.Background(),
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at, id`,
		club.ID, audit.ActionFinancialSettingsUpdated)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type change struct{ Old, New bool }
	var got []*change
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var d struct {
			Fields           []string
			AutomaticBooking *change `json:"automatic_booking"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		if named := slices.Contains(d.Fields, "automatic_booking"); named != (d.AutomaticBooking != nil) {
			t.Errorf("audit row names the field %v but carries values %v: %s", named, d.AutomaticBooking, raw)
		}
		got = append(got, d.AutomaticBooking)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []*change{
		{Old: false, New: true},
		nil, // the phone PATCH omits the field: no old/new key
		{Old: true, New: false},
		{Old: false, New: false},
	}
	if len(got) != len(want) {
		t.Fatalf("audit rows = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if (got[i] == nil) != (want[i] == nil) || (got[i] != nil && *got[i] != *want[i]) {
			t.Errorf("audit row %d automatic_booking = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A PATCH naming all three audited fields reads their old values in one
// SELECT; each lands in its own old/new pair (BRY-568).
func TestIntegration_SettingsAuditAllOldValues(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	insertItem := func(name string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, pricing_kind)
			 VALUES ($1, 'membership', $2, 500, 'once', true, 'flat') RETURNING id`,
			club.ID, name,
		).Scan(&id); err != nil {
			t.Fatalf("seed price item: %v", err)
		}
		return id
	}
	first, second := insertItem("Synthetic join fee A"), insertItem("Synthetic join fee B")
	if _, err := pool.Exec(ctx,
		`UPDATE clubs SET join_price_item_id = $2, municipality_codes = ARRAY['4601'], automatic_booking = true
		 WHERE id = $1`, club.ID, first); err != nil {
		t.Fatal(err)
	}

	body := `{"join_price_item_id":"` + second + `","municipality_codes":["4601","4602"],"automatic_booking":false}`
	req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
	rec := httptest.NewRecorder()
	h.HandleUpdateFinancialSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}

	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2`,
		club.ID, audit.ActionFinancialSettingsUpdated,
	).Scan(&raw); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	var d struct {
		JoinPriceItemID   struct{ Old, New string }   `json:"join_price_item_id"`
		MunicipalityCodes struct{ Old, New []string } `json:"municipality_codes"`
		AutomaticBooking  struct{ Old, New bool }     `json:"automatic_booking"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.JoinPriceItemID.Old != first || d.JoinPriceItemID.New != second {
		t.Errorf("join_price_item_id = %+v, want %s -> %s", d.JoinPriceItemID, first, second)
	}
	if !slices.Equal(d.MunicipalityCodes.Old, []string{"4601"}) || !slices.Equal(d.MunicipalityCodes.New, []string{"4601", "4602"}) {
		t.Errorf("municipality_codes = %+v, want [4601] -> [4601 4602]", d.MunicipalityCodes)
	}
	if !d.AutomaticBooking.Old || d.AutomaticBooking.New {
		t.Errorf("automatic_booking = %+v, want true -> false", d.AutomaticBooking)
	}
}
