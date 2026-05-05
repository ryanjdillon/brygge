package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type ClubSettingsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewClubSettingsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *ClubSettingsHandler {
	return &ClubSettingsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "club_settings").Logger(),
	}
}

type clubSetting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type updateSettingsRequest struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

func (h *ClubSettingsHandler) HandleGetBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT key, value, updated_at FROM club_settings WHERE club_id = $1 ORDER BY key`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list club settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	settings := make(map[string]json.RawMessage)
	for rows.Next() {
		var s clubSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		settings[s.Key] = s.Value
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, settings)
}

func (h *ClubSettingsHandler) HandleUpdateBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Settings) == 0 {
		Error(w, http.StatusBadRequest, "settings map is required")
		return
	}

	allowedKeys := map[string]bool{
		"hoist_slot_duration_minutes": true,
		"hoist_open_hour":             true,
		"hoist_close_hour":            true,
		"hoist_max_consecutive_slots": true,
		"slip_share_rebate_pct":       true,
		"season_summer_start":         true,
		"season_summer_end":           true,
		"season_winter_start":         true,
		"season_winter_end":           true,
	}

	for key := range req.Settings {
		if !allowedKeys[key] {
			Error(w, http.StatusBadRequest, "unknown setting key: "+key)
			return
		}
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	for key, value := range req.Settings {
		_, err := tx.Exec(ctx,
			`INSERT INTO club_settings (club_id, key, value, updated_at)
			 VALUES ($1, $2, $3, now())
			 ON CONFLICT (club_id, key) DO UPDATE SET value = $3, updated_at = now()`,
			claims.ClubID, key, value,
		)
		if err != nil {
			h.log.Error().Err(err).Str("key", key).Msg("failed to upsert setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_booking_settings", "club_settings", claims.ClubID, nil, req.Settings); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

type clubFinancialSettings struct {
	Name              string `json:"name"`
	OrgNumber         string `json:"org_number"`
	Address           string `json:"address"`
	BankAccount       string `json:"bank_account"`
	WebsiteURL        string `json:"website_url"`
	ChairmanEmail     string `json:"chairman_email"`
	TreasurerEmail    string `json:"treasurer_email"`
	SecretaryEmail    string `json:"secretary_email"`
	HarborMasterEmail string `json:"harbor_master_email"`
	HasLogo           bool   `json:"has_logo"`
	LogoMIME          string `json:"logo_mime"`
}

// HandleGetFinancialSettings returns the club's invoice-relevant
// fields stored on the clubs table (org_number, address, bank_account).
// These render on every faktura PDF.
func (h *ClubSettingsHandler) HandleGetFinancialSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var s clubFinancialSettings
	if err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''), COALESCE(bank_account, ''),
		        COALESCE(website_url, ''),
		        COALESCE(chairman_email, ''), COALESCE(treasurer_email, ''),
		        COALESCE(secretary_email, ''), COALESCE(harbor_master_email, ''),
		        (logo_data IS NOT NULL), COALESCE(logo_mime, '')
		   FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&s.Name, &s.OrgNumber, &s.Address, &s.BankAccount,
		&s.WebsiteURL,
		&s.ChairmanEmail, &s.TreasurerEmail,
		&s.SecretaryEmail, &s.HarborMasterEmail,
		&s.HasLogo, &s.LogoMIME); err != nil {
		h.log.Error().Err(err).Msg("load financial settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, s)
}

type updateFinancialSettingsRequest struct {
	OrgNumber         *string `json:"org_number,omitempty"`
	Address           *string `json:"address,omitempty"`
	BankAccount       *string `json:"bank_account,omitempty"`
	WebsiteURL        *string `json:"website_url,omitempty"`
	ChairmanEmail     *string `json:"chairman_email,omitempty"`
	TreasurerEmail    *string `json:"treasurer_email,omitempty"`
	SecretaryEmail    *string `json:"secretary_email,omitempty"`
	HarborMasterEmail *string `json:"harbor_master_email,omitempty"`
}

// HandleUpdateFinancialSettings updates org_number, address, and
// bank_account on the clubs row. Each field is optional; only supplied
// keys are written.
func (h *ClubSettingsHandler) HandleUpdateFinancialSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req updateFinancialSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OrgNumber == nil && req.Address == nil && req.BankAccount == nil &&
		req.WebsiteURL == nil && req.ChairmanEmail == nil && req.TreasurerEmail == nil &&
		req.SecretaryEmail == nil && req.HarborMasterEmail == nil {
		Error(w, http.StatusBadRequest, "no fields supplied")
		return
	}
	// Build COALESCE-style update so unspecified fields stay as-is.
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET
		   org_number          = COALESCE($2, org_number),
		   address             = COALESCE($3, address),
		   bank_account        = COALESCE($4, bank_account),
		   website_url         = COALESCE($5, website_url),
		   chairman_email      = COALESCE($6, chairman_email),
		   treasurer_email     = COALESCE($7, treasurer_email),
		   secretary_email     = COALESCE($8, secretary_email),
		   harbor_master_email = COALESCE($9, harbor_master_email)
		 WHERE id = $1`,
		claims.ClubID, req.OrgNumber, req.Address, req.BankAccount,
		req.WebsiteURL, req.ChairmanEmail, req.TreasurerEmail,
		req.SecretaryEmail, req.HarborMasterEmail,
	); err != nil {
		h.log.Error().Err(err).Msg("update financial settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_financial_settings", "clubs", claims.ClubID, nil, req); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit financial settings")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

const maxLogoBytes = 2 * 1024 * 1024

// HandleUploadClubLogo accepts a multipart upload (field name "logo").
// Only PNG and JPEG are accepted; SVG is rejected because the PDF
// renderer can't rasterize vector formats.
func (h *ClubSettingsHandler) HandleUploadClubLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := r.ParseMultipartForm(maxLogoBytes + 1024); err != nil {
		Error(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	file, _, err := r.FormFile("logo")
	if err != nil {
		Error(w, http.StatusBadRequest, "logo file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		Error(w, http.StatusBadRequest, "could not read upload")
		return
	}
	if len(data) > maxLogoBytes {
		Error(w, http.StatusRequestEntityTooLarge, "logo exceeds 2 MB")
		return
	}
	mime := http.DetectContentType(data)
	if mime != "image/png" && mime != "image/jpeg" {
		Error(w, http.StatusUnsupportedMediaType, "logo must be PNG or JPEG; SVG and other formats are not supported")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET logo_data = $2, logo_mime = $3 WHERE id = $1`,
		claims.ClubID, data, mime,
	); err != nil {
		h.log.Error().Err(err).Msg("save club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "upload_club_logo", "clubs", claims.ClubID, nil, map[string]any{"mime": mime, "size": len(data)}); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit club logo upload")
	}
	JSON(w, http.StatusOK, map[string]any{"status": "updated", "mime": mime, "size": len(data)})
}

// HandleGetClubLogo streams the stored logo bytes for the caller's
// club. Auth required, scoped to claim's club.
func (h *ClubSettingsHandler) HandleGetClubLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var data []byte
	var mime string
	err := h.db.QueryRow(ctx,
		`SELECT logo_data, COALESCE(logo_mime, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&data, &mime)
	if err == pgx.ErrNoRows || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("load club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, bytes.NewReader(data))
}

// HandleDeleteClubLogo clears the stored logo.
func (h *ClubSettingsHandler) HandleDeleteClubLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET logo_data = NULL, logo_mime = '' WHERE id = $1`,
		claims.ClubID,
	); err != nil {
		h.log.Error().Err(err).Msg("clear club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "delete_club_logo", "clubs", claims.ClubID, nil, nil); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit club logo delete")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
