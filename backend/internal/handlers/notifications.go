package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type NotificationsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
	push   pushSender
}

func NewNotificationsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *NotificationsHandler {
	return &NotificationsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "notifications").Logger(),
		push:   webpushSender{config: cfg},
	}
}

type notificationCategory struct {
	Name    string
	Default bool
}

var defaultCategories = []notificationCategory{
	{Name: "payment_reminder", Default: true},
	{Name: "slip_offer", Default: true},
	{Name: "booking_confirm", Default: true},
	{Name: "volunteer_reminder", Default: true},
	{Name: "board_announcement", Default: true},
	{Name: "waiting_list", Default: true},
	{Name: "new_document", Default: false},
	{Name: "event_reminder", Default: true},
}

type subscribeKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type subscribeRequest struct {
	Endpoint string        `json:"endpoint"`
	Keys     subscribeKeys `json:"keys"`
}

type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

type categoryPreference struct {
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
	Required bool   `json:"required"`
	Default  bool   `json:"default"`
}

type updatePreferenceRequest struct {
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
}

type updateConfigRequest struct {
	Category string `json:"category"`
	Required bool   `json:"required"`
	LeadDays *int   `json:"lead_days"`
}

type pushSubscriptionRow struct {
	Endpoint string
	P256dh   string
	Auth     string
}

func (h *NotificationsHandler) HandleGetVAPIDKey(w http.ResponseWriter, r *http.Request) {
	// The SPA (useNotifications.ts) and the OpenAPI spec read "vapid_key".
	JSON(w, http.StatusOK, map[string]string{"vapid_key": h.config.VAPIDPublicKey})
}

func (h *NotificationsHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		Error(w, http.StatusBadRequest, "endpoint, keys.p256dh, and keys.auth are required")
		return
	}

	userAgent := r.Header.Get("User-Agent")

	_, err := h.db.Exec(ctx,
		`INSERT INTO push_subscriptions (user_id, club_id, endpoint, p256dh, auth, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (endpoint) DO UPDATE SET
		   user_id = $1, club_id = $2, p256dh = $4, auth = $5, user_agent = $6, created_at = now()`,
		claims.UserID, claims.ClubID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth, userAgent,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert push subscription")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]string{"status": "subscribed"})
}

func (h *NotificationsHandler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req unsubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Endpoint == "" {
		Error(w, http.StatusBadRequest, "endpoint is required")
		return
	}

	_, err := h.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2`,
		req.Endpoint, claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete push subscription")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationsHandler) HandleGetPreferences(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	prefRows, err := h.db.Query(ctx,
		`SELECT category, enabled FROM notification_preferences WHERE user_id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query notification preferences")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer prefRows.Close()

	userPrefs := make(map[string]bool)
	for prefRows.Next() {
		var category string
		var enabled bool
		if err := prefRows.Scan(&category, &enabled); err != nil {
			h.log.Error().Err(err).Msg("failed to scan preference")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		userPrefs[category] = enabled
	}
	if err := prefRows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating preferences")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	configRows, err := h.db.Query(ctx,
		`SELECT category, required FROM notification_config WHERE club_id = $1`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query notification config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer configRows.Close()

	requiredMap := make(map[string]bool)
	for configRows.Next() {
		var category string
		var required bool
		if err := configRows.Scan(&category, &required); err != nil {
			h.log.Error().Err(err).Msg("failed to scan config")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		requiredMap[category] = required
	}
	if err := configRows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	categories := make([]categoryPreference, 0, len(defaultCategories))
	for _, dc := range defaultCategories {
		enabled := dc.Default
		if v, ok := userPrefs[dc.Name]; ok {
			enabled = v
		}
		required := requiredMap[dc.Name]
		if required {
			enabled = true
		}
		categories = append(categories, categoryPreference{
			Category: dc.Name,
			Enabled:  enabled,
			Required: required,
			Default:  dc.Default,
		})
	}

	JSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func (h *NotificationsHandler) HandleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updatePreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Category == "" {
		Error(w, http.StatusBadRequest, "category is required")
		return
	}

	validCategory := false
	for _, dc := range defaultCategories {
		if dc.Name == req.Category {
			validCategory = true
			break
		}
	}
	if !validCategory {
		Error(w, http.StatusBadRequest, "unknown category")
		return
	}

	if !req.Enabled {
		var required bool
		err := h.db.QueryRow(ctx,
			`SELECT required FROM notification_config WHERE club_id = $1 AND category = $2`,
			claims.ClubID, req.Category,
		).Scan(&required)
		if err == nil && required {
			Error(w, http.StatusBadRequest, "cannot disable required notification category")
			return
		}
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO notification_preferences (user_id, club_id, category, enabled)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, club_id, category) DO UPDATE SET enabled = $4`,
		claims.UserID, claims.ClubID, req.Category, req.Enabled,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert notification preference")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"category": req.Category, "enabled": req.Enabled})
}

func (h *NotificationsHandler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT category, required, lead_days FROM notification_config WHERE club_id = $1`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query notification config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type configEntry struct {
		Category string `json:"category"`
		Required bool   `json:"required"`
		LeadDays *int   `json:"lead_days"`
	}

	configMap := make(map[string]configEntry)
	for rows.Next() {
		var c configEntry
		if err := rows.Scan(&c.Category, &c.Required, &c.LeadDays); err != nil {
			h.log.Error().Err(err).Msg("failed to scan config entry")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		configMap[c.Category] = c
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	configs := make([]configEntry, 0, len(defaultCategories))
	for _, dc := range defaultCategories {
		if c, ok := configMap[dc.Name]; ok {
			configs = append(configs, c)
		} else {
			configs = append(configs, configEntry{
				Category: dc.Name,
				Required: false,
				LeadDays: nil,
			})
		}
	}

	JSON(w, http.StatusOK, map[string]any{"categories": configs})
}

func (h *NotificationsHandler) HandleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Category == "" {
		Error(w, http.StatusBadRequest, "category is required")
		return
	}

	validCategory := false
	for _, dc := range defaultCategories {
		if dc.Name == req.Category {
			validCategory = true
			break
		}
	}
	if !validCategory {
		Error(w, http.StatusBadRequest, "unknown category")
		return
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO notification_config (club_id, category, required, lead_days)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (club_id, category) DO UPDATE SET required = $3, lead_days = $4`,
		claims.ClubID, req.Category, req.Required, req.LeadDays,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert notification config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"category":  req.Category,
		"required":  req.Required,
		"lead_days": req.LeadDays,
	})
}

// Error codes the test-push route answers with, so the SPA can say what to
// do instead of showing a raw message.
const (
	codePushNotConfigured  = "push_not_configured"
	codeNoPushSubscription = "no_push_subscription"
)

// PushTestRateLimit is how many test pushes a member may send per minute;
// each one reaches an outside push service. routes.go mounts it with
// PushTestRateKey.
const PushTestRateLimit = 5

// PushTestRateKey buckets the test-push limiter per user (IP bucket before
// login), apart from every other limiter's budget.
func PushTestRateKey(r *http.Request) string {
	return "push-test:" + middleware.UserOrIPKey(r)
}

// pushSendTimeout bounds one delivery to a push service, so a slow service
// cannot hold the request (and the member's button) open.
const pushSendTimeout = 10 * time.Second

// testPushCopy is the test notification's title (with the club name) and
// body, per UI language (the clubs/users language CHECK list).
var testPushCopy = map[string]struct{ Title, Body string }{
	"nb": {"Testvarsel fra %s", "Varsler fungerer på denne enheten."},
	"nn": {"Testvarsel frå %s", "Varslar fungerer på denne eininga."},
	"en": {"Test notification from %s", "Notifications work on this device."},
	"de": {"Testbenachrichtigung von %s", "Benachrichtigungen funktionieren auf diesem Gerät."},
	"fr": {"Notification de test de %s", "Les notifications fonctionnent sur cet appareil."},
	"it": {"Notifica di prova da %s", "Le notifiche funzionano su questo dispositivo."},
	"nl": {"Testmelding van %s", "Meldingen werken op dit apparaat."},
	"pl": {"Powiadomienie testowe od %s", "Powiadomienia działają na tym urządzeniu."},
}

// testPushPayload is what the service worker (frontend/public/sw.js) shows:
// the title names the club, the text is in the member's language (their own
// choice, else the club's default, else Bokmål). Tapping it opens the
// member's notifications page.
func testPushPayload(locale, clubName string) []byte {
	c, ok := testPushCopy[locale]
	if !ok {
		c = testPushCopy["nb"]
	}
	if strings.TrimSpace(clubName) == "" {
		clubName = "Brygge"
	}
	b, _ := json.Marshal(map[string]string{
		"title":    fmt.Sprintf(c.Title, clubName),
		"body":     c.Body,
		"category": "test",
		"url":      "/portal/notifications",
	})
	return b
}

// testPushResult counts what happened to each of the caller's subscriptions.
type testPushResult struct {
	Sent    int `json:"sent"`
	Removed int `json:"removed"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

// HandleTestPush sends a test notification to the caller's own push
// subscriptions only (BRY-682), so a member can check that a device, such
// as an iPhone with Brygge on the Home Screen, actually receives pushes.
// A subscription the push service reports gone (404/410) is deleted.
func (h *NotificationsHandler) HandleTestPush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if h.config.VAPIDPublicKey == "" || h.config.VAPIDPrivateKey == "" {
		JSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "push notifications are not configured on this server",
			"code":  codePushNotConfigured,
		})
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT endpoint, p256dh, auth FROM push_subscriptions
		 WHERE user_id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query push subscriptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	subs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pushSubscriptionRow, error) {
		var s pushSubscriptionRow
		err := row.Scan(&s.Endpoint, &s.P256dh, &s.Auth)
		return s, err
	})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to read push subscriptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if len(subs) == 0 {
		JSON(w, http.StatusConflict, map[string]string{
			"error": "no device is subscribed to push notifications for this user",
			"code":  codeNoPushSubscription,
		})
		return
	}

	locale, clubName := h.testPushLocale(ctx, claims.UserID, claims.ClubID)
	payload := testPushPayload(locale, clubName)

	res := testPushResult{Total: len(subs)}
	for _, s := range subs {
		out, err := h.push.Send(ctx, s, payload)
		host := pushServiceHost(s.Endpoint)
		switch {
		case err != nil:
			h.log.Warn().Err(err).Str("push_host", host).Msg("failed to send test push")
			res.Failed++
		case out.Status == http.StatusNotFound || out.Status == http.StatusGone:
			if _, err := h.db.Exec(ctx,
				`DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2`,
				s.Endpoint, claims.UserID,
			); err != nil {
				h.log.Error().Err(err).Msg("failed to delete expired push subscription")
				res.Failed++
				continue
			}
			h.log.Info().Int("status", out.Status).Str("push_host", host).Msg("removed expired push subscription")
			res.Removed++
		case out.Status >= 200 && out.Status < 300:
			res.Sent++
		default:
			// The host and the service's reason (Apple: {"reason":"BadJwtToken"})
			// are what an operator needs; the endpoint path is a bearer
			// capability for the device and is never logged.
			h.log.Warn().Int("status", out.Status).Str("push_host", host).
				Str("push_response", out.Body).Msg("push service refused test push")
			res.Failed++
		}
	}

	JSON(w, http.StatusOK, res)
}

// testPushLocale is the member's UI language (their preference, else the
// club default) and the club's display name, as the join mail resolves it.
func (h *NotificationsHandler) testPushLocale(ctx context.Context, userID, clubID string) (locale, clubName string) {
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(u.preferred_language, c.default_language, ''), c.name
		   FROM users u JOIN clubs c ON c.id = u.club_id
		  WHERE u.id = $1 AND c.id = $2`, userID, clubID,
	).Scan(&locale, &clubName); err != nil {
		h.log.Warn().Err(err).Msg("test push: language unreadable, using the default")
	}
	if h.config.ClubName != "" {
		clubName = h.config.ClubName
	}
	return locale, clubName
}

// pushServiceHost is the push service's host from a subscription endpoint
// (web.push.apple.com, fcm.googleapis.com, …), safe to log.
func pushServiceHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}

// pushOutcome is the push service's answer to one send.
type pushOutcome struct {
	Status int
	// Body is the start of a non-2xx response body (the service's reason),
	// empty on success.
	Body string
}

// pushSender delivers one encrypted push message. The handler holds it as a
// dependency so tests can stand in for the push service.
type pushSender interface {
	// Send returns the push service's answer, or an error when the request
	// could not be made (including the per-send timeout).
	Send(ctx context.Context, sub pushSubscriptionRow, payload []byte) (pushOutcome, error)
}

// webpushSender sends through webpush-go with the server's VAPID keys.
type webpushSender struct {
	config *config.Config
	// client is the HTTP client to the push service; nil means one with
	// pushSendTimeout. Tests set a fake.
	client webpush.HTTPClient
}

// pushResponseLogLimit caps how much of a refusal body is kept for the log.
const pushResponseLogLimit = 200

func (s webpushSender) Send(ctx context.Context, sub pushSubscriptionRow, payload []byte) (pushOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, pushSendTimeout)
	defer cancel()

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: pushSendTimeout}
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      client,
		VAPIDPublicKey:  s.config.VAPIDPublicKey,
		VAPIDPrivateKey: s.config.VAPIDPrivateKey,
		Subscriber:      vapidSubscriber(s.config),
		TTL:             60,
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		return pushOutcome{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	out := pushOutcome{Status: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, pushResponseLogLimit))
		out.Body = strings.ToValidUTF8(string(b), "")
	}
	return out, nil
}

// vapidSubscriber is the contact in the VAPID JWT "sub" claim. webpush-go
// prefixes "mailto:" to anything that is not an https URL, so a value that
// already starts with "mailto:" becomes "mailto:mailto:…", which Apple's push
// service refuses (403 BadJwtToken). The club's own site is the contact.
func vapidSubscriber(cfg *config.Config) string {
	if strings.HasPrefix(cfg.FrontendURL, "https:") {
		return cfg.FrontendURL
	}
	return "admin@" + cfg.Domain
}
