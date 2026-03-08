package handlers

import (
	"encoding/json"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type NotificationsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewNotificationsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *NotificationsHandler {
	return &NotificationsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "notifications").Logger(),
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
	{Name: "dugnad_reminder", Default: true},
	{Name: "styre_announcement", Default: true},
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
	JSON(w, http.StatusOK, map[string]string{"public_key": h.config.VAPIDPublicKey})
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

func (h *NotificationsHandler) HandleTestPush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if h.config.VAPIDPublicKey == "" || h.config.VAPIDPrivateKey == "" {
		Error(w, http.StatusServiceUnavailable, "VAPID keys not configured")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query push subscriptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var subs []pushSubscriptionRow
	for rows.Next() {
		var s pushSubscriptionRow
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			h.log.Error().Err(err).Msg("failed to scan subscription")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		subs = append(subs, s)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating subscriptions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	payload := `{"title":"Test Notification","body":"Push notifications are working!","category":"test"}`
	sent := 0

	for _, s := range subs {
		sub := &webpush.Subscription{
			Endpoint: s.Endpoint,
			Keys: webpush.Keys{
				P256dh: s.P256dh,
				Auth:   s.Auth,
			},
		}
		resp, err := webpush.SendNotification([]byte(payload), sub, &webpush.Options{
			VAPIDPublicKey:  h.config.VAPIDPublicKey,
			VAPIDPrivateKey: h.config.VAPIDPrivateKey,
			Subscriber:      "mailto:admin@brygge.no",
			TTL:             30,
		})
		if err != nil {
			h.log.Warn().Err(err).Str("endpoint", s.Endpoint).Msg("failed to send test push")
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusGone {
			_, _ = h.db.Exec(ctx,
				`DELETE FROM push_subscriptions WHERE endpoint = $1`,
				s.Endpoint,
			)
			h.log.Info().Str("endpoint", s.Endpoint).Msg("removed expired push subscription")
			continue
		}

		sent++
	}

	JSON(w, http.StatusOK, map[string]any{
		"sent":  sent,
		"total": len(subs),
	})
}
