package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/devtoken"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// DevTokenHandler lets admins mint, list and revoke developer bearer tokens
// for the read-only dev-query endpoint (BRY-115). The bearer route itself is
// only mounted when DEV_QUERY_TOKENS_ENABLED is set; minting is refused
// otherwise so an admin isn't handed a credential nothing accepts.
type DevTokenHandler struct {
	svc     *devtoken.Service
	audit   *audit.Service
	enabled bool
	log     zerolog.Logger
}

func NewDevTokenHandler(svc *devtoken.Service, auditService *audit.Service, enabled bool, log zerolog.Logger) *DevTokenHandler {
	return &DevTokenHandler{
		svc:     svc,
		audit:   auditService,
		enabled: enabled,
		log:     log.With().Str("handler", "dev_tokens").Logger(),
	}
}

type devTokenListResponse struct {
	Enabled         bool             `json:"enabled"`
	DefaultTTLHours int              `json:"default_ttl_hours"`
	MaxTTLHours     int              `json:"max_ttl_hours"`
	Tokens          []devtoken.Token `json:"tokens"`
}

func (h *DevTokenHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	tokens, err := h.svc.List(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list developer tokens")
		Error(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	JSON(w, http.StatusOK, devTokenListResponse{
		Enabled:         h.enabled,
		DefaultTTLHours: int(devtoken.DefaultTTL / time.Hour),
		MaxTTLHours:     int(devtoken.MaxTTL / time.Hour),
		Tokens:          tokens,
	})
}

type createDevTokenRequest struct {
	Name     string `json:"name"`
	TTLHours int    `json:"ttl_hours"`
}

type createDevTokenResponse struct {
	devtoken.Token
	// Plaintext is returned exactly once; only its hash is stored.
	Plaintext string `json:"token"`
}

func (h *DevTokenHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !h.enabled {
		Error(w, http.StatusConflict, "developer tokens are disabled on this deployment (DEV_QUERY_TOKENS_ENABLED)")
		return
	}
	var req createDevTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TTLHours < 0 {
		Error(w, http.StatusBadRequest, "ttl_hours must be positive")
		return
	}

	plaintext, tok, err := h.svc.Mint(r.Context(), claims.ClubID, claims.UserID, req.Name,
		time.Duration(req.TTLHours)*time.Hour)
	switch {
	case errors.Is(err, devtoken.ErrName), errors.Is(err, devtoken.ErrTTL):
		Error(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		h.log.Error().Err(err).Msg("mint developer token")
		Error(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionDeveloperTokenCreated, "developer_token", tok.ID,
			map[string]any{"name": tok.Name, "prefix": tok.Prefix, "scope": tok.Scope, "expires_at": tok.ExpiresAt})
	}
	JSON(w, http.StatusCreated, createDevTokenResponse{Token: tok, Plaintext: plaintext})
}

func (h *DevTokenHandler) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "tokenID")
	err := h.svc.Revoke(r.Context(), claims.ClubID, id)
	switch {
	case errors.Is(err, devtoken.ErrNotFound):
		Error(w, http.StatusNotFound, "token not found or already revoked")
		return
	case err != nil:
		h.log.Error().Err(err).Msg("revoke developer token")
		Error(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionDeveloperTokenRevoked, "developer_token", id, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}
