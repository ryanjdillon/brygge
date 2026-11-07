package handlers

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

// Self-service membership join (BRY-629, spec BRY-476). A visitor submits the
// public form; nothing but a membership_applications row exists until they
// click the emailed link and the SPA confirms it with a POST. Confirming
// creates (or reactivates) the user as an applicant, records the consents,
// opens the pending checkout order for the club's join price item and logs
// them in. Settling that order grants membership (BRY-630).

const (
	// joinTokenTTL is how long a confirmation link works; the hourly sweep
	// deletes the row after it.
	joinTokenTTL = 24 * time.Hour

	// Field caps: they bound what a visitor can store before confirming.
	joinMaxName       = 100
	joinMaxEmail      = 254 // RFC 5321 path limit
	joinMaxPhone      = 40
	joinMaxAddress    = 200
	joinMaxPostalCode = 20
	joinMaxCity       = 100
	joinMaxBody       = 16 << 10

	// joinWorkTimeout bounds the work done after the 202: the lookup, the
	// application row and the send.
	joinWorkTimeout = 30 * time.Second

	// Per-recipient throttle: every email /join sends to one address counts,
	// whichever branch sent it, so the form cannot be used to flood a mailbox
	// from many IPs.
	joinRecipientGap      = 10 * time.Minute
	joinRecipientDailyMax = 3
	joinRecipientDay      = 24 * time.Hour

	// joinClubDailyMailCap is the default club-wide cap on join emails in a
	// rolling day, both kinds together (config.JoinDailyMailCap overrides
	// it). It bounds what many addresses from many IPs can make the club's
	// sender emit, which the per-address and per-IP budgets cannot.
	joinClubDailyMailCap = 100

	// joinEmailHMACInfo separates the join email key from every other use of
	// the server secret it is derived from.
	joinEmailHMACInfo = "brygge/join-email-hmac"
)

// joinCountScript increments a day counter and gives it its expiry in the
// same step, so a counter can never be left without one (it would then cap
// an address, or the club, forever). A key found without an expiry gets one.
var joinCountScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// joinRequiredDocs are the documents a joiner consents to. Joining is closed
// until the club has published every one, and confirming records a consent to
// each at the version shown when the form was submitted.
var joinRequiredDocs = []string{"privacy", "statutes", "harbour_rules", "terms_of_sale"}

// Error codes the join SPA pages match on.
const (
	codeJoinClosed           = "join_closed"
	codeJoinTermsNotAccepted = "terms_not_accepted"
	codeJoinTokenInvalid     = "join_token_invalid"
	codeJoinTokenUsed        = "join_token_used"
	codeJoinTokenExpired     = "join_token_expired"
	codeJoinAlreadyMember    = "already_member"
	codeJoinRejoinBlocked    = "rejoin_blocked"
	codeUnsupportedMedia     = "unsupported_media_type"
)

// The coarse reasons GET /join/status gives for a closed join, besides the
// legal document types not yet published. They describe the club's setup,
// never a person.
const (
	joinMissingClub           = "club"
	joinMissingPriceItem      = "price_item_unset"
	joinMissingPriceInactive  = "price_item_inactive"
	joinMissingPriceNotFlat   = "price_item_not_flat"
	joinMissingPriceOtherClub = "price_item_other_club"
)

// joinRequested outcomes, recorded in the membership.join_requested audit row.
const (
	joinOutcomeNew          = "new"
	joinOutcomeResent       = "resent"
	joinOutcomeApplicant    = "applicant"
	joinOutcomeReactivation = "reactivation"
	joinOutcomeMember       = "member"
	joinOutcomeBlocked      = "blocked"
	joinOutcomeThrottled    = "throttled"
	joinOutcomeCapped       = "capped"
	joinOutcomeFailed       = "failed"
)

// JoinHandler serves the public join routes.
type JoinHandler struct {
	db       *pgxpool.Pool
	rdb      *redis.Client
	config   *config.Config
	email    email.Sender
	sessions *auth.SessionService
	audit    *audit.Service
	log      zerolog.Logger

	// addresses is the address register the form searches and the join
	// re-looks the picked address up in (BRY-639).
	addresses addressRegister
	// addressTimeout overrides addressLookupTimeout; tests shorten it.
	addressTimeout time.Duration

	// emailKey keys the HMAC that stands in for an address in Redis keys and
	// the audit log, so neither can be reversed by hashing guessed addresses.
	emailKey []byte

	// async runs the per-address work of a join request after the response
	// is written, so every address gets the same answer in the same time.
	// Tests replace it to run the work inline.
	async func(func())
}

// joinEmailKey derives the email HMAC key from TOTP_ENCRYPTION_KEY, the
// server secret every deploy keeps in /etc/brygge/env. Without it (local
// development) a random per-process key is used: the throttle then restarts
// with the process and audit values don't match across restarts.
func joinEmailKey(cfg *config.Config, log zerolog.Logger) []byte {
	if cfg != nil && cfg.TOTPEncryptionKey != "" {
		key, err := hkdf.Key(sha256.New, []byte(cfg.TOTPEncryptionKey), nil, joinEmailHMACInfo, 32)
		if err == nil {
			return key
		}
		log.Error().Err(err).Msg("join: deriving the email HMAC key failed; using a random one")
	} else {
		log.Warn().Msg("join: TOTP_ENCRYPTION_KEY unset; the email HMAC key is random per process")
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
}

// runJoinJob runs one background join job, so a panic in it is logged as an
// error instead of taking the whole API down with it.
func runJoinJob(log zerolog.Logger, job func()) {
	defer func() {
		if p := recover(); p != nil {
			log.Error().Interface("panic", p).Msg("join: background job panicked")
		}
	}()
	job()
}

func NewJoinHandler(
	db *pgxpool.Pool,
	rdb *redis.Client,
	cfg *config.Config,
	mailer email.Sender,
	sessions *auth.SessionService,
	auditSvc *audit.Service,
	log zerolog.Logger,
) *JoinHandler {
	h := &JoinHandler{
		db:       db,
		rdb:      rdb,
		config:   cfg,
		email:    mailer,
		sessions: sessions,
		audit:    auditSvc,
		log:      log.With().Str("handler", "join").Logger(),
	}
	h.addresses = newKartverketAddresses(cfg)
	h.emailKey = joinEmailKey(cfg, h.log)
	h.async = func(f func()) { go runJoinJob(h.log, f) }
	return h
}

// joinState is whether the club takes joins, and on what terms.
type joinState struct {
	clubID      string
	priceItemID string
	name        string
	description string
	amount      float64
	currency    string
	// versions are the current versions of joinRequiredDocs.
	versions map[string]string
	// missing names what keeps join closed; empty means open.
	missing []string
}

func (s joinState) open() bool { return len(s.missing) == 0 }

// joinClubID resolves the deploy's club, as the other public routes do.
// Empty without an error when the clubs row does not exist yet.
func joinClubID(ctx context.Context, q pgxQuerier, slug string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// loadJoinState decides whether join is open for clubID: the club's
// join_price_item_id names an active, flat price item of the same club, and
// every one of joinRequiredDocs is published.
func loadJoinState(ctx context.Context, q dbQuerier, clubID string) (joinState, error) {
	st := joinState{clubID: clubID}
	if clubID == "" {
		st.missing = []string{joinMissingClub}
		return st, nil
	}

	var (
		itemID, itemClub, kind, name, desc, currency *string
		active                                       *bool
		amount                                       *float64
	)
	err := q.QueryRow(ctx,
		`SELECT c.join_price_item_id::text, p.club_id::text, p.is_active, p.pricing_kind,
		        p.name, p.description, p.amount::float8, p.currency
		   FROM clubs c
		   LEFT JOIN price_items p ON p.id = c.join_price_item_id
		  WHERE c.id = $1`,
		clubID,
	).Scan(&itemID, &itemClub, &active, &kind, &name, &desc, &amount, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		st.missing = []string{joinMissingClub}
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("load join price item: %w", err)
	}
	switch {
	case itemID == nil || itemClub == nil:
		st.missing = append(st.missing, joinMissingPriceItem)
	case *itemClub != clubID:
		st.missing = append(st.missing, joinMissingPriceOtherClub)
	case active == nil || !*active:
		st.missing = append(st.missing, joinMissingPriceInactive)
	case kind == nil || *kind != "flat":
		st.missing = append(st.missing, joinMissingPriceNotFlat)
	default:
		st.priceItemID = *itemID
		st.name = derefString(name)
		st.description = derefString(desc)
		st.currency = derefString(currency)
		if amount != nil {
			st.amount = *amount
		}
	}

	versions, missingDocs, err := LatestLegalVersions(ctx, q, clubID, joinRequiredDocs)
	if err != nil {
		return st, err
	}
	st.versions = versions
	st.missing = append(st.missing, missingDocs...)
	return st, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type joinPrice struct {
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
}

type joinStatusResponse struct {
	Open    bool       `json:"open"`
	Price   *joinPrice `json:"price"`
	Missing []string   `json:"missing"`
	// Year is the server's current year, so the form labels the dues with
	// the year they are billed for rather than the visitor's clock.
	Year int `json:"year"`
}

// HandleJoinStatus answers whether the join form may be used, with the price
// when it may.
func (h *JoinHandler) HandleJoinStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := joinClubID(ctx, h.db, h.config.ClubSlug)
	if err != nil {
		h.log.Error().Err(err).Msg("join status: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	st, err := loadJoinState(ctx, h.db, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("join status: load state failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := joinStatusResponse{Open: st.open(), Missing: st.missing, Year: time.Now().In(time.Local).Year()}
	if resp.Missing == nil {
		resp.Missing = []string{}
	}
	if st.open() {
		desc := st.description
		if desc == "" {
			desc = st.name
		}
		resp.Price = &joinPrice{Amount: st.amount, Currency: st.currency, Name: st.name, Description: desc}
	}
	JSON(w, http.StatusOK, resp)
}

type joinRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	AddressLine string `json:"address_line"`
	PostalCode  string `json:"postal_code"`
	City        string `json:"city"`
	// AddressID is the address the visitor picked from the register search.
	// The server looks it up again and stores the register's address in
	// place of the typed one. Residency (is_local) is never taken from the
	// client: it follows from a verified address, and is unknown otherwise.
	AddressID   string `json:"address_id"`
	Newsletter  bool   `json:"newsletter"`
	AcceptTerms bool   `json:"accept_terms"`
	// ConsentVersions is optional: the version of each required document the
	// page showed. When sent, it must match the club's current versions, so
	// nobody is recorded as agreeing to a text they were not shown.
	ConsentVersions map[string]string `json:"consent_versions"`
	// Website is a honeypot, as on the contact form: hidden in the form, so
	// only a script that fills every input sets it.
	Website string `json:"website"`
}

// joinInput is a validated, cleaned join request.
type joinInput struct {
	firstName, lastName, email, phone string
	addressLine, postalCode, city     string
	addressID                         string
	newsletter                        bool

	// Set by verifyJoinAddress: whether the register confirmed the address,
	// its municipality, and residency derived from it.
	addressVerified bool
	kommunenummer   string
	isLocal         bool
}

// joinMaxAddressID bounds the register id a visitor can send.
const joinMaxAddressID = 64

// clean validates the request and returns the stored form, or a message.
func (req joinRequest) clean() (joinInput, string) {
	in := joinInput{
		firstName:   cleanSingleLine(req.FirstName),
		lastName:    cleanSingleLine(req.LastName),
		phone:       cleanSingleLine(req.Phone),
		addressLine: cleanSingleLine(req.AddressLine),
		postalCode:  cleanSingleLine(req.PostalCode),
		city:        cleanSingleLine(req.City),
		addressID:   strings.TrimSpace(req.AddressID),
		newsletter:  req.Newsletter,
	}
	if in.firstName == "" || in.lastName == "" || strings.TrimSpace(req.Email) == "" ||
		in.phone == "" || in.addressLine == "" || in.postalCode == "" || in.city == "" {
		return in, "first_name, last_name, email, phone, address_line, postal_code and city are required"
	}
	if utf8.RuneCountInString(in.firstName) > joinMaxName ||
		utf8.RuneCountInString(in.lastName) > joinMaxName ||
		len(req.Email) > joinMaxEmail ||
		utf8.RuneCountInString(in.phone) > joinMaxPhone ||
		utf8.RuneCountInString(in.addressLine) > joinMaxAddress ||
		utf8.RuneCountInString(in.postalCode) > joinMaxPostalCode ||
		utf8.RuneCountInString(in.city) > joinMaxCity ||
		len(in.addressID) > joinMaxAddressID {
		return in, "a field is too long"
	}
	phone, ok := normalizePhone(in.phone)
	if !ok {
		return in, "invalid phone number"
	}
	in.phone = phone
	addr, ok := parseBareEmail(req.Email)
	if !ok || isReservedEmailDomain(addr) {
		return in, "invalid email address"
	}
	// Stored lowercased, as an admin-created user's address is, so the
	// unique (club_id, email) and the magic-link lookup see one spelling.
	in.email = strings.ToLower(addr)
	return in, ""
}

// joinAccepted is the one answer to every well-formed request while join is
// open: new address, pending applicant, former member, existing member,
// throttled or honeypot alike.
var joinAccepted = map[string]string{"status": "received"}

// requireJSON answers 415 unless the request declares a JSON body. A
// cross-site HTML form can only send form or text types, and CORS does not
// stop such a "simple" POST from reaching the handler, so this is what keeps
// another site from submitting or confirming a join in a visitor's browser.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		JSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "Content-Type must be application/json",
			"code":  codeUnsupportedMedia,
		})
		return false
	}
	return true
}

// staleJoinConsents reports whether shown, when sent, differs from the
// club's current version of any required document.
func staleJoinConsents(shown, current map[string]string) bool {
	if shown == nil {
		return false
	}
	for _, d := range joinRequiredDocs {
		if v, ok := shown[d]; !ok || v != current[d] {
			return true
		}
	}
	return false
}

// HandleJoin takes the public join form. It answers 202 before any
// per-address work, which runs in the background: the response and its
// timing say nothing about whether the address is known.
func (h *JoinHandler) HandleJoin(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, joinMaxBody)
	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Error(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in, msg := req.clean()
	if msg != "" {
		Error(w, http.StatusBadRequest, msg)
		return
	}
	if !req.AcceptTerms {
		JSON(w, http.StatusBadRequest, map[string]string{
			"error": "the terms must be accepted",
			"code":  codeJoinTermsNotAccepted,
		})
		return
	}

	ctx := r.Context()
	clubID, err := joinClubID(ctx, h.db, h.config.ClubSlug)
	if err != nil {
		h.log.Error().Err(err).Msg("join: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	st, err := loadJoinState(ctx, h.db, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("join: load state failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !st.open() {
		JSON(w, http.StatusConflict, map[string]string{
			"error": "joining is not open",
			"code":  codeJoinClosed,
		})
		return
	}
	if staleJoinConsents(req.ConsentVersions, st.versions) {
		JSON(w, http.StatusConflict, map[string]any{
			"error":    "a document has a newer version",
			"code":     codeLegalDocumentStale,
			"versions": st.versions,
		})
		return
	}

	// A filled honeypot is checked last, so it gets exactly the answer a real
	// submission with the same fields would; then nothing is stored, sent or
	// logged.
	if strings.TrimSpace(req.Website) != "" {
		JSON(w, http.StatusAccepted, joinAccepted)
		return
	}

	ip := r.RemoteAddr
	// Detached from the request so the work outlives the response, keeping
	// the request's trace and log values.
	detached := context.WithoutCancel(ctx)
	h.async(func() {
		workCtx, cancel := context.WithTimeout(detached, joinWorkTimeout)
		defer cancel()
		h.processJoin(workCtx, st, in, ip)
	})
	JSON(w, http.StatusAccepted, joinAccepted)
}

// emailHMAC is the stable, address-free key of an email: the throttle's
// Redis keys and the audit row use it, never the address. It is keyed, so it
// cannot be matched by hashing a list of guessed addresses.
func (h *JoinHandler) emailHMAC(addr string) string {
	mac := hmac.New(sha256.New, h.emailKey)
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return hex.EncodeToString(mac.Sum(nil))
}

// joinTokenHash is what membership_applications.token_hash stores.
func joinTokenHash(token string) string {
	return sha256Hex(token)
}

// joinBudget is what allowRecipient decided.
type joinBudget int

const (
	joinBudgetAllowed   joinBudget = iota
	joinBudgetThrottled            // the address's budget is spent
	joinBudgetCapped               // the club's daily cap is spent
)

// clubDailyMailCap is the club-wide daily cap in force.
func (h *JoinHandler) clubDailyMailCap() int64 {
	if h.config != nil && h.config.JoinDailyMailCap > 0 {
		return int64(h.config.JoinDailyMailCap)
	}
	return joinClubDailyMailCap
}

// countDay increments a rolling-day counter atomically with its expiry.
func (h *JoinHandler) countDay(ctx context.Context, key string) (int64, error) {
	return joinCountScript.Run(ctx, h.rdb, []string{key}, joinRecipientDay.Milliseconds()).Int64()
}

// allowRecipient takes one send from the budgets: the address may get one
// email per joinRecipientGap and joinRecipientDailyMax per joinRecipientDay,
// and the club at most clubDailyMailCap in a rolling day. An error means a
// budget could not be read, and the caller sends nothing.
func (h *JoinHandler) allowRecipient(ctx context.Context, clubID, emailKey string) (joinBudget, error) {
	if h.rdb == nil {
		return joinBudgetThrottled, errors.New("join: no redis for the send budget")
	}
	ok, err := h.rdb.SetNX(ctx, "join:rcpt:gap:"+emailKey, 1, joinRecipientGap).Result()
	if err != nil {
		return joinBudgetThrottled, fmt.Errorf("recipient gap: %w", err)
	}
	if !ok {
		return joinBudgetThrottled, nil
	}
	n, err := h.countDay(ctx, "join:rcpt:day:"+emailKey)
	if err != nil {
		return joinBudgetThrottled, fmt.Errorf("recipient day count: %w", err)
	}
	if n > joinRecipientDailyMax {
		return joinBudgetThrottled, nil
	}
	total, err := h.countDay(ctx, "join:club:day:"+clubID)
	if err != nil {
		return joinBudgetThrottled, fmt.Errorf("club day count: %w", err)
	}
	if limit := h.clubDailyMailCap(); total > limit {
		// Many addresses at once is either a burst of real joiners or abuse;
		// either way the board should know joins are being turned away.
		h.log.Warn().Str("club_id", clubID).Int64("cap", limit).
			Msg("join: club-wide daily join email cap reached; nothing sent")
		return joinBudgetCapped, nil
	}
	return joinBudgetAllowed, nil
}

// joinExistingUser is the person in the club holding the submitted address.
type joinExistingUser struct {
	id          string
	email       string
	deactivated bool
	roles       []string
	// rejoinBlocked is set when any of the club's rows with the address
	// carries the board's "can't rejoin" mark.
	rejoinBlocked bool
}

// findJoinUser loads the club's person with that address, case-insensitively;
// an active row wins over a deactivated one. nil when there is none.
func findJoinUser(ctx context.Context, q pgxQuerier, clubID, addr string, forUpdate bool) (*joinExistingUser, error) {
	query := `SELECT id, email, deactivated_at IS NOT NULL
	            FROM users
	           WHERE club_id = $1 AND lower(email) = lower($2) AND kind = 'person'
	           ORDER BY deactivated_at IS NULL DESC, created_at
	           LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var u joinExistingUser
	err := q.QueryRow(ctx, query, clubID, addr).Scan(&u.id, &u.email, &u.deactivated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(array_agg(role::text), '{}') FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		u.id, clubID,
	).Scan(&u.roles); err != nil {
		return nil, fmt.Errorf("load user roles: %w", err)
	}
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(bool_or(rejoin_blocked), false) FROM users
		  WHERE club_id = $1 AND lower(email) = lower($2) AND kind = 'person'`,
		clubID, addr,
	).Scan(&u.rejoinBlocked); err != nil {
		return nil, fmt.Errorf("load rejoin block: %w", err)
	}
	return &u, nil
}

// processJoin is the per-address half of a join request, run after the 202.
// It sends at most one email and records one membership.join_requested row.
func (h *JoinHandler) processJoin(ctx context.Context, st joinState, in joinInput, ip string) {
	emailKey := h.emailHMAC(in.email)
	outcome, applicationID := h.joinBranch(ctx, st, in, emailKey)
	if h.audit != nil {
		clubID := st.clubID
		h.audit.Log(ctx, audit.Entry{
			ClubID:     &clubID,
			ActorIP:    ip,
			Action:     audit.ActionMembershipJoinRequested,
			Resource:   "membership_application",
			ResourceID: applicationID,
			Details:    map[string]any{"email_hmac": emailKey, "outcome": outcome},
		})
	}
}

// joinBranch runs the R7 branch for the address and reports which one it took
// and the application it wrote, if any.
func (h *JoinHandler) joinBranch(ctx context.Context, st joinState, in joinInput, emailKey string) (outcome, applicationID string) {
	budget, err := h.allowRecipient(ctx, st.clubID, emailKey)
	if err != nil {
		h.log.Error().Err(err).Msg("join: send budget unavailable, nothing sent")
		return joinOutcomeFailed, ""
	}
	switch budget {
	case joinBudgetThrottled:
		return joinOutcomeThrottled, ""
	case joinBudgetCapped:
		return joinOutcomeCapped, ""
	}

	u, err := findJoinUser(ctx, h.db, st.clubID, in.email, false)
	if err != nil {
		h.log.Error().Err(err).Msg("join: user lookup failed")
		return joinOutcomeFailed, ""
	}

	locale, clubName := h.joinMailLocale(ctx, st.clubID)

	// An active user who is not only an applicant already belongs to the
	// club: nothing is created, and the address gets a login link instead.
	// The board has marked the address "can't rejoin" (BRY-476): nothing is
	// created, active or not, and the address is pointed at the board.
	if u != nil && u.rejoinBlocked {
		contact, err := contactRecipient(ctx, h.db, h.config.ClubSlug)
		if err != nil {
			h.log.Warn().Err(err).Msg("join: club contact address unreadable; the email asks for a reply instead")
			contact = ""
		}
		h.send(ctx, st.clubID, u.id, u.email, email.KindJoinContactBoard,
			email.JoinContactBoardSubject(locale, clubName),
			email.JoinContactBoardBody(locale, clubName, h.config.Domain, contact))
		return joinOutcomeBlocked, ""
	}

	if u != nil && !u.deactivated && !roles.IsApplicantOnly(u.roles) {
		loginURL, err := issueMagicLink(ctx, h.db, h.config, st.clubID, u.email)
		if err != nil {
			h.log.Error().Err(err).Msg("join: issue login link failed")
			return joinOutcomeFailed, ""
		}
		h.send(ctx, st.clubID, u.id, u.email, email.KindJoinAlreadyMember,
			email.JoinAlreadyMemberSubject(locale, clubName),
			email.JoinAlreadyMemberBody(locale, clubName, h.config.Domain, loginURL))
		return joinOutcomeMember, ""
	}

	in = h.verifyJoinAddress(ctx, st.clubID, in)

	token, err := generateToken()
	if err != nil {
		h.log.Error().Err(err).Msg("join: generate token failed")
		return joinOutcomeFailed, ""
	}
	replaced, applicationID, err := h.storeApplication(ctx, st, in, joinTokenHash(token))
	if err != nil {
		h.log.Error().Err(err).Msg("join: store application failed")
		return joinOutcomeFailed, ""
	}

	userID := ""
	switch {
	case u != nil && u.deactivated:
		outcome, userID = joinOutcomeReactivation, u.id
	case u != nil:
		outcome, userID = joinOutcomeApplicant, u.id
	case replaced:
		outcome = joinOutcomeResent
	default:
		outcome = joinOutcomeNew
	}
	h.send(ctx, st.clubID, userID, in.email, email.KindJoinConfirm,
		email.JoinConfirmSubject(locale, clubName),
		email.JoinConfirmBody(locale, clubName, h.config.Domain, joinConfirmURL(h.config, token)))
	return outcome, applicationID
}

// storeApplication writes the application, replacing any unconfirmed one for
// the same address so only the newest link works. It reports whether one was
// replaced.
func (h *JoinHandler) storeApplication(ctx context.Context, st joinState, in joinInput, tokenHash string) (replaced bool, id string, err error) {
	versions, err := json.Marshal(st.versions)
	if err != nil {
		return false, "", fmt.Errorf("marshal consent versions: %w", err)
	}
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	tag, err := tx.Exec(ctx,
		`DELETE FROM membership_applications
		  WHERE club_id = $1 AND lower(email) = lower($2) AND consumed_at IS NULL`,
		st.clubID, in.email,
	)
	if err != nil {
		return false, "", fmt.Errorf("replace pending application: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO membership_applications
		        (club_id, first_name, last_name, email, phone, address_line, postal_code, city,
		         is_local, newsletter, token_hash, consent_versions, expires_at,
		         address_verified, address_kommunenummer, address_verified_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, now() + $13::interval,
		         $14, NULLIF($15, ''), CASE WHEN $14 THEN now() END)
		 RETURNING id`,
		st.clubID, in.firstName, in.lastName, in.email, in.phone, in.addressLine, in.postalCode, in.city,
		in.isLocal, in.newsletter, tokenHash, versions, fmt.Sprintf("%d seconds", int(joinTokenTTL.Seconds())),
		in.addressVerified, in.kommunenummer,
	).Scan(&id); err != nil {
		return false, "", fmt.Errorf("insert application: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("commit: %w", err)
	}
	return tag.RowsAffected() > 0, id, nil
}

// joinMailLocale is the language and display name of the club's join mail.
func (h *JoinHandler) joinMailLocale(ctx context.Context, clubID string) (locale, clubName string) {
	var lang, name string
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(default_language, ''), name FROM clubs WHERE id = $1`, clubID,
	).Scan(&lang, &name); err != nil {
		h.log.Warn().Err(err).Msg("join: club language unreadable, using the default")
	}
	if h.config.ClubName != "" {
		name = h.config.ClubName
	}
	return email.JoinLocale(lang), name
}

// send mails one join email. A failure is logged without the address; the
// visitor already has their answer and can submit again.
func (h *JoinHandler) send(ctx context.Context, clubID, userID, to string, kind email.Kind, subject, body string) {
	if h.email == nil {
		h.log.Warn().Str("kind", string(kind)).Msg("join email not sent: no mail transport configured")
		return
	}
	mailCtx := email.WithSendMeta(ctx, email.SendMeta{ClubID: clubID, Kind: kind, UserID: userID})
	if err := h.email.Send(mailCtx, to, subject, body); err != nil {
		h.log.Error().Err(err).Str("kind", string(kind)).Msg("join email send failed")
	}
}

// joinConfirmURL is the SPA page the confirmation email links to. Opening it
// changes nothing: the page confirms with a POST, so a mail scanner that
// prefetches links cannot use the token up.
func joinConfirmURL(cfg *config.Config, token string) string {
	return publicSiteBase(cfg) + "/bli-medlem/stadfest?token=" + url.QueryEscape(token)
}

// joinApplication is a stored application as confirm reads it.
type joinApplication struct {
	id                                string
	firstName, lastName, email, phone string
	addressLine, postalCode, city     string
	isLocal, newsletter               bool
	versions                          map[string]string
	expiresAt                         time.Time
	consumed                          bool

	addressVerified   bool
	kommunenummer     *string
	addressVerifiedAt *time.Time
}

// loadJoinApplication reads the club's application behind token and reports
// why it cannot be confirmed, as an error code (empty when it can).
func loadJoinApplication(ctx context.Context, q pgxQuerier, clubID, token string, forUpdate bool) (*joinApplication, string, error) {
	if token == "" || clubID == "" {
		return nil, codeJoinTokenInvalid, nil
	}
	query := `SELECT id, first_name, last_name, email, COALESCE(phone, ''), address_line, postal_code, city,
	                 is_local, newsletter, consent_versions, expires_at, consumed_at IS NOT NULL,
	                 address_verified, address_kommunenummer, address_verified_at
	            FROM membership_applications
	           WHERE token_hash = $1 AND club_id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var (
		a        joinApplication
		versions []byte
	)
	err := q.QueryRow(ctx, query, joinTokenHash(token), clubID).Scan(
		&a.id, &a.firstName, &a.lastName, &a.email, &a.phone, &a.addressLine, &a.postalCode, &a.city,
		&a.isLocal, &a.newsletter, &versions, &a.expiresAt, &a.consumed,
		&a.addressVerified, &a.kommunenummer, &a.addressVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, codeJoinTokenInvalid, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load application: %w", err)
	}
	if err := json.Unmarshal(versions, &a.versions); err != nil {
		return nil, "", fmt.Errorf("decode consent versions: %w", err)
	}
	switch {
	case a.consumed:
		return &a, codeJoinTokenUsed, nil
	case !time.Now().Before(a.expiresAt):
		return &a, codeJoinTokenExpired, nil
	}
	return &a, "", nil
}

type joinConfirmStatusResponse struct {
	Valid     bool   `json:"valid"`
	FirstName string `json:"first_name"`
	Code      string `json:"code,omitempty"`
}

// HandleJoinConfirmStatus tells the confirm page whether its token can still
// be confirmed. It only reads: the link a scanner prefetches lands here (via
// the SPA) or on the page, and neither uses the token up.
func (h *JoinHandler) HandleJoinConfirmStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clubID, err := joinClubID(ctx, h.db, h.config.ClubSlug)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm status: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	app, code, err := loadJoinApplication(ctx, h.db, clubID, chi.URLParam(r, "token"), false)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm status: load application failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if code != "" {
		JSON(w, http.StatusOK, joinConfirmStatusResponse{Valid: false, Code: code})
		return
	}
	JSON(w, http.StatusOK, joinConfirmStatusResponse{Valid: true, FirstName: app.firstName})
}

type joinConfirmRequest struct {
	Token string `json:"token"`
}

type joinConfirmResponse struct {
	CheckoutURL string `json:"checkout_url"`
	OrderToken  string `json:"order_token"`
}

// errJoinRejoinBlocked is a confirm for an address the board has marked
// "can't rejoin" since the request was made.
var errJoinRejoinBlocked = errors.New("the board has blocked rejoining")

// errJoinAlreadyMember is a confirm for an address that became an active
// non-applicant user after the request was made.
var errJoinAlreadyMember = errors.New("address already belongs to a user")

// HandleJoinConfirm confirms an application: in one transaction it locks the
// application, creates or reactivates the user as an applicant, records the
// consents, opens the pending join order and uses the token up. It then logs
// the applicant in and returns the order's pay page.
func (h *JoinHandler) HandleJoinConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireJSON(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req joinConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	clubID, err := joinClubID(ctx, h.db, h.config.ClubSlug)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: begin failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	app, code, err := loadJoinApplication(ctx, tx, clubID, strings.TrimSpace(req.Token), true)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: load application failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if code != "" {
		JSON(w, http.StatusGone, map[string]string{"error": "the confirmation link can no longer be used", "code": code})
		return
	}

	st, err := loadJoinState(ctx, tx, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: load state failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !st.open() {
		// The token stays usable: join may open again within its 24 hours.
		JSON(w, http.StatusConflict, map[string]string{"error": "joining is not open", "code": codeJoinClosed})
		return
	}

	userID, reactivated, err := confirmJoinUser(ctx, tx, clubID, app)
	if errors.Is(err, errJoinAlreadyMember) || errors.Is(err, errJoinRejoinBlocked) {
		h.refuseConfirm(ctx, w, tx, app.id, err)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: user write failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := recordJoinConsents(ctx, tx, clubID, userID, app); err != nil {
		h.log.Error().Err(err).Msg("join confirm: consents failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	orderID, orderToken, err := ensureJoinOrder(ctx, tx, st, userID)
	if err != nil {
		h.log.Error().Err(err).Msg("join confirm: order failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE membership_applications SET consumed_at = now() WHERE id = $1`, app.id); err != nil {
		h.log.Error().Err(err).Msg("join confirm: consume application failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	details, _ := json.Marshal(map[string]any{
		"application_id": app.id,
		"order_id":       orderID,
		"reactivated":    reactivated,
		"newsletter":     app.newsletter,
	})
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, actor_ip, action, resource, resource_id, details)
		 VALUES ($1, $2, $3, $4, 'user', $5, $6)`,
		clubID, userID, r.RemoteAddr, audit.ActionMembershipJoinConfirmed, userID, details,
	); err != nil {
		h.log.Error().Err(err).Msg("join confirm: audit failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("join confirm: commit failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// The applicant is logged in as a magic link would do it. Paying needs
	// no session (the pay page is token-addressed), so a session failure
	// still returns the pay page; they can log in later to finish.
	if h.sessions != nil {
		sessionID, err := h.sessions.CreateSession(ctx, userID, clubID, r.RemoteAddr, r.UserAgent())
		if err != nil {
			h.log.Error().Err(err).Str("user_id", userID).Msg("join confirm: create session failed")
		} else {
			middleware.SetSessionCookie(w, sessionID, cookieSecure(h.config))
		}
	}

	JSON(w, http.StatusOK, joinConfirmResponse{CheckoutURL: checkoutURL(h.config, orderToken), OrderToken: orderToken})
}

// refuseConfirm answers a confirm that may create nothing (the address is
// taken, or the board blocked rejoining): the token is spent so it cannot be
// retried, and nothing else is written.
func (h *JoinHandler) refuseConfirm(ctx context.Context, w http.ResponseWriter, tx pgx.Tx, applicationID string, reason error) {
	if _, err := tx.Exec(ctx, `UPDATE membership_applications SET consumed_at = now() WHERE id = $1`, applicationID); err != nil {
		h.log.Error().Err(err).Msg("join confirm: consume application failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("join confirm: commit failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if errors.Is(reason, errJoinRejoinBlocked) {
		JSON(w, http.StatusConflict, map[string]string{"error": "contact the board about membership", "code": codeJoinRejoinBlocked})
		return
	}
	JSON(w, http.StatusConflict, map[string]string{"error": "the address already belongs to a user", "code": codeJoinAlreadyMember})
}

// confirmJoinUser makes the application's address an applicant: a new user,
// a reactivated former member (roles reset to applicant only, contact details
// and any pending cancellation replaced), or an existing applicant with the
// newly typed details. errJoinAlreadyMember when it is an active user with any
// other role.
func confirmJoinUser(ctx context.Context, tx pgx.Tx, clubID string, app *joinApplication) (userID string, reactivated bool, err error) {
	u, err := findJoinUser(ctx, tx, clubID, app.email, true)
	if err != nil {
		return "", false, err
	}
	if u != nil && u.rejoinBlocked {
		return "", false, errJoinRejoinBlocked
	}

	switch {
	case u == nil:
		// ON CONFLICT rather than catching 23505: a unique violation would
		// abort the transaction, and the confirm must still spend the token.
		// A row appearing between the lookup and here (a concurrent confirm
		// or admin create) means the address is taken.
		err = tx.QueryRow(ctx,
			`INSERT INTO users (club_id, email, first_name, last_name, phone, address_line, postal_code, city, is_local,
			                    address_verified, address_kommunenummer, address_verified_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 ON CONFLICT (club_id, email) DO NOTHING
			 RETURNING id`,
			clubID, app.email, app.firstName, app.lastName, app.phone, app.addressLine, app.postalCode, app.city, app.isLocal,
			app.addressVerified, app.kommunenummer, app.addressVerifiedAt,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, errJoinAlreadyMember
		}
		if err != nil {
			return "", false, fmt.Errorf("insert user: %w", err)
		}

	case u.deactivated:
		userID, reactivated = u.id, true
		if _, err := tx.Exec(ctx,
			`UPDATE users
			    SET deactivated_at = NULL, deactivated_by = NULL,
			        first_name = $3, last_name = $4, phone = $5, address_line = $6,
			        postal_code = $7, city = $8, is_local = $9,
			        address_verified = $10, address_kommunenummer = $11, address_verified_at = $12,
			        membership_cancelled_at = NULL, membership_ends_on = NULL,
			        membership_cancel_reason = NULL, updated_at = now()
			  WHERE id = $1 AND club_id = $2`,
			u.id, clubID, app.firstName, app.lastName, app.phone, app.addressLine, app.postalCode, app.city, app.isLocal,
			app.addressVerified, app.kommunenummer, app.addressVerifiedAt,
		); err != nil {
			return "", false, fmt.Errorf("reactivate user: %w", err)
		}
		// Whatever the former member held (member, a board role) lapsed with
		// the deactivation; they come back as an applicant like anyone else.
		if _, err := tx.Exec(ctx,
			`DELETE FROM user_roles WHERE user_id = $1 AND club_id = $2`, u.id, clubID,
		); err != nil {
			return "", false, fmt.Errorf("reset roles: %w", err)
		}

	case roles.IsApplicantOnly(u.roles):
		userID = u.id
		if _, err := tx.Exec(ctx,
			`UPDATE users
			    SET first_name = $3, last_name = $4, phone = $5, address_line = $6,
			        postal_code = $7, city = $8, is_local = $9,
			        address_verified = $10, address_kommunenummer = $11, address_verified_at = $12,
			        updated_at = now()
			  WHERE id = $1 AND club_id = $2`,
			u.id, clubID, app.firstName, app.lastName, app.phone, app.addressLine, app.postalCode, app.city, app.isLocal,
			app.addressVerified, app.kommunenummer, app.addressVerifiedAt,
		); err != nil {
			return "", false, fmt.Errorf("update applicant: %w", err)
		}

	default:
		return "", false, errJoinAlreadyMember
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'applicant')
		 ON CONFLICT (user_id, club_id, role) DO NOTHING`,
		userID, clubID,
	); err != nil {
		return "", false, fmt.Errorf("grant applicant role: %w", err)
	}
	return userID, reactivated, nil
}

// recordJoinConsents writes a consent to each required document at the
// version stored with the application, the text shown when the form was
// submitted, and the newsletter opt-in when it was ticked. An unticked box is
// the person's current answer too: a returning user's open newsletter consent
// is revoked.
func recordJoinConsents(ctx context.Context, tx pgx.Tx, clubID, userID string, app *joinApplication) error {
	for _, docType := range joinRequiredDocs {
		v, ok := app.versions[docType]
		if !ok {
			return fmt.Errorf("application has no %s version", docType)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_consents (user_id, club_id, consent_type, version) VALUES ($1, $2, $3, $4)`,
			userID, clubID, docType, v,
		); err != nil {
			return fmt.Errorf("insert %s consent: %w", docType, err)
		}
	}
	if app.newsletter {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_consents (user_id, club_id, consent_type, version) VALUES ($1, $2, 'newsletter', $3)`,
			userID, clubID, newsletterConsentVersion,
		); err != nil {
			return fmt.Errorf("insert newsletter consent: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE user_consents SET revoked_at = now()
		  WHERE user_id = $1 AND club_id = $2 AND consent_type = 'newsletter' AND revoked_at IS NULL`,
		userID, clubID,
	); err != nil {
		return fmt.Errorf("revoke newsletter consent: %w", err)
	}
	return nil
}

// ensureJoinOrder returns the user's pending join order, creating it when
// there is none. It is an ordinary token-addressed checkout order, like a
// harbor QR order: one ad-hoc line for the join price item and no faktura
// source, paid on /betal/{token}. purpose marks it so settlement grants
// membership. A second confirm by the same applicant reuses the open order
// rather than charging twice, re-pricing it to the current join item.
func ensureJoinOrder(ctx context.Context, tx pgx.Tx, st joinState, userID string) (orderID, token string, err error) {
	currency := st.currency
	if currency == "" {
		currency = paymentCurrency
	}
	// The applicant chose bank payment earlier: the join faktura is the one
	// payable thing (BRY-631), so confirm points at its pay order rather than
	// opening a second way to pay the same join.
	// Confirming is activity on the join: touching the pay order restarts
	// the 60-day expiry clock (ACC-14), as re-pricing a pending order does.
	orderID, token, err = openJoinInvoiceOrder(ctx, tx, st.clubID, userID)
	if err != nil {
		return "", "", err
	}
	if orderID != "" {
		if _, err := tx.Exec(ctx, `UPDATE orders SET updated_at = now() WHERE id = $1`, orderID); err != nil {
			return "", "", fmt.Errorf("touch join faktura pay order: %w", err)
		}
		return orderID, token, nil
	}
	err = tx.QueryRow(ctx,
		`SELECT id, token FROM orders
		  WHERE club_id = $1 AND user_id = $2 AND purpose = 'membership_join'
		    AND status = 'pending' AND token IS NOT NULL
		  ORDER BY created_at DESC
		  LIMIT 1
		  FOR UPDATE`,
		st.clubID, userID,
	).Scan(&orderID, &token)
	if err == nil {
		// The order may predate a price change; it charges exactly one line,
		// the current join item, and nothing else.
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET total_amount = $2, currency = $3, updated_at = now() WHERE id = $1`,
			orderID, st.amount, currency,
		); err != nil {
			return "", "", fmt.Errorf("re-price join order: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM order_lines WHERE order_id = $1`, orderID); err != nil {
			return "", "", fmt.Errorf("clear join order lines: %w", err)
		}
		if err := insertJoinLine(ctx, tx, orderID, st); err != nil {
			return "", "", err
		}
		return orderID, token, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("load pending join order: %w", err)
	}

	token = genCheckoutToken()
	if err := tx.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, currency, token, purpose)
		 VALUES ($1, $2, $3, $4, $5, 'membership_join')
		 RETURNING id`,
		st.clubID, userID, st.amount, currency, token,
	).Scan(&orderID); err != nil {
		return "", "", fmt.Errorf("insert join order: %w", err)
	}
	if err := insertJoinLine(ctx, tx, orderID, st); err != nil {
		return "", "", err
	}
	return orderID, token, nil
}

// insertJoinLine writes the join order's one line: the join price item.
func insertJoinLine(ctx context.Context, tx pgx.Tx, orderID string, st joinState) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, $3, 1, $4, $4)`,
		orderID, st.priceItemID, st.name, st.amount,
	); err != nil {
		return fmt.Errorf("insert join order line: %w", err)
	}
	return nil
}

// PurgeExpired deletes membership applications past their expiry, confirmed
// or not: an unconfirmed one never became a user, and a confirmed one's data
// now lives on the user. Run by the hourly sweep.
func (h *JoinHandler) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := h.db.Exec(ctx, `DELETE FROM membership_applications WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("purge expired membership applications: %w", err)
	}
	return tag.RowsAffected(), nil
}
