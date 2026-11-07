package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/brygge-klubb/brygge/internal/config"
)

// Address search and verification for the join form (BRY-639). The form
// looks the home address up in Kartverket's open address register through
// GET /join/address-search, so the club's CSP stays unchanged and the
// visitor's typing never reaches a third party directly. POST /join sends the
// picked address's id; the server looks that id up again and stores the
// register's street, postal code and city, never the client's copy, and
// derives is_local from the address's municipality. Whenever the register
// cannot answer, the typed address is saved unverified: the register is a
// convenience, never a gate.

const (
	// addressLookupTimeout bounds one search or re-lookup, both of the
	// register's requests together.
	addressLookupTimeout = 3 * time.Second

	addressSearchMinRunes = 3
	addressSearchMaxRunes = 100
	addressSearchMax      = 8

	// addressSearchCacheTTL keeps a repeated query (a visitor deleting and
	// retyping, or two visitors on the same street) off the register.
	addressSearchCacheTTL = 10 * time.Minute

	addressMaxBody = 1 << 20

	// addressUpstreamPerMinute caps the club's calls to the register across
	// every visitor, so many IPs together cannot make the club's server
	// hammer a free public service. Over it, the form falls back to manual
	// entry; so it does when the counter cannot be read.
	addressUpstreamPerMinute = 300
	addressUpstreamWindow    = time.Minute
	addressUpstreamKey       = "join:address:upstream"

	codeAddressQueryInvalid = "address_query_invalid"
)

// joinAddress is one address of the register as the join form shows it. ID is
// what POST /join sends back so the server can look the same address up.
type joinAddress struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Street        string `json:"street"`
	PostalCode    string `json:"postal_code"`
	City          string `json:"city"`
	Kommunenummer string `json:"kommunenummer"`
}

// addressRegister is the address register the join form searches. Lookup
// returns the one address id names, or an error when it cannot.
type addressRegister interface {
	Search(ctx context.Context, query string, limit int) ([]joinAddress, error)
	Lookup(ctx context.Context, id string) (joinAddress, error)
}

// errAddressNotFound is a re-lookup that found no address with the id.
var errAddressNotFound = errors.New("address not found in the register")

// kartverketAddresses is the register behind Kartverket's Adresse-API
// (ws.geonorge.no/adresser/v1, CC BY 4.0, no key).
type kartverketAddresses struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

// newKartverketAddresses builds the register client. It is deliberately not
// traced: an otelhttp span records the full URL, and the URL carries what the
// visitor typed. It never follows a redirect to another host.
func newKartverketAddresses(cfg *config.Config) *kartverketAddresses {
	base := config.DefaultKartverketAddressURL
	userAgent := "Brygge"
	if cfg != nil {
		if cfg.KartverketAddressURL != "" {
			base = cfg.KartverketAddressURL
		}
		if cfg.Domain != "" {
			userAgent = "Brygge (+https://" + cfg.Domain + ")"
		}
	}
	base = strings.TrimRight(base, "/")
	host := ""
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	return &kartverketAddresses{
		baseURL:   base,
		userAgent: userAgent,
		client: &http.Client{
			Timeout: addressLookupTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Host != host || len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// kartverketAddress is the part of one /sok hit the join form uses.
type kartverketAddress struct {
	Adressetekst  string `json:"adressetekst"`
	Adressekode   int    `json:"adressekode"`
	Nummer        int    `json:"nummer"`
	Bokstav       string `json:"bokstav"`
	Kommunenummer string `json:"kommunenummer"`
	Gardsnummer   int    `json:"gardsnummer"`
	Bruksnummer   int    `json:"bruksnummer"`
	Festenummer   int    `json:"festenummer"`
	Undernummer   *int   `json:"undernummer"`
	Objtype       string `json:"objtype"`
	Postnummer    string `json:"postnummer"`
	Poststed      string `json:"poststed"`
}

// kartverketFields is the /sok filtrer list: only what kartverketAddress
// reads comes back.
var kartverketFields = strings.Join([]string{
	"adresser.adressetekst", "adresser.adressekode", "adresser.nummer", "adresser.bokstav",
	"adresser.kommunenummer", "adresser.gardsnummer", "adresser.bruksnummer", "adresser.festenummer",
	"adresser.undernummer", "adresser.objtype", "adresser.postnummer", "adresser.poststed",
}, ",")

// id is the address's stable identity in the register: a street address
// (vegadresse) is its municipality, street code, number and letter; a
// cadastral address (matrikkeladresse) its municipality and
// gårds-/bruks-/feste-/undernummer. Both are fields /sok filters on exactly.
func (a kartverketAddress) id() string {
	if a.Objtype == "Matrikkeladresse" {
		under := ""
		if a.Undernummer != nil {
			under = strconv.Itoa(*a.Undernummer)
		}
		return fmt.Sprintf("m:%s:%d:%d:%d:%s", a.Kommunenummer, a.Gardsnummer, a.Bruksnummer, a.Festenummer, under)
	}
	return fmt.Sprintf("v:%s:%d:%d:%s", a.Kommunenummer, a.Adressekode, a.Nummer, a.Bokstav)
}

func (a kartverketAddress) joinAddress() joinAddress {
	city := titleCasePlace(a.Poststed)
	return joinAddress{
		ID:            a.id(),
		Label:         fmt.Sprintf("%s, %s %s", a.Adressetekst, a.Postnummer, city),
		Street:        a.Adressetekst,
		PostalCode:    a.Postnummer,
		City:          city,
		Kommunenummer: a.Kommunenummer,
	}
}

// titleCasePlace turns the register's upper-case poststed ("MO I RANA") into
// the spelling people write ("Mo i Rana").
func titleCasePlace(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && (w == "i" || w == "på") {
			continue
		}
		parts := strings.Split(w, "-")
		for j, p := range parts {
			r, size := utf8.DecodeRuneInString(p)
			if size > 0 {
				parts[j] = string(unicode.ToUpper(r)) + p[size:]
			}
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

func (k *kartverketAddresses) sok(ctx context.Context, params url.Values) ([]kartverketAddress, error) {
	params.Set("filtrer", kartverketFields)
	params.Set("asciiKompatibel", "false")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.baseURL+"/sok?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("address register request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", k.userAgent)
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("address register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("address register answered %d", resp.StatusCode)
	}
	var body struct {
		Adresser []kartverketAddress `json:"adresser"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, addressMaxBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("address register response: %w", err)
	}
	return body.Adresser, nil
}

// Search finds addresses as the visitor types: the last word as a prefix
// first ("Fiktivveg 1" finds "Fiktivvegen 12"), then, when that finds
// nothing, a fuzzy search that forgives a typo. A fuzzy search cannot take a
// wildcard, so they are two requests.
func (k *kartverketAddresses) Search(ctx context.Context, query string, limit int) ([]joinAddress, error) {
	n := strconv.Itoa(limit)
	hits, err := k.sok(ctx, url.Values{"sok": {strings.ReplaceAll(query, "*", "") + "*"}, "treffPerSide": {n}})
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		hits, err = k.sok(ctx, url.Values{"sok": {strings.ReplaceAll(query, "*", "")}, "fuzzy": {"true"}, "treffPerSide": {n}})
		if err != nil {
			return nil, err
		}
	}
	out := make([]joinAddress, 0, len(hits))
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, h.joinAddress())
	}
	return out, nil
}

var (
	streetAddressID    = regexp.MustCompile(`^v:(\d{4}):(\d{1,6}):(\d{1,5}):(\p{Lu}{0,2})$`)
	cadastralAddressID = regexp.MustCompile(`^m:(\d{4}):(\d{1,6}):(\d{1,6}):(\d{1,6}):(\d{0,6})$`)
)

// Lookup finds the one address id names. The query filters on every field of
// the id, and only a hit whose own id is the same counts: the register's
// filters are exact, and comparing again keeps "12" from matching "12A".
func (k *kartverketAddresses) Lookup(ctx context.Context, id string) (joinAddress, error) {
	params := url.Values{"treffPerSide": {"50"}}
	if m := streetAddressID.FindStringSubmatch(id); m != nil {
		params.Set("objtype", "Vegadresse")
		params.Set("kommunenummer", m[1])
		params.Set("adressekode", m[2])
		params.Set("nummer", m[3])
		// Without a letter the filter is left out (the number's lettered
		// variants come back too); the id comparison below picks the one.
		if m[4] != "" {
			params.Set("bokstav", m[4])
		}
	} else if m := cadastralAddressID.FindStringSubmatch(id); m != nil {
		params.Set("objtype", "Matrikkeladresse")
		params.Set("kommunenummer", m[1])
		params.Set("gardsnummer", m[2])
		params.Set("bruksnummer", m[3])
		params.Set("festenummer", m[4])
		if m[5] != "" {
			params.Set("undernummer", m[5])
		}
	} else {
		return joinAddress{}, errAddressNotFound
	}
	hits, err := k.sok(ctx, params)
	if err != nil {
		return joinAddress{}, err
	}
	for _, h := range hits {
		if h.id() == id {
			return h.joinAddress(), nil
		}
	}
	return joinAddress{}, errAddressNotFound
}

type addressSearchResponse struct {
	Addresses []joinAddress `json:"addresses"`
	// Fallback tells the form the register could not be searched, so it
	// switches to typing the address by hand.
	Fallback bool `json:"fallback"`
}

// normalizeAddressQuery is the query as searched and cached: one line, single
// spaces, lower case.
func normalizeAddressQuery(q string) string {
	return strings.ToLower(cleanSingleLine(q))
}

func addressSearchCacheKey(q string) string {
	sum := sha256.Sum256([]byte(q))
	return "join:address:" + hex.EncodeToString(sum[:])
}

// HandleAddressSearch is the join form's address type-ahead. A register that
// errors or times out answers 200 with fallback set, never an error, so the
// form moves to manual entry. The query is never logged.
func (h *JoinHandler) HandleAddressSearch(w http.ResponseWriter, r *http.Request) {
	q := normalizeAddressQuery(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < addressSearchMinRunes || n > addressSearchMaxRunes {
		JSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("q must be %d to %d characters", addressSearchMinRunes, addressSearchMaxRunes),
			"code":  codeAddressQueryInvalid,
		})
		return
	}
	ctx := r.Context()
	key := addressSearchCacheKey(q)
	if h.rdb != nil {
		if cached, err := h.rdb.Get(ctx, key).Bytes(); err == nil {
			var resp addressSearchResponse
			if json.Unmarshal(cached, &resp) == nil {
				JSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	if h.addresses == nil || !h.allowUpstream(ctx) {
		JSON(w, http.StatusOK, addressSearchResponse{Addresses: []joinAddress{}, Fallback: true})
		return
	}

	searchCtx, cancel := context.WithTimeout(ctx, h.lookupTimeout())
	defer cancel()
	found, err := h.addresses.Search(searchCtx, q, addressSearchMax)
	if err != nil {
		h.log.Warn().Err(redactURLError(err)).Msg("join: address search failed; the form falls back to manual entry")
		JSON(w, http.StatusOK, addressSearchResponse{Addresses: []joinAddress{}, Fallback: true})
		return
	}
	resp := addressSearchResponse{Addresses: found}
	h.log.Debug().Int("results", len(found)).Msg("join: address search")
	if h.rdb != nil {
		if b, err := json.Marshal(resp); err == nil {
			_ = h.rdb.Set(ctx, key, b, addressSearchCacheTTL).Err()
		}
	}
	JSON(w, http.StatusOK, resp)
}

// allowUpstream takes one call from the club-wide register budget. A
// counter that cannot be read allows nothing.
func (h *JoinHandler) allowUpstream(ctx context.Context) bool {
	if h.rdb == nil {
		return false
	}
	n, err := joinCountScript.Run(ctx, h.rdb, []string{addressUpstreamKey}, addressUpstreamWindow.Milliseconds()).Int64()
	if err != nil {
		h.log.Warn().Err(err).Msg("join: address search budget unreadable; the form falls back to manual entry")
		return false
	}
	if n > addressUpstreamPerMinute {
		h.log.Warn().Int("per_minute", addressUpstreamPerMinute).Msg("join: club-wide address search budget spent; the form falls back to manual entry")
		return false
	}
	return true
}

// redactURLError drops the request URL from a client error: it carries the
// visitor's query.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// lookupTimeout is how long one search or re-lookup may take.
func (h *JoinHandler) lookupTimeout() time.Duration {
	if h.addressTimeout > 0 {
		return h.addressTimeout
	}
	return addressLookupTimeout
}

// verifyJoinAddress looks the picked address up again and, when the register
// confirms it, replaces the typed street, postal code and city with the
// register's and derives is_local from the club's municipality codes. Any
// failure leaves the typed address, unverified, with is_local unknown
// (false): joining never waits on the register.
func (h *JoinHandler) verifyJoinAddress(ctx context.Context, clubID string, in joinInput) joinInput {
	in.addressVerified, in.kommunenummer, in.isLocal = false, "", false
	if in.addressID == "" || h.addresses == nil {
		return in
	}
	lookupCtx, cancel := context.WithTimeout(ctx, h.lookupTimeout())
	defer cancel()
	a, err := h.addresses.Lookup(lookupCtx, in.addressID)
	if err != nil {
		h.log.Warn().Err(redactURLError(err)).Msg("join: address re-lookup failed; saved unverified")
		return in
	}
	var codes []string
	if err := h.db.QueryRow(ctx,
		`SELECT municipality_codes FROM clubs WHERE id = $1`, clubID,
	).Scan(&codes); err != nil {
		h.log.Warn().Err(err).Msg("join: club municipality codes unreadable; address saved unverified")
		return in
	}
	in.addressLine, in.postalCode, in.city = a.Street, a.PostalCode, a.City
	in.addressVerified, in.kommunenummer = true, a.Kommunenummer
	if len(codes) == 0 {
		// A club without municipality codes cannot tell local from not:
		// residency stays unknown (false) rather than computed as
		// non-local, and the kommunenummer is kept for the board.
		h.log.Warn().Msg("join: club has no municipality codes; residency left unknown")
		return in
	}
	in.isLocal = slices.Contains(codes, a.Kommunenummer)
	return in
}
