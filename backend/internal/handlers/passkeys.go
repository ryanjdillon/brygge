package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/passkey"
)

// passkeyChallengeTTL bounds how long a begin→finish ceremony may take.
const passkeyChallengeTTL = 5 * time.Minute

// PasskeyHandler serves the WebAuthn ceremonies: registration + management
// (BRY-249) and step-up (BRY-250); login is added by a later sub-issue.
type PasskeyHandler struct {
	wa       *webauthn.WebAuthn
	store    *passkey.Store
	chal     *passkey.ChallengeStore
	sessions *auth.SessionService
	audit    *audit.Service
	config   *config.Config
	log      zerolog.Logger
}

func NewPasskeyHandler(
	wa *webauthn.WebAuthn,
	store *passkey.Store,
	chal *passkey.ChallengeStore,
	sessions *auth.SessionService,
	auditService *audit.Service,
	cfg *config.Config,
	log zerolog.Logger,
) *PasskeyHandler {
	return &PasskeyHandler{
		wa:       wa,
		store:    store,
		chal:     chal,
		sessions: sessions,
		audit:    auditService,
		config:   cfg,
		log:      log.With().Str("handler", "passkey").Logger(),
	}
}

// challenge keys are namespaced by ceremony so a register challenge can't be
// replayed against a step-up finish, or vice versa.
func regChallengeKey(sessionID string) string    { return "reg:" + sessionID }
func stepUpChallengeKey(sessionID string) string { return "step:" + sessionID }

// HandleRegisterBegin starts enrollment for the logged-in member and returns
// the creation options for @simplewebauthn/browser's startRegistration. Gated
// upstream by fresh TOTP — adding a factor is a step-up action.
func (h *PasskeyHandler) HandleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	sessionID := middleware.GetSessionID(ctx)
	if claims == nil || sessionID == "" {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	user, err := h.store.LoadUser(ctx, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("load user for registration")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	options, sessionData, err := h.wa.BeginRegistration(
		user,
		webauthn.WithExclusions(credentialExclusions(user)),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
	)
	if err != nil {
		h.log.Error().Err(err).Msg("begin registration")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.chal.Put(ctx, regChallengeKey(sessionID), sessionData, passkeyChallengeTTL); err != nil {
		h.log.Error().Err(err).Msg("store registration challenge")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// options.Response is the inner PublicKeyCredentialCreationOptions that
	// startRegistration({ optionsJSON }) expects.
	JSON(w, http.StatusOK, options.Response)
}

type passkeyRegisterFinishResponse struct {
	Message string `json:"message"`
	ID      string `json:"id"`
}

// HandleRegisterFinish verifies the attestation and persists the credential.
func (h *PasskeyHandler) HandleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	sessionID := middleware.GetSessionID(ctx)
	if claims == nil || sessionID == "" {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionData, err := h.chal.Take(ctx, regChallengeKey(sessionID))
	if err != nil {
		Error(w, http.StatusBadRequest, "no registration in progress — start again")
		return
	}

	user, err := h.store.LoadUser(ctx, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("load user for finish")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	cred, err := h.wa.FinishRegistration(user, *sessionData, r)
	if err != nil {
		// Verification failures are the client's problem (bad attestation,
		// origin mismatch, expired challenge).
		Error(w, http.StatusBadRequest, "passkey registration failed")
		return
	}

	if err := h.store.SaveCredential(ctx, claims.UserID, claims.ClubID, cred, ""); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			Error(w, http.StatusConflict, "this passkey is already registered")
			return
		}
		h.log.Error().Err(err).Msg("save credential")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, passkeyRegisterFinishResponse{Message: "passkey registered"})
}

type passkeyListResponse struct {
	Passkeys []passkey.CredentialInfo `json:"passkeys"`
}

// HandleList returns the member's enrolled passkeys (metadata only).
func (h *PasskeyHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	list, err := h.store.ListCredentials(ctx, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("list credentials")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, passkeyListResponse{Passkeys: list})
}

type passkeyRenameRequest struct {
	Name string `json:"name"`
}

// HandleRename sets the user-facing label on one of the member's passkeys.
func (h *PasskeyHandler) HandleRename(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var req passkeyRenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := trimName(req.Name)
	if name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := h.store.RenameCredential(ctx, claims.UserID, id, name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "passkey not found")
			return
		}
		h.log.Error().Err(err).Msg("rename credential")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]string{"message": "renamed"})
}

// HandleDelete removes one of the member's passkeys. Gated upstream by fresh
// TOTP — removing a factor is a step-up action.
func (h *PasskeyHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	if err := h.store.DeleteCredential(ctx, claims.UserID, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "passkey not found")
			return
		}
		h.log.Error().Err(err).Msg("delete credential")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// HandleStepUpBegin starts a passkey assertion for the logged-in member to
// satisfy the admin step-up in place of a TOTP code. User verification is
// required — the passkey then proves possession + inherence in one gesture.
func (h *PasskeyHandler) HandleStepUpBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	sessionID := middleware.GetSessionID(ctx)
	if claims == nil || sessionID == "" {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	user, err := h.store.LoadUser(ctx, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("load user for step-up")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(user.WebAuthnCredentials()) == 0 {
		Error(w, http.StatusBadRequest, "no passkey enrolled")
		return
	}

	// BeginLogin scopes allowCredentials to this user's registered passkeys.
	options, sessionData, err := h.wa.BeginLogin(
		user,
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		h.log.Error().Err(err).Msg("begin step-up")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.chal.Put(ctx, stepUpChallengeKey(sessionID), sessionData, passkeyChallengeTTL); err != nil {
		h.log.Error().Err(err).Msg("store step-up challenge")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, options.Response)
}

type passkeyStepUpFinishResponse struct {
	Message   string `json:"message"`
	MFAMethod string `json:"mfa_method"`
}

// HandleStepUpFinish verifies the assertion and stamps the session's step-up
// timestamp, satisfying RequireAdminTOTP / RequireFreshTOTP.
func (h *PasskeyHandler) HandleStepUpFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	sessionID := middleware.GetSessionID(ctx)
	if claims == nil || sessionID == "" {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionData, err := h.chal.Take(ctx, stepUpChallengeKey(sessionID))
	if err != nil {
		Error(w, http.StatusBadRequest, "no step-up in progress — start again")
		return
	}

	user, err := h.store.LoadUser(ctx, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("load user for step-up finish")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	cred, err := h.wa.FinishLogin(user, *sessionData, r)
	if err != nil {
		Error(w, http.StatusBadRequest, "passkey verification failed")
		return
	}
	if cred.Authenticator.CloneWarning {
		// Signature counter went backwards — possible cloned authenticator.
		// Don't hard-fail (some platform authenticators always report 0), but
		// leave a trail.
		h.log.Warn().Str("user_id", claims.UserID).Msg("passkey clone warning during step-up")
	}

	// Persist the updated signature counter / last-used before granting step-up.
	if err := h.store.TouchCredential(ctx, cred); err != nil {
		h.log.Error().Err(err).Msg("touch credential after step-up")
	}

	if err := h.sessions.StampStepUp(ctx, sessionID, "passkey"); err != nil {
		h.log.Error().Err(err).Msg("stamp step-up")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, passkeyStepUpFinishResponse{Message: "verified", MFAMethod: "passkey"})
}

type passkeyAdminResetResponse struct {
	Message string `json:"message"`
	Removed int    `json:"removed"`
}

// HandleAdminReset wipes another member's passkeys (recovery backstop, BRY-251):
// a locked-out board member re-bootstraps via magic link → re-enrolls. TOTP +
// recovery codes remain the break-glass, so a member who kept TOTP is never
// fully locked out of step-up. Gated upstream by admin role + fresh TOTP.
func (h *PasskeyHandler) HandleAdminReset(w http.ResponseWriter, r *http.Request) {
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

	// Tenant scoping — an admin can only reset members of their own club.
	targetClub, err := h.store.UserClubID(ctx, targetID)
	if err != nil {
		if errors.Is(err, passkey.ErrNoUser) {
			Error(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error().Err(err).Msg("look up target user")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetClub != actor.ClubID {
		Error(w, http.StatusNotFound, "user not found")
		return
	}

	removed, err := h.store.DeleteAllForUser(ctx, targetID)
	if err != nil {
		h.log.Error().Err(err).Msg("delete target passkeys")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Revoke the target's sessions so a session that already stepped up via the
	// now-removed passkey can't linger.
	if err := h.sessions.DeleteUserSessions(ctx, targetID); err != nil {
		h.log.Error().Err(err).Msg("revoke target sessions")
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, actor.ClubID, actor.UserID, r.RemoteAddr,
			audit.ActionPasskeyAdminReset, "user", targetID, nil)
	}

	JSON(w, http.StatusOK, passkeyAdminResetResponse{
		Message: "passkeys removed; the member can re-enroll after signing in",
		Removed: removed,
	})
}

// credentialExclusions lists a member's existing credentials so an authenticator
// already enrolled won't be offered again.
func credentialExclusions(user *passkey.User) []protocol.CredentialDescriptor {
	creds := user.WebAuthnCredentials()
	out := make([]protocol.CredentialDescriptor, 0, len(creds))
	for i := range creds {
		out = append(out, creds[i].Descriptor())
	}
	return out
}

func trimName(s string) string {
	const maxLen = 64
	r := []rune(s)
	// trim surrounding whitespace
	for len(r) > 0 && (r[0] == ' ' || r[0] == '\t') {
		r = r[1:]
	}
	for len(r) > 0 && (r[len(r)-1] == ' ' || r[len(r)-1] == '\t') {
		r = r[:len(r)-1]
	}
	if len(r) > maxLen {
		r = r[:maxLen]
	}
	return string(r)
}
