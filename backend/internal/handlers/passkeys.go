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

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/passkey"
)

// passkeyChallengeTTL bounds how long a begin→finish ceremony may take.
const passkeyChallengeTTL = 5 * time.Minute

// PasskeyHandler serves the WebAuthn ceremonies: registration + management
// here (BRY-249); step-up and login are added by later sub-issues.
type PasskeyHandler struct {
	wa     *webauthn.WebAuthn
	store  *passkey.Store
	chal   *passkey.ChallengeStore
	config *config.Config
	log    zerolog.Logger
}

func NewPasskeyHandler(
	wa *webauthn.WebAuthn,
	store *passkey.Store,
	chal *passkey.ChallengeStore,
	cfg *config.Config,
	log zerolog.Logger,
) *PasskeyHandler {
	return &PasskeyHandler{
		wa:     wa,
		store:  store,
		chal:   chal,
		config: cfg,
		log:    log.With().Str("handler", "passkey").Logger(),
	}
}

// challenge keys are namespaced by ceremony so a register challenge can't be
// replayed against a step-up finish.
func regChallengeKey(sessionID string) string { return "reg:" + sessionID }

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
