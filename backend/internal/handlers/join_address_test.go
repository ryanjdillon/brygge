package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/config"
)

// fakeAddressRegister stands in for Kartverket's Adresse-API /sok with a
// handful of made-up addresses. A sok query matches every word as a prefix
// of a word of the address; fuzzy drops each word's last letter first, which
// is enough to forgive the one-letter typo the tests make. The field filters
// are exact, as the real API's are, and a filter left out filters nothing
// (nummer=12 without bokstav finds 12 and 12A).
type fakeAddressRegister struct {
	srv      *httptest.Server
	requests atomic.Int32

	mu    sync.Mutex
	fail  bool
	delay time.Duration
	// redirect, when set, answers every request with a redirect there.
	redirect string
	// lastQuery and lastUA are the newest request's raw query and
	// User-Agent.
	lastQuery string
	lastUA    string
}

func intp(n int) *int { return &n }

var fakeAddresses = []kartverketAddress{
	{Adressetekst: "Fiktivvegen 12", Adressekode: 90001, Nummer: 12, Kommunenummer: "4601", Objtype: "Vegadresse", Postnummer: "5999", Poststed: "TESTBY"},
	{Adressetekst: "Fiktivvegen 12A", Adressekode: 90001, Nummer: 12, Bokstav: "A", Kommunenummer: "4601", Objtype: "Vegadresse", Postnummer: "5999", Poststed: "TESTBY"},
	{Adressetekst: "Påfunnstien 3", Adressekode: 90002, Nummer: 3, Kommunenummer: "0999", Objtype: "Vegadresse", Postnummer: "9999", Poststed: "MO I PÅFUNN"},
	{Adressetekst: "10/2", Gardsnummer: 10, Bruksnummer: 2, Undernummer: intp(1), Kommunenummer: "4601", Objtype: "Matrikkeladresse", Postnummer: "5998", Poststed: "TESTBY-NORD"},
}

func newFakeAddressRegister(t *testing.T) *fakeAddressRegister {
	t.Helper()
	f := &fakeAddressRegister{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAddressRegister) set(fail bool, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail, f.delay = fail, delay
}

func (f *fakeAddressRegister) query() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastQuery
}

func (f *fakeAddressRegister) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.mu.Lock()
	fail, delay, redirect := f.fail, f.delay, f.redirect
	f.lastQuery = r.URL.RawQuery
	f.lastUA = r.UserAgent()
	f.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if fail || r.URL.Path != "/sok" {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	var out []kartverketAddress
	for _, a := range fakeAddresses {
		if fakeMatches(a, q) {
			out = append(out, a)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"adresser": out})
}

func fakeMatches(a kartverketAddress, q map[string][]string) bool {
	exact := map[string]string{
		"objtype": a.Objtype, "kommunenummer": a.Kommunenummer, "bokstav": a.Bokstav,
		"adressekode": itoa(a.Adressekode), "nummer": itoa(a.Nummer),
		"gardsnummer": itoa(a.Gardsnummer), "bruksnummer": itoa(a.Bruksnummer), "festenummer": itoa(a.Festenummer),
	}
	if a.Undernummer != nil {
		exact["undernummer"] = itoa(*a.Undernummer)
	}
	for k, want := range exact {
		if v, ok := q[k]; ok && v[0] != want {
			return false
		}
	}
	if _, ok := q["undernummer"]; ok && a.Undernummer == nil {
		return false
	}
	sok, ok := q["sok"]
	if !ok {
		return true
	}
	words := strings.Fields(strings.ToLower(a.Adressetekst + " " + a.Postnummer + " " + a.Poststed))
	fuzzy := len(q["fuzzy"]) > 0 && q["fuzzy"][0] == "true"
	for _, tok := range strings.Fields(strings.ToLower(strings.TrimSuffix(sok[0], "*"))) {
		if fuzzy && len(tok) > 3 {
			tok = tok[:len(tok)-1]
		}
		found := false
		for _, w := range words {
			if strings.HasPrefix(w, tok) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// newAddressSearchHandler is a JoinHandler with only what the search needs:
// the fake register and a miniredis cache.
// The handler's log goes to logs, so a test can check what was written.
func newAddressSearchHandler(t *testing.T) (*JoinHandler, *fakeAddressRegister, *miniredis.Miniredis) {
	t.Helper()
	h, f, mr, _ := newAddressSearchHandlerWithLog(t)
	return h, f, mr
}

func newAddressSearchHandlerWithLog(t *testing.T) (*JoinHandler, *fakeAddressRegister, *miniredis.Miniredis, *bytes.Buffer) {
	t.Helper()
	f := newFakeAddressRegister(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	logs := &bytes.Buffer{}
	h := &JoinHandler{
		rdb:       rdb,
		log:       zerolog.New(logs).Level(zerolog.DebugLevel),
		addresses: newKartverketAddresses(&config.Config{KartverketAddressURL: f.srv.URL, Domain: "club.example.test"}),
	}
	return h, f, mr, logs
}

func searchAddresses(t *testing.T, h *JoinHandler, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/join/address-search?q="+strings.ReplaceAll(q, " ", "+"), nil)
	rec := httptest.NewRecorder()
	h.HandleAddressSearch(rec, req)
	return rec
}

func TestAddressSearchMapsTheRegister(t *testing.T) {
	h, f, _ := newAddressSearchHandler(t)

	rec := searchAddresses(t, h, "Fiktivvegen 12")
	assertStatus(t, rec, http.StatusOK)
	resp := decodeJSON[addressSearchResponse](t, rec)
	if resp.Fallback || len(resp.Addresses) != 2 {
		t.Fatalf("response = %+v, want two addresses", resp)
	}
	want := joinAddress{
		ID: "v:4601:90001:12:", Label: "Fiktivvegen 12, 5999 Testby",
		Street: "Fiktivvegen 12", PostalCode: "5999", City: "Testby", Kommunenummer: "4601",
	}
	if resp.Addresses[0] != want {
		t.Errorf("first = %+v, want %+v", resp.Addresses[0], want)
	}
	if resp.Addresses[1].ID != "v:4601:90001:12:A" {
		t.Errorf("second id = %q", resp.Addresses[1].ID)
	}
	if q := f.query(); !strings.Contains(q, "treffPerSide=8") || !strings.Contains(q, "sok=fiktivvegen+12%2A") {
		t.Errorf("register query = %q, want a prefix search for 8", q)
	}
	f.mu.Lock()
	ua := f.lastUA
	f.mu.Unlock()
	if ua != "Brygge (+https://club.example.test)" {
		t.Errorf("User-Agent = %q", ua)
	}

	// The register's upper-case poststed is written as people write it, and
	// a cadastral address gets its own id form.
	resp = decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "påfunnstien"))
	if len(resp.Addresses) != 1 || resp.Addresses[0].City != "Mo i Påfunn" {
		t.Errorf("påfunnstien = %+v", resp.Addresses)
	}
	resp = decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "10/2"))
	if len(resp.Addresses) != 1 || resp.Addresses[0].ID != "m:4601:10:2:0:1" || resp.Addresses[0].City != "Testby-Nord" {
		t.Errorf("10/2 = %+v", resp.Addresses)
	}
}

// Nothing for the prefix search: the fuzzy one forgives a typo.
func TestAddressSearchFallsBackToFuzzy(t *testing.T) {
	h, f, _ := newAddressSearchHandler(t)
	resp := decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "fiktivvegenx"))
	if len(resp.Addresses) != 2 {
		t.Fatalf("fuzzy = %+v, want the two Fiktivvegen addresses", resp)
	}
	if n := f.requests.Load(); n != 2 {
		t.Errorf("register requests = %d, want prefix then fuzzy", n)
	}
	if q := f.query(); !strings.Contains(q, "fuzzy=true") || strings.Contains(q, "%2A") {
		t.Errorf("second query = %q, want fuzzy without a wildcard", q)
	}
}

func TestAddressSearchQueryLength(t *testing.T) {
	h, f, _ := newAddressSearchHandler(t)
	for _, q := range []string{"", "ab", "  ab  ", strings.Repeat("x", addressSearchMaxRunes+1)} {
		rec := searchAddresses(t, h, q)
		assertStatus(t, rec, http.StatusBadRequest)
		if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeAddressQueryInvalid {
			t.Errorf("q %q: code %q", q, code)
		}
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("an invalid query reached the register %d times", n)
	}
	assertStatus(t, searchAddresses(t, h, "abc"), http.StatusOK)
}

// A repeated query, however it is spaced or cased, is answered from Redis
// under a hash of the normalised query; the query itself is not in the key.
func TestAddressSearchIsCached(t *testing.T) {
	h, f, mr := newAddressSearchHandler(t)
	searchAddresses(t, h, "Fiktivvegen 12")
	searchAddresses(t, h, "  fiktivvegen   12 ")
	if n := f.requests.Load(); n != 1 {
		t.Errorf("register requests = %d, want 1", n)
	}
	for _, k := range mr.Keys() {
		if strings.Contains(k, "fiktiv") {
			t.Errorf("cache key %q carries the query", k)
		}
	}
	if ttl := mr.TTL(addressSearchCacheKey("fiktivvegen 12")); ttl <= 0 || ttl > addressSearchCacheTTL {
		t.Errorf("cache ttl = %v", ttl)
	}
}

// An unreachable or slow register answers 200 with the fallback flag and no
// addresses, so the form switches to manual entry; nothing is cached.
func TestAddressSearchFallback(t *testing.T) {
	h, f, mr, logs := newAddressSearchHandlerWithLog(t)
	f.set(true, 0)
	rec := searchAddresses(t, h, "Fiktivvegen")
	assertStatus(t, rec, http.StatusOK)
	if resp := decodeJSON[addressSearchResponse](t, rec); !resp.Fallback || resp.Addresses == nil || len(resp.Addresses) != 0 {
		t.Errorf("error response = %+v, want fallback with []", resp)
	}

	f.set(false, 2*time.Second)
	h.addressTimeout = 100 * time.Millisecond
	start := time.Now()
	rec = searchAddresses(t, h, "Påfunnstien")
	if took := time.Since(start); took > time.Second {
		t.Errorf("timeout took %v", took)
	}
	if resp := decodeJSON[addressSearchResponse](t, rec); !resp.Fallback || len(resp.Addresses) != 0 {
		t.Errorf("timeout response = %+v, want fallback", resp)
	}
	for _, k := range mr.Keys() {
		if k != addressUpstreamKey {
			t.Errorf("a fallback was cached: %v", k)
		}
	}
	// Both failures were logged, without what the visitor typed.
	if out := strings.ToLower(logs.String()); strings.Count(out, "address search failed") != 2 ||
		strings.Contains(out, "fiktiv") || strings.Contains(out, "påfunn") || strings.Contains(out, "p%c3%a5funn") {
		t.Errorf("log = %s", logs.String())
	}

	// A redirect to another host is not followed.
	f.set(false, 0)
	other := newFakeAddressRegister(t)
	f.mu.Lock()
	f.redirect = other.srv.URL + "/sok?sok=x"
	f.mu.Unlock()
	if resp := decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "Fiktivvegen 9")); !resp.Fallback {
		t.Errorf("redirect: %+v, want fallback", resp)
	}
	if n := other.requests.Load(); n != 0 {
		t.Errorf("followed a redirect off-host %d times", n)
	}

	// No register configured at all behaves the same.
	h.addresses = nil
	if resp := decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "Fiktivvegen")); !resp.Fallback {
		t.Errorf("no register: %+v", resp)
	}
}

// The club as a whole calls the register at most addressUpstreamPerMinute
// times a minute; over it, or with the counter unreadable, the form falls
// back to manual entry and the register is not called.
func TestAddressSearchClubBudget(t *testing.T) {
	h, f, mr := newAddressSearchHandler(t)
	if err := mr.Set(addressUpstreamKey, strconv.Itoa(addressUpstreamPerMinute)); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(addressUpstreamKey, time.Minute)
	rec := searchAddresses(t, h, "Fiktivvegen")
	assertStatus(t, rec, http.StatusOK)
	if resp := decodeJSON[addressSearchResponse](t, rec); !resp.Fallback {
		t.Errorf("over budget: %+v, want fallback", resp)
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("over budget reached the register %d times", n)
	}

	mr.FastForward(time.Minute + time.Second)
	if resp := decodeJSON[addressSearchResponse](t, searchAddresses(t, h, "Fiktivvegen")); resp.Fallback || len(resp.Addresses) != 2 {
		t.Errorf("next minute: %+v", resp)
	}

	mr.Close()
	rec = searchAddresses(t, h, "Påfunnstien")
	assertStatus(t, rec, http.StatusOK)
	if resp := decodeJSON[addressSearchResponse](t, rec); !resp.Fallback {
		t.Errorf("redis down: %+v, want fallback", resp)
	}
	if n := f.requests.Load(); n != 1 {
		t.Errorf("register requests = %d, want only the in-budget one", n)
	}
}

// Re-lookup is exact: "12" does not find "12A", and an id the register does
// not hold, or that is not an id at all, finds nothing.
func TestAddressLookupIsExact(t *testing.T) {
	f := newFakeAddressRegister(t)
	k := newKartverketAddresses(&config.Config{KartverketAddressURL: f.srv.URL})
	ctx := context.Background()
	for id, street := range map[string]string{
		"v:4601:90001:12:":  "Fiktivvegen 12",
		"v:4601:90001:12:A": "Fiktivvegen 12A",
		"m:4601:10:2:0:1":   "10/2",
	} {
		a, err := k.Lookup(ctx, id)
		if err != nil || a.Street != street || a.ID != id {
			t.Errorf("Lookup(%q) = %+v, %v; want %s", id, a, err, street)
		}
	}
	if q := f.query(); !strings.Contains(q, "undernummer=1") || !strings.Contains(q, "objtype=Matrikkeladresse") {
		t.Errorf("cadastral lookup query = %q", q)
	}
	// A letterless number is looked up without a bokstav filter; the
	// register then answers 12 and 12A, and the id comparison keeps 12.
	if _, err := k.Lookup(ctx, "v:4601:90001:12:"); err != nil {
		t.Fatal(err)
	}
	if q, _ := url.ParseQuery(f.query()); q.Has("bokstav") {
		t.Errorf("letterless lookup query = %q, want no bokstav filter", q)
	}
	for _, id := range []string{"v:4601:90001:13:", "v:0999:90001:12:", "x:1", "v:4601:90001:12:A:extra", "'; DROP"} {
		if _, err := k.Lookup(ctx, id); err != errAddressNotFound {
			t.Errorf("Lookup(%q) err = %v, want not found", id, err)
		}
	}
}

// The vectors live in testdata so frontend/src/lib/__tests__/phone.test.ts
// checks its mirror of the rules against the same table.
func TestNormalizePhone(t *testing.T) {
	raw, err := os.ReadFile("testdata/phone_vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vectors struct {
		Valid   map[string]string `json:"valid"`
		Invalid []string          `json:"invalid"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if len(vectors.Valid) == 0 || len(vectors.Invalid) == 0 {
		t.Fatal("empty vector table")
	}
	for in, want := range vectors.Valid {
		if got, ok := normalizePhone(in); !ok || got != want {
			t.Errorf("normalizePhone(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range vectors.Invalid {
		if got, ok := normalizePhone(in); ok {
			t.Errorf("normalizePhone(%q) = %q, want invalid", in, got)
		}
	}
}

// joinedApplication is the stored application's address fields.
type joinedApplication struct {
	street, postal, city string
	isLocal, verified    bool
	kommune              *string
	verifiedAt           *time.Time
}

func (e *joinEnv) application(t *testing.T, addr string) joinedApplication {
	t.Helper()
	var a joinedApplication
	if err := e.db.QueryRow(context.Background(),
		`SELECT address_line, postal_code, city, is_local, address_verified, address_kommunenummer, address_verified_at
		   FROM membership_applications WHERE club_id = $1 AND email = $2`, e.clubID, addr,
	).Scan(&a.street, &a.postal, &a.city, &a.isLocal, &a.verified, &a.kommune, &a.verifiedAt); err != nil {
		t.Fatalf("application: %v", err)
	}
	return a
}

// A picked address is looked up again: the register's street, postal code
// and city replace whatever the client sent, and residency follows from the
// club's municipality codes, whatever is_local the client claims. Confirming
// copies all of it to the user.
func TestJoinAddressIDVerifies(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()

	local := joinBody("local.addr@example.com")
	local["address_id"] = "v:4601:90001:12:A"
	local["address_line"] = "Something the client made up 1"
	local["postal_code"] = "0000"
	local["city"] = "Elsewhere"
	local["is_local"] = false
	assertStatus(t, e.do(t, http.MethodPost, "/join", local), http.StatusAccepted)
	a := e.application(t, "local.addr@example.com")
	if a.street != "Fiktivvegen 12A" || a.postal != "5999" || a.city != "Testby" {
		t.Errorf("address = %+v, want the register's", a)
	}
	if !a.verified || !a.isLocal || a.kommune == nil || *a.kommune != "4601" || a.verifiedAt == nil {
		t.Errorf("verification = %+v, want verified and local in 4601", a)
	}

	away := joinBody("away.addr@example.com")
	away["address_id"] = "v:0999:90002:3:"
	away["is_local"] = true
	assertStatus(t, e.do(t, http.MethodPost, "/join", away), http.StatusAccepted)
	a = e.application(t, "away.addr@example.com")
	if !a.verified || a.isLocal || a.street != "Påfunnstien 3" || a.kommune == nil || *a.kommune != "0999" {
		t.Errorf("out-of-municipality = %+v, want verified and not local", a)
	}

	token := e.lastConfirmToken(t, "local.addr@example.com")
	assertStatus(t, e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": token}), http.StatusOK)
	var (
		street        string
		isLocal       bool
		verified      *bool
		kommune       *string
		verifiedAt    *time.Time
		appVerifiedAt time.Time
		userID, appID string
	)
	if err := e.db.QueryRow(ctx,
		`SELECT id, address_line, is_local, address_verified, address_kommunenummer, address_verified_at
		   FROM users WHERE club_id = $1 AND email = 'local.addr@example.com'`, e.clubID,
	).Scan(&userID, &street, &isLocal, &verified, &kommune, &verifiedAt); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := e.db.QueryRow(ctx,
		`SELECT id, address_verified_at FROM membership_applications WHERE club_id = $1 AND email = 'local.addr@example.com'`, e.clubID,
	).Scan(&appID, &appVerifiedAt); err != nil {
		t.Fatalf("application: %v", err)
	}
	if street != "Fiktivvegen 12A" || !isLocal || verified == nil || !*verified || kommune == nil || *kommune != "4601" ||
		verifiedAt == nil || !verifiedAt.Equal(appVerifiedAt) {
		t.Errorf("user = %s local=%v verified=%v kommune=%v at=%v", street, isLocal, verified, kommune, verifiedAt)
	}

	// The board (or the member) editing a verified address leaves an address
	// the register never saw: the verification goes with the old one.
	mustExec(t, e.db, ctx, `UPDATE users SET address_line = 'Fiktivvegen 99' WHERE id = $1`, userID)
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND address_verified IS NULL
	                      AND address_kommunenummer IS NULL AND address_verified_at IS NULL`, userID); n != 1 {
		t.Error("an edited address kept its verification")
	}
}

// The register down at submit, an id it does not know, or no id at all: the
// typed address is saved unverified, residency unknown, and the join goes
// through.
func TestJoinAddressUnverified(t *testing.T) {
	e := setupJoinEnv(t)
	e.h.addressTimeout = 200 * time.Millisecond

	cases := []struct {
		addr  string
		id    string
		setup func()
	}{
		{"manual@example.com", "", func() {}},
		{"down@example.com", "v:4601:90001:12:", func() { e.register.set(true, 0) }},
		{"slow@example.com", "v:4601:90001:12:", func() { e.register.set(false, 2*time.Second) }},
		{"unknown@example.com", "v:4601:90001:77:", func() { e.register.set(false, 0) }},
	}
	for _, c := range cases {
		c.setup()
		b := joinBody(c.addr)
		b["is_local"] = true
		if c.id != "" {
			b["address_id"] = c.id
		}
		assertStatus(t, e.do(t, http.MethodPost, "/join", b), http.StatusAccepted)
		a := e.application(t, c.addr)
		if a.verified || a.isLocal || a.kommune != nil || a.verifiedAt != nil {
			t.Errorf("%s: %+v, want unverified with residency unknown", c.addr, a)
		}
		if a.street != "Testvegen 1" || a.postal != "0001" || a.city != "Testby" {
			t.Errorf("%s: address = %+v, want the typed one", c.addr, a)
		}
		if len(e.mailsTo(c.addr)) != 1 {
			t.Errorf("%s: no confirmation sent", c.addr)
		}
	}
}

// Erasing a member clears the register check with the address.
func TestEraseClearsAddressVerification(t *testing.T) {
	e := setupJoinEnv(t)
	ctx := context.Background()
	userID := e.seedUser(t, "erase.addr@example.com", false, "member")
	mustExec(t, e.db, ctx,
		`UPDATE users SET address_verified = true, address_kommunenummer = '4601', address_verified_at = now() WHERE id = $1`, userID)
	if err := accounting.EraseMemberMembershipData(ctx, e.db, userID); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND address_verified IS NULL
	                      AND address_kommunenummer IS NULL AND address_verified_at IS NULL`, userID); n != 1 {
		t.Error("erasure left the address verification")
	}
}

// A club with no municipality codes cannot say who is local: the address is
// still verified, but residency stays unknown rather than computed as
// non-local.
func TestJoinAddressWithoutMunicipalityCodes(t *testing.T) {
	e := setupJoinEnv(t)
	mustExec(t, e.db, context.Background(), `UPDATE clubs SET municipality_codes = '{}' WHERE id = $1`, e.clubID)
	b := joinBody("nocodes@example.com")
	b["address_id"] = "v:4601:90001:12:"
	assertStatus(t, e.do(t, http.MethodPost, "/join", b), http.StatusAccepted)
	a := e.application(t, "nocodes@example.com")
	if !a.verified || a.isLocal || a.kommune == nil || *a.kommune != "4601" || a.street != "Fiktivvegen 12" {
		t.Errorf("application = %+v, want verified with residency unknown", a)
	}
}
