package handlers

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/unsubscribe"
)

// optOutCategories are the broadcast categories members may opt out of.
// Transactional categories (magic_link, invoice) are intentionally absent.
var optOutCategories = []string{"broadcast"}

type EmailPrefsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
	secret []byte
}

func NewEmailPrefsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *EmailPrefsHandler {
	secret, _ := hex.DecodeString(cfg.TOTPEncryptionKey)
	return &EmailPrefsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "email_prefs").Logger(),
		secret: secret,
	}
}

type emailPref struct {
	Category     string `json:"category"`
	EmailEnabled bool   `json:"email_enabled"`
	CanOptOut    bool   `json:"can_opt_out"`
}

// HandleGetEmailPrefs returns email opt-in status for all opt-outable categories.
func (h *EmailPrefsHandler) HandleGetEmailPrefs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT category, email_enabled
		 FROM communication_preferences
		 WHERE user_id = $1 AND club_id = $2`,
		claims.UserID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query email prefs")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	stored := map[string]bool{}
	for rows.Next() {
		var cat string
		var enabled bool
		if err := rows.Scan(&cat, &enabled); err != nil {
			h.log.Error().Err(err).Msg("failed to scan email pref")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		stored[cat] = enabled
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("email prefs rows error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	prefs := make([]emailPref, 0, len(optOutCategories))
	for _, cat := range optOutCategories {
		enabled := true
		if v, ok := stored[cat]; ok {
			enabled = v
		}
		prefs = append(prefs, emailPref{Category: cat, EmailEnabled: enabled, CanOptOut: true})
	}
	JSON(w, http.StatusOK, prefs)
}

// HandleUpdateEmailPref sets email_enabled for one category.
func (h *EmailPrefsHandler) HandleUpdateEmailPref(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Category     string `json:"category"`
		EmailEnabled bool   `json:"email_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	allowed := false
	for _, c := range optOutCategories {
		if c == req.Category {
			allowed = true
			break
		}
	}
	if !allowed {
		Error(w, http.StatusBadRequest, "category cannot be configured")
		return
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO communication_preferences (user_id, club_id, category, email_enabled)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, club_id, category)
		 DO UPDATE SET email_enabled = EXCLUDED.email_enabled, updated_at = now()`,
		claims.UserID, claims.ClubID, req.Category, req.EmailEnabled,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert email pref")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"category": req.Category, "email_enabled": req.EmailEnabled})
}

// HandleUnsubscribeRedirect handles GET /unsubscribe — redirects to the
// member profile preferences page after verifying the token. Mail clients
// that don't support RFC 8058 one-click will land here.
func (h *EmailPrefsHandler) HandleUnsubscribeRedirect(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	_, category, err := unsubscribe.VerifyToken(token, h.secret)
	if err != nil {
		http.Error(w, "invalid or expired unsubscribe link", http.StatusBadRequest)
		return
	}

	target := h.config.FrontendURL + "/portal/profile?unsubscribe=" + category
	http.Redirect(w, r, target, http.StatusFound)
}

// HandleUnsubscribeOneClick handles POST /unsubscribe as required by RFC 8058.
// Mail clients (Gmail, Apple Mail) POST with body "List-Unsubscribe=One-Click".
func (h *EmailPrefsHandler) HandleUnsubscribeOneClick(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	userID, category, err := unsubscribe.VerifyToken(token, h.secret)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid or expired unsubscribe token")
		return
	}

	allowed := false
	for _, c := range optOutCategories {
		if c == category {
			allowed = true
			break
		}
	}
	if !allowed {
		Error(w, http.StatusBadRequest, "category cannot be opted out")
		return
	}

	var clubID string
	err = h.db.QueryRow(ctx,
		`SELECT club_id FROM users WHERE id = $1`,
		userID,
	).Scan(&clubID)
	if err != nil {
		h.log.Error().Err(err).Str("user_id", userID).Msg("user not found for unsubscribe")
		Error(w, http.StatusBadRequest, "invalid unsubscribe token")
		return
	}

	_, err = h.db.Exec(ctx,
		`INSERT INTO communication_preferences (user_id, club_id, category, email_enabled)
		 VALUES ($1, $2, $3, false)
		 ON CONFLICT (user_id, club_id, category)
		 DO UPDATE SET email_enabled = false, updated_at = now()`,
		userID, clubID, category,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to record one-click unsubscribe")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("user_id", userID).Str("category", category).Msg("one-click unsubscribe")
	JSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}
