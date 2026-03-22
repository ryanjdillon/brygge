package handlers

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type TOTPHandler struct {
	db       *pgxpool.Pool
	config   *config.Config
	sessions *auth.SessionService
	audit    *audit.Service
	log      zerolog.Logger
	encKey   []byte
}

func NewTOTPHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	sessions *auth.SessionService,
	auditService *audit.Service,
	log zerolog.Logger,
) *TOTPHandler {
	var encKey []byte
	if cfg.TOTPEncryptionKey != "" {
		var err error
		encKey, err = hex.DecodeString(cfg.TOTPEncryptionKey)
		if err != nil || len(encKey) != 32 {
			log.Fatal().Msg("TOTP_ENCRYPTION_KEY must be 64 hex characters (32 bytes)")
		}
	}
	return &TOTPHandler{
		db:       db,
		config:   cfg,
		sessions: sessions,
		audit:    auditService,
		log:      log.With().Str("handler", "totp").Logger(),
		encKey:   encKey,
	}
}

type totpSetupResponse struct {
	Secret string `json:"secret"`
	QRURL  string `json:"qr_url"`
}

// HandleSetup generates a TOTP secret and returns it with a QR URL.
// The secret is NOT stored yet — it must be confirmed with a valid code first.
func (h *TOTPHandler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Brygge",
		AccountName: claims.UserID,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate TOTP key")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, totpSetupResponse{
		Secret: key.Secret(),
		QRURL:  key.URL(),
	})
}

type totpCodeRequest struct {
	Code   string `json:"code"`
	Secret string `json:"secret,omitempty"`
}

// HandleConfirm validates a TOTP code against the provided secret,
// then encrypts and stores the secret, enabling TOTP for the user.
func (h *TOTPHandler) HandleConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if len(h.encKey) == 0 {
		Error(w, http.StatusServiceUnavailable, "TOTP not configured (missing encryption key)")
		return
	}

	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Code == "" || req.Secret == "" {
		Error(w, http.StatusBadRequest, "code and secret are required")
		return
	}

	if !totp.Validate(req.Code, req.Secret) {
		Error(w, http.StatusBadRequest, "invalid TOTP code")
		return
	}

	encrypted, err := auth.Encrypt(h.encKey, []byte(req.Secret))
	if err != nil {
		h.log.Error().Err(err).Msg("failed to encrypt TOTP secret")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = h.db.Exec(ctx,
		`UPDATE users SET totp_secret_encrypted = $1, totp_enabled = true WHERE id = $2`,
		encrypted, claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store TOTP secret")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"message": "TOTP enabled"})
}

// HandleVerify validates a TOTP code and stamps the session.
func (h *TOTPHandler) HandleVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionID := middleware.GetSessionID(ctx)
	if sessionID == "" {
		Error(w, http.StatusUnauthorized, "session required")
		return
	}

	if len(h.encKey) == 0 {
		Error(w, http.StatusServiceUnavailable, "TOTP not configured")
		return
	}

	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Code == "" {
		Error(w, http.StatusBadRequest, "code is required")
		return
	}

	// Fetch encrypted secret
	var encryptedSecret []byte
	var totpEnabled bool
	err := h.db.QueryRow(ctx,
		`SELECT totp_secret_encrypted, totp_enabled FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&encryptedSecret, &totpEnabled)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch TOTP secret")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if !totpEnabled || encryptedSecret == nil {
		Error(w, http.StatusBadRequest, "TOTP is not enabled for this account")
		return
	}

	secret, err := auth.Decrypt(h.encKey, encryptedSecret)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to decrypt TOTP secret")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if !totp.Validate(req.Code, string(secret)) {
		Error(w, http.StatusUnauthorized, "invalid TOTP code")
		return
	}

	if err := h.sessions.StampTOTP(ctx, sessionID); err != nil {
		h.log.Error().Err(err).Msg("failed to stamp TOTP on session")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionTOTPVerified, "user", claims.UserID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "TOTP verified"})
}
