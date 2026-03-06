package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

const (
	oauthStateTTL    = 10 * time.Minute
	oauthStatePrefix = "oauth_state:"
	revokedPrefix    = "revoked_refresh:"
)

type AuthHandler struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	jwt    *auth.JWTService
	vipps  *auth.VippsClient
	config *config.Config
	log    zerolog.Logger
}

func NewAuthHandler(
	db *pgxpool.Pool,
	rdb *redis.Client,
	jwt *auth.JWTService,
	vipps *auth.VippsClient,
	cfg *config.Config,
	log zerolog.Logger,
) *AuthHandler {
	return &AuthHandler{
		db:     db,
		redis:  rdb,
		jwt:    jwt,
		vipps:  vipps,
		config: cfg,
		log:    log.With().Str("handler", "auth").Logger(),
	}
}

func (h *AuthHandler) HandleVippsLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate oauth state")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	key := oauthStatePrefix + state
	if err := h.redis.Set(r.Context(), key, "1", oauthStateTTL).Err(); err != nil {
		h.log.Error().Err(err).Msg("failed to store oauth state")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.Redirect(w, r, h.vipps.AuthorizationURL(state), http.StatusTemporaryRedirect)
}

func (h *AuthHandler) HandleVippsCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		Error(w, http.StatusBadRequest, "missing state or code parameter")
		return
	}

	key := oauthStatePrefix + state
	res, err := h.redis.GetDel(ctx, key).Result()
	if err != nil || res == "" {
		Error(w, http.StatusBadRequest, "invalid or expired oauth state")
		return
	}

	tokenResp, err := h.vipps.ExchangeCode(ctx, code)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to exchange vipps code")
		Error(w, http.StatusBadGateway, "failed to authenticate with vipps")
		return
	}

	userInfo, err := h.vipps.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch vipps user info")
		Error(w, http.StatusBadGateway, "failed to fetch user info from vipps")
		return
	}

	userID, roles, err := h.upsertVippsUser(ctx, userInfo)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert vipps user")
		Error(w, http.StatusInternalServerError, "failed to create or update user")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(userID, h.config.ClubSlug, roles)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate access token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	refreshToken, err := h.jwt.GenerateRefreshToken(userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate refresh token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, tokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	})
}

func (h *AuthHandler) HandleEmailRegister(w http.ResponseWriter, r *http.Request) {
	var req emailRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" || req.FullName == "" {
		Error(w, http.StatusBadRequest, "email, password, and full_name are required")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to hash password")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = h.db.Exec(r.Context(),
		`INSERT INTO users (email, password_hash, full_name, club_slug, auth_provider)
		 VALUES ($1, $2, $3, $4, 'email')`,
		req.Email, hash, req.FullName, h.config.ClubSlug,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert user")
		Error(w, http.StatusConflict, "user already exists or registration failed")
		return
	}

	JSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

func (h *AuthHandler) HandleEmailLogin(w http.ResponseWriter, r *http.Request) {
	var req emailLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		Error(w, http.StatusBadRequest, "email and password are required")
		return
	}

	var userID, passwordHash string
	var roles []string
	err := h.db.QueryRow(r.Context(),
		`SELECT id, password_hash, roles FROM users WHERE email = $1 AND club_slug = $2`,
		req.Email, h.config.ClubSlug,
	).Scan(&userID, &passwordHash, &roles)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(userID, h.config.ClubSlug, roles)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate access token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	refreshToken, err := h.jwt.GenerateRefreshToken(userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate refresh token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, tokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	})
}

func (h *AuthHandler) HandleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RefreshToken == "" {
		Error(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	revoked, _ := h.redis.Exists(r.Context(), revokedPrefix+req.RefreshToken).Result()
	if revoked > 0 {
		Error(w, http.StatusUnauthorized, "token has been revoked")
		return
	}

	claims, err := h.jwt.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	var roles []string
	err = h.db.QueryRow(r.Context(),
		`SELECT roles FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&roles)
	if err != nil {
		Error(w, http.StatusUnauthorized, "user not found")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(claims.UserID, h.config.ClubSlug, roles)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate access token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, tokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	})
}

func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RefreshToken != "" {
		h.redis.Set(r.Context(), revokedPrefix+req.RefreshToken, "1", h.config.JWTRefreshExpiry)
	}

	JSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	JSON(w, http.StatusOK, meResponse{
		UserID: claims.UserID,
		ClubID: claims.ClubID,
		Roles:  claims.Roles,
	})
}

func (h *AuthHandler) upsertVippsUser(ctx context.Context, info *auth.VippsUserInfo) (string, []string, error) {
	var userID string
	var roles []string

	err := h.db.QueryRow(ctx,
		`SELECT id, roles FROM users WHERE vipps_sub = $1 AND club_slug = $2`,
		info.Sub, h.config.ClubSlug,
	).Scan(&userID, &roles)

	if err == pgx.ErrNoRows {
		err = h.db.QueryRow(ctx,
			`INSERT INTO users (vipps_sub, email, full_name, phone, address_street, address_postal_code, address_city, address_country, club_slug, auth_provider, roles)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'vipps', ARRAY['applicant'])
			 RETURNING id, roles`,
			info.Sub, info.Email, info.Name, info.Phone,
			info.Address.Street, info.Address.PostalCode, info.Address.City, info.Address.Country,
			h.config.ClubSlug,
		).Scan(&userID, &roles)
		if err != nil {
			return "", nil, fmt.Errorf("inserting vipps user: %w", err)
		}
		return userID, roles, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("looking up vipps user: %w", err)
	}

	_, err = h.db.Exec(ctx,
		`UPDATE users SET email = $1, full_name = $2, phone = $3,
		 address_street = $4, address_postal_code = $5, address_city = $6, address_country = $7,
		 updated_at = now()
		 WHERE id = $8`,
		info.Email, info.Name, info.Phone,
		info.Address.Street, info.Address.PostalCode, info.Address.City, info.Address.Country,
		userID,
	)
	if err != nil {
		return "", nil, fmt.Errorf("updating vipps user: %w", err)
	}

	return userID, roles, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
}

type emailRegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
	ClubSlug string `json:"club_slug"`
}

type emailLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	ClubSlug string `json:"club_slug"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type meResponse struct {
	UserID string   `json:"user_id"`
	ClubID string   `json:"club_id"`
	Roles  []string `json:"roles"`
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
