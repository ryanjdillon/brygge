package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/config"
)

// The club's municipality codes (clubs.municipality_codes, BRY-643) decide
// which verified joiners count as local (BRD-16). The treasurer edits them in
// the settings; the settings page shows each code's name from Kartverket's
// kommuneinfo API through GET /admin/settings/municipalities/{code}. The name
// is a convenience: a lookup that fails shows the code alone and never blocks
// saving, and an unknown code is a warning, not a refusal, so a code issued
// after a merger can be saved before the register knows it.

const (
	// maxMunicipalityCodes caps the list: a club's catchment is a handful of
	// municipalities, and the cap bounds the settings page's name lookups.
	maxMunicipalityCodes = 20

	municipalityLookupTimeout = 3 * time.Second
	municipalityMaxBody       = 1 << 20

	// A name changes only at a merger or a renaming, so a found or unknown
	// answer is kept for a day; a failure is never cached.
	municipalityCacheTTL = 24 * time.Hour

	codeInvalidMunicipalityCodes = "invalid_municipality_codes"

	municipalityFound       = "found"
	municipalityUnknown     = "unknown"
	municipalityUnavailable = "unavailable"
)

var municipalityCodePattern = regexp.MustCompile(`^[0-9]{4}$`)

// normalizeMunicipalityCodes cleans a municipality_codes setting in place:
// each code trimmed and exactly four digits, duplicates dropped in order of
// first appearance, at most maxMunicipalityCodes. It reports false for a list
// that breaks any of these. Unset (nil) passes untouched; an empty list
// clears the setting.
func normalizeMunicipalityCodes(p *[]string) bool {
	if p == nil {
		return true
	}
	out := make([]string, 0, len(*p))
	seen := map[string]bool{}
	for _, c := range *p {
		c = strings.TrimSpace(c)
		if !municipalityCodePattern.MatchString(c) {
			return false
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) > maxMunicipalityCodes {
		return false
	}
	*p = out
	return true
}

// errMunicipalityNotFound is a code the register does not hold.
var errMunicipalityNotFound = errors.New("municipality not found in the register")

// municipalityRegister names a municipality by its four-digit code.
type municipalityRegister interface {
	Name(ctx context.Context, code string) (string, error)
}

// kartverketMunicipalities is the register behind Kartverket's kommuneinfo
// API (ws.geonorge.no/kommuneinfo/v1, free, no key).
type kartverketMunicipalities struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

// newKartverketMunicipalities builds the kommuneinfo client the same way as
// the address register's: untraced, with a User-Agent naming the deployment,
// a timeout, and no redirect to another host.
func newKartverketMunicipalities(cfg *config.Config) *kartverketMunicipalities {
	base := config.DefaultKartverketMunicipalityURL
	userAgent := "Brygge"
	if cfg != nil {
		if cfg.KartverketMunicipalityURL != "" {
			base = cfg.KartverketMunicipalityURL
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
	return &kartverketMunicipalities{
		baseURL:   base,
		userAgent: userAgent,
		client: &http.Client{
			Timeout: municipalityLookupTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Host != host || len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// Name looks code up in kommuneinfo. A 404 (or a 400, the API's answer to a
// well-formed number it does not recognise) is errMunicipalityNotFound; any
// other failure is an error, so the caller shows the code without a name.
func (k *kartverketMunicipalities) Name(ctx context.Context, code string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.baseURL+"/kommuner/"+url.PathEscape(code), nil)
	if err != nil {
		return "", fmt.Errorf("municipality register request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", k.userAgent)
	resp, err := k.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("municipality register: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return "", errMunicipalityNotFound
	default:
		return "", fmt.Errorf("municipality register answered %d", resp.StatusCode)
	}
	var body struct {
		Kommunenummer    string `json:"kommunenummer"`
		Kommunenavn      string `json:"kommunenavn"`
		KommunenavnNorsk string `json:"kommunenavnNorsk"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, municipalityMaxBody)).Decode(&body); err != nil {
		return "", fmt.Errorf("municipality register response: %w", err)
	}
	if body.Kommunenummer != code {
		return "", fmt.Errorf("municipality register answered another code")
	}
	name := strings.TrimSpace(body.KommunenavnNorsk)
	if name == "" {
		name = strings.TrimSpace(body.Kommunenavn)
	}
	if name == "" {
		return "", fmt.Errorf("municipality register answered no name")
	}
	return name, nil
}

type municipalityCacheEntry struct {
	name    string
	found   bool
	expires time.Time
}

// municipalityNames caches the register's answers in process. Only
// four-digit codes reach it, so it holds at most 10 000 entries.
type municipalityNames struct {
	register municipalityRegister
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]municipalityCacheEntry
}

func newMunicipalityNames(register municipalityRegister) *municipalityNames {
	return &municipalityNames{register: register, now: time.Now, cache: map[string]municipalityCacheEntry{}}
}

// lookup returns the name and status (found, unknown or unavailable) of code.
func (m *municipalityNames) lookup(ctx context.Context, code string) (string, string, error) {
	m.mu.Lock()
	e, ok := m.cache[code]
	m.mu.Unlock()
	if ok && m.now().Before(e.expires) {
		if e.found {
			return e.name, municipalityFound, nil
		}
		return "", municipalityUnknown, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, municipalityLookupTimeout)
	defer cancel()
	name, err := m.register.Name(lookupCtx, code)
	switch {
	case errors.Is(err, errMunicipalityNotFound):
		m.store(code, municipalityCacheEntry{})
		return "", municipalityUnknown, nil
	case err != nil:
		return "", municipalityUnavailable, err
	}
	m.store(code, municipalityCacheEntry{name: name, found: true})
	return name, municipalityFound, nil
}

func (m *municipalityNames) store(code string, e municipalityCacheEntry) {
	e.expires = m.now().Add(municipalityCacheTTL)
	m.mu.Lock()
	m.cache[code] = e
	m.mu.Unlock()
}

type municipalityLookupResponse struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Status is found (Name set), unknown (the register holds no such
	// code) or unavailable (the register could not be asked).
	Status string `json:"status"`
}

// HandleLookupMunicipality names one municipality code for the settings
// page. A register that errors answers 200 with status unavailable, never an
// error, so the page shows the code alone.
func (h *ClubSettingsHandler) HandleLookupMunicipality(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if !municipalityCodePattern.MatchString(code) {
		JSON(w, http.StatusBadRequest, map[string]string{
			"error": "a municipality code is four digits",
			"code":  codeInvalidMunicipalityCodes,
		})
		return
	}
	resp := municipalityLookupResponse{Code: code, Status: municipalityUnavailable}
	if h.municipalities != nil {
		name, status, err := h.municipalities.lookup(r.Context(), code)
		if err != nil {
			h.log.Warn().Err(redactURLError(err)).Str("code", code).Msg("municipality name lookup failed")
		}
		resp.Name, resp.Status = name, status
	}
	JSON(w, http.StatusOK, resp)
}
