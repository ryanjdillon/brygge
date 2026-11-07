package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func TestNormalizeMunicipalityCodes(t *testing.T) {
	twenty := make([]string, 0, 21)
	for i := range 20 {
		twenty = append(twenty, fmt.Sprintf("%04d", 9000+i))
	}
	tests := []struct {
		name string
		in   []string
		want []string
		ok   bool
	}{
		{"empty clears", []string{}, []string{}, true},
		{"trimmed and deduplicated in order", []string{" 9001", "9002", "9001 ", "9002"}, []string{"9001", "9002"}, true},
		{"twenty", twenty, twenty, true},
		{"twenty with a repeat", append(slices.Clone(twenty), "9000"), twenty, true},
		{"twenty-one", append(slices.Clone(twenty), "9999"), nil, false},
		{"three digits", []string{"901"}, nil, false},
		{"five digits", []string{"90011"}, nil, false},
		{"letters", []string{"90a1"}, nil, false},
		{"blank", []string{""}, nil, false},
		{"non-ascii digits", []string{"٩٠٠١"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := slices.Clone(tt.in)
			ok := normalizeMunicipalityCodes(&in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && !slices.Equal(in, tt.want) {
				t.Errorf("codes = %v, want %v", in, tt.want)
			}
		})
	}
	if !normalizeMunicipalityCodes(nil) {
		t.Error("an omitted list was refused")
	}
}

// fakeKommuneinfo serves the kommuneinfo shape for the synthetic codes it
// knows; the tests never call the real API.
func fakeKommuneinfo(t *testing.T, names map[string]string, seenUA *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenUA != nil {
			seenUA.Store(r.Header.Get("User-Agent"))
		}
		code, ok := strings.CutPrefix(r.URL.Path, "/v1/kommuner/")
		switch {
		case !ok:
			http.NotFound(w, r)
		case code == "9500":
			w.WriteHeader(http.StatusServiceUnavailable)
		case code == "9501":
			_, _ = w.Write([]byte(`{"kommunenummer":"9502","kommunenavnNorsk":"Another"}`))
		case code == "9503":
			_, _ = w.Write([]byte(`not json`))
		case code == "9504":
			http.Redirect(w, r, "http://elsewhere.example.invalid/v1/kommuner/9504", http.StatusFound)
		case names[code] != "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kommunenummer": code, "kommunenavn": names[code], "kommunenavnNorsk": names[code],
				"fylkesnavn": "Syntetisk fylke",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKartverketMunicipalitiesName(t *testing.T) {
	var ua atomic.Value
	srv := fakeKommuneinfo(t, map[string]string{"9001": "Synthetic Øy"}, &ua)
	k := newKartverketMunicipalities(&config.Config{KartverketMunicipalityURL: srv.URL + "/v1/", Domain: "club.example.test"})
	ctx := context.Background()

	name, err := k.Name(ctx, "9001")
	if err != nil || name != "Synthetic Øy" {
		t.Fatalf("found code = %q, %v", name, err)
	}
	if got := ua.Load(); got != "Brygge (+https://club.example.test)" {
		t.Errorf("User-Agent = %v", got)
	}
	if _, err := k.Name(ctx, "9002"); !errors.Is(err, errMunicipalityNotFound) {
		t.Errorf("unknown code: %v, want not found", err)
	}
	for _, code := range []string{"9500", "9501", "9503", "9504"} {
		if _, err := k.Name(ctx, code); err == nil || errors.Is(err, errMunicipalityNotFound) {
			t.Errorf("%s: err = %v, want a failure that is not 'not found'", code, err)
		}
	}
	if def := newKartverketMunicipalities(nil); def.baseURL != config.DefaultKartverketMunicipalityURL || def.userAgent != "Brygge" {
		t.Errorf("default client = %q %q", def.baseURL, def.userAgent)
	}
}

type countingRegister struct {
	calls atomic.Int32
	name  string
	err   error
}

func (c *countingRegister) Name(context.Context, string) (string, error) {
	c.calls.Add(1)
	return c.name, c.err
}

// Found and unknown answers are cached for a day; a failure is asked again.
func TestMunicipalityNamesCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)
	reg := &countingRegister{name: "Synthetic"}
	m := newMunicipalityNames(reg)
	m.now = func() time.Time { return now }

	for range 2 {
		if name, status, err := m.lookup(ctx, "9001"); name != "Synthetic" || status != municipalityFound || err != nil {
			t.Fatalf("lookup = %q %q %v", name, status, err)
		}
	}
	if n := reg.calls.Load(); n != 1 {
		t.Errorf("register asked %d times, want 1 (cached)", n)
	}
	now = now.Add(municipalityCacheTTL + time.Second)
	if _, _, err := m.lookup(ctx, "9001"); err != nil || reg.calls.Load() != 2 {
		t.Errorf("an expired entry was not asked again (%d calls, %v)", reg.calls.Load(), err)
	}

	reg.err = errMunicipalityNotFound
	for range 2 {
		if _, status, err := m.lookup(ctx, "9002"); status != municipalityUnknown || err != nil {
			t.Fatalf("unknown = %q %v", status, err)
		}
	}
	if n := reg.calls.Load(); n != 3 {
		t.Errorf("register asked %d times, want 3 (unknown cached)", n)
	}

	reg.err = errors.New("register down")
	for range 2 {
		if _, status, err := m.lookup(ctx, "9003"); status != municipalityUnavailable || err == nil {
			t.Fatalf("failure = %q %v", status, err)
		}
	}
	if n := reg.calls.Load(); n != 5 {
		t.Errorf("register asked %d times, want 5 (failures not cached)", n)
	}
}

func TestHandleLookupMunicipality(t *testing.T) {
	srv := fakeKommuneinfo(t, map[string]string{"9001": "Synthetic Øy"}, nil)
	h := &ClubSettingsHandler{
		log:            zerolog.Nop(),
		municipalities: newMunicipalityNames(newKartverketMunicipalities(&config.Config{KartverketMunicipalityURL: srv.URL + "/v1"})),
	}
	r := chi.NewRouter()
	r.Get("/m/{code}", h.HandleLookupMunicipality)
	get := func(h http.Handler, code string) (int, municipalityLookupResponse, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/m/"+code, nil))
		var body municipalityLookupResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body, rec.Body.String()
	}

	for code, want := range map[string]municipalityLookupResponse{
		"9001": {Code: "9001", Name: "Synthetic Øy", Status: municipalityFound},
		"9002": {Code: "9002", Status: municipalityUnknown},
		"9500": {Code: "9500", Status: municipalityUnavailable},
	} {
		if status, got, raw := get(r, code); status != http.StatusOK || got != want {
			t.Errorf("%s: %d %+v (%s), want 200 %+v", code, status, got, raw, want)
		}
	}
	if status, _, raw := get(r, "901"); status != http.StatusBadRequest || !strings.Contains(raw, codeInvalidMunicipalityCodes) {
		t.Errorf("malformed code: %d %s", status, raw)
	}

	// Without a register the page still gets an answer: the code alone.
	bare := chi.NewRouter()
	bare.Get("/m/{code}", (&ClubSettingsHandler{log: zerolog.Nop()}).HandleLookupMunicipality)
	if status, got, _ := get(bare, "9001"); status != http.StatusOK || got.Status != municipalityUnavailable {
		t.Errorf("no register: %d %+v", status, got)
	}
}

// The municipality codes setting takes four-digit codes, deduplicated, at
// most 20; [] clears it, an omitted field keeps it; each accepted write is
// audited with the old and new list; and it touches only the caller's club.
func TestFinancialSettingsMunicipalityCodes(t *testing.T) { //nolint:cyclop // one scenario walked through every refusal and the accepted values
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := fin.NewClub(t, pool)
	other := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}

	if _, err := pool.Exec(ctx, `UPDATE clubs SET municipality_codes = '{9900}' WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, req)
		return rec
	}
	get := func() []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleGetFinancialSettings(rec, req)
		var s struct {
			MunicipalityCodes []string `json:"municipality_codes"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("get settings: %v (%s)", err, rec.Body)
		}
		if s.MunicipalityCodes == nil {
			t.Fatalf("municipality_codes missing or null: %s", rec.Body)
		}
		return s.MunicipalityCodes
	}
	column := func(clubID string) []string {
		t.Helper()
		var v []string
		if err := pool.QueryRow(ctx, `SELECT municipality_codes FROM clubs WHERE id = $1`, clubID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if got := get(); len(got) != 0 {
		t.Errorf("default = %v, want []", got)
	}

	twentyOne := make([]string, 21)
	for i := range twentyOne {
		twentyOne[i] = fmt.Sprintf(`"%04d"`, 9000+i)
	}
	for name, body := range map[string]string{
		"three digits": `{"municipality_codes":["901"]}`,
		"letters":      `{"municipality_codes":["90a1"]}`,
		"blank":        `{"municipality_codes":[""]}`,
		"twenty-one":   `{"municipality_codes":[` + strings.Join(twentyOne, ",") + `]}`,
		"not a list":   `{"municipality_codes":"9001"}`,
	} {
		rec := update(body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
			continue
		}
		if name == "not a list" {
			continue
		}
		var refusal struct{ Code string }
		if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || refusal.Code != codeInvalidMunicipalityCodes {
			t.Errorf("%s: code %q, want %q", name, refusal.Code, codeInvalidMunicipalityCodes)
		}
		if got := column(club.ID); len(got) != 0 {
			t.Fatalf("%s: a refused list was stored: %v", name, got)
		}
	}

	if rec := update(`{"municipality_codes":[" 9001","9002","9001"]}`); rec.Code != http.StatusOK {
		t.Fatalf("valid list: %d %s", rec.Code, rec.Body)
	}
	want := []string{"9001", "9002"}
	if got := column(club.ID); !slices.Equal(got, want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	if got := get(); !slices.Equal(got, want) {
		t.Errorf("read back %v, want %v", got, want)
	}

	for _, body := range []string{`{"bank_auto_assign":false}`, `{"municipality_codes":null}`} {
		if rec := update(body); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		if got := column(club.ID); !slices.Equal(got, want) {
			t.Errorf("%s changed the codes to %v", body, got)
		}
	}

	if rec := update(`{"municipality_codes":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if got := column(club.ID); len(got) != 0 {
		t.Errorf("[] left %v", got)
	}
	if got := column(other.ID); !slices.Equal(got, []string{"9900"}) {
		t.Errorf("another club's codes changed to %v", got)
	}

	rows, err := pool.Query(ctx,
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at`,
		club.ID, audit.ActionFinancialSettingsUpdated)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type change struct{ Old, New []string }
	var changes []change
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var d struct {
			Fields            []string
			MunicipalityCodes *change `json:"municipality_codes"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		named := slices.Contains(d.Fields, "municipality_codes")
		if named != (d.MunicipalityCodes != nil) {
			t.Errorf("audit row names the field %v but carries values %v: %s", named, d.MunicipalityCodes, raw)
		}
		if d.MunicipalityCodes != nil {
			if d.MunicipalityCodes.Old == nil || d.MunicipalityCodes.New == nil {
				t.Errorf("audit values hold null, want lists: %s", raw)
			}
			changes = append(changes, *d.MunicipalityCodes)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantChanges := []change{{Old: []string{}, New: want}, {Old: want, New: []string{}}}
	if !slices.EqualFunc(changes, wantChanges, func(a, b change) bool {
		return slices.Equal(a.Old, b.Old) && slices.Equal(a.New, b.New)
	}) {
		t.Errorf("audit values = %+v, want %+v", changes, wantChanges)
	}
}
