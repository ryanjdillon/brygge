package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// recoveryCodeCount is how many single-use codes are generated each
// time the user enrolls or rotates. 10 matches GitHub/Google convention.
const recoveryCodeCount = 10

// recoveryAlphabet is base32 minus visually-ambiguous characters
// (0/O, 1/I/L). Codes are short enough that the user has to copy them;
// optimize for low transcription error.
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

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

// totpConfirmResponse returns the freshly-generated recovery codes.
// They appear in this response ONCE and never again — the SPA must
// surface them to the user and require explicit confirmation that
// they've been saved before navigating away.
type totpConfirmResponse struct {
	Message       string   `json:"message"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// HandleConfirm validates a TOTP code against the provided secret,
// then encrypts and stores the secret, generates recovery codes,
// and returns the codes in plaintext one time.
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

	codes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`UPDATE users SET totp_secret_encrypted = $1, totp_enabled = true WHERE id = $2`,
		encrypted, claims.UserID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to store TOTP secret")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Replace any pre-existing codes (e.g. user is re-enrolling).
	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, claims.UserID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to clear old recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := insertRecoveryCodes(ctx, tx, claims.UserID, codes); err != nil {
		h.log.Error().Err(err).Msg("failed to store recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit TOTP enrollment")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// The 6-digit code the user just submitted to /confirm came from
	// their authenticator — that's exactly what /verify checks. Treat
	// successful enrollment as proof-of-possession and stamp the
	// session, so a user who arrived here from a 12h-gate redirect
	// (e.g. clicked Admin → got bounced to verify-totp → bounced
	// again to /portal/security) walks straight into their original
	// destination after enrolling, instead of having to do the
	// authenticator-code dance a second time.
	if sessionID := middleware.GetSessionID(ctx); sessionID != "" && h.sessions != nil {
		if err := h.sessions.StampTOTP(ctx, sessionID); err != nil {
			h.log.Warn().Err(err).Msg("failed to stamp TOTP after enrollment (non-fatal)")
		}
	}

	JSON(w, http.StatusOK, totpConfirmResponse{
		Message:       "TOTP enabled",
		RecoveryCodes: codes,
	})
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

type totpRecoverRequest struct {
	Code string `json:"code"`
}

// HandleRecover redeems a single-use recovery code: the user's
// authenticator is unavailable (lost device), so they prove possession
// by presenting one of the codes generated at enrollment. On success
// the session is stamped (same effect as HandleVerify), the code is
// marked used, and the action is audit-logged.
func (h *TOTPHandler) HandleRecover(w http.ResponseWriter, r *http.Request) {
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

	var req totpRecoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	candidate := normalizeRecoveryCode(req.Code)
	if candidate == "" {
		Error(w, http.StatusBadRequest, "code is required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT code_hash FROM totp_recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	candidateBytes := []byte(candidate)
	var matchedHash string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			h.log.Error().Err(err).Msg("failed to scan recovery code hash")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), candidateBytes) == nil {
			matchedHash = hash
			break
		}
	}
	// Drain remaining rows so the query is fully consumed before next call.
	rows.Close()

	if matchedHash == "" {
		Error(w, http.StatusUnauthorized, "invalid or already-used recovery code")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`UPDATE totp_recovery_codes SET used_at = NOW()
		 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		claims.UserID, matchedHash,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to mark recovery code used")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() != 1 {
		// Race: someone else just used this code. Treat as failure.
		Error(w, http.StatusUnauthorized, "invalid or already-used recovery code")
		return
	}

	if err := h.sessions.StampTOTP(ctx, sessionID); err != nil {
		h.log.Error().Err(err).Msg("failed to stamp TOTP on session")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit recovery redemption")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionTOTPRecoveryRedeemed, "user", claims.UserID, nil)
	}

	// Report how many codes the user has left so the SPA can warn
	// when they're running low.
	remaining, _ := h.countUnusedRecoveryCodes(ctx, claims.UserID)

	JSON(w, http.StatusOK, map[string]any{
		"message":         "recovery code accepted",
		"codes_remaining": remaining,
	})
}

type totpRegenerateResponse struct {
	Message       string   `json:"message"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// HandleRegenerateCodes wipes the user's existing recovery codes and
// returns a fresh batch. Gated by RequireFreshTOTP at the route level
// so an attacker with a stale session cookie can't lock the legitimate
// owner out by rotating codes.
func (h *TOTPHandler) HandleRegenerateCodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	codes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, claims.UserID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to clear recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := insertRecoveryCodes(ctx, tx, claims.UserID, codes); err != nil {
		h.log.Error().Err(err).Msg("failed to store new recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit recovery code rotation")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionTOTPCodesRegenerated, "user", claims.UserID, nil)
	}

	JSON(w, http.StatusOK, totpRegenerateResponse{
		Message:       "recovery codes regenerated",
		RecoveryCodes: codes,
	})
}

func (h *TOTPHandler) countUnusedRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM totp_recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	).Scan(&n)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// generateRecoveryCodes produces n codes shaped XXXX-XXXX using a
// reduced alphabet (no 0/O/1/I/L). 8 chars from a 31-char alphabet
// gives ~40 bits of entropy per code — well above brute-force range
// when the codes are bcrypt-hashed at rest.
func generateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, n)
	for i := 0; i < n; i++ {
		var sb strings.Builder
		sb.Grow(9) // 4 + 1 + 4
		for j := 0; j < 8; j++ {
			b := make([]byte, 1)
			for {
				if _, err := rand.Read(b); err != nil {
					return nil, err
				}
				// Reject-and-resample to avoid modulo bias when
				// 256 isn't a multiple of len(alphabet).
				if int(b[0]) < (256/len(recoveryAlphabet))*len(recoveryAlphabet) {
					break
				}
			}
			sb.WriteByte(recoveryAlphabet[int(b[0])%len(recoveryAlphabet)])
			if j == 3 {
				sb.WriteByte('-')
			}
		}
		codes[i] = sb.String()
	}
	return codes, nil
}

// normalizeRecoveryCode uppercases and strips dashes/whitespace so
// both `xxxx-xxxx` (typed) and `XXXXXXXX` (pasted, dashless) match.
// The stored bcrypt is always over the dashed-uppercase form.
func normalizeRecoveryCode(in string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1 // strip dashes, spaces, etc.
		}
	}, in)
	if len(cleaned) != 8 {
		return ""
	}
	return cleaned[:4] + "-" + cleaned[4:]
}

// HandleAdminDisableTOTP is the lost-device backstop: another admin
// disables a target user's TOTP entirely so they can re-enroll from
// scratch via /admin/totp/setup. All of the target's recovery codes
// and active sessions are wiped in the same transaction so any
// in-flight elevated state (a stolen cookie that's somehow still
// valid) loses access immediately.
//
// The acting admin must be fresh-TOTP-verified (RequireFreshTOTP at
// the route level). Audit logged.
func (h *TOTPHandler) HandleAdminDisableTOTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := middleware.GetClaims(ctx)
	if actor == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	targetID := chi.URLParam(r, "userID")
	if targetID == "" {
		Error(w, http.StatusBadRequest, "userID is required")
		return
	}

	// Verify target belongs to the same club so an admin from one
	// tenant can't reset users in another.
	var targetClubID string
	err := h.db.QueryRow(ctx,
		`SELECT club_id FROM users WHERE id = $1`, targetID,
	).Scan(&targetClubID)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to look up target user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetClubID != actor.ClubID {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`UPDATE users SET totp_secret_encrypted = NULL, totp_enabled = false WHERE id = $1`,
		targetID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to disable TOTP")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`, targetID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to clear recovery codes")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = $1`, targetID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to revoke sessions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit admin TOTP disable")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, actor.ClubID, actor.UserID, r.RemoteAddr,
			audit.ActionTOTPAdminDisabled, "user", targetID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{
		"message": "TOTP disabled for user; they will need to re-enroll on next login",
	})
}

// insertRecoveryCodes bcrypts and inserts each plaintext code on the
// supplied transaction. Caller is responsible for the surrounding
// DELETE (when re-issuing) and Commit.
func insertRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string, codes []string) error {
	for _, code := range codes {
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_recovery_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, string(hash),
		); err != nil {
			return err
		}
	}
	return nil
}
