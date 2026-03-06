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

	userID, clubID, roles, err := h.upsertVippsUser(ctx, userInfo)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to upsert vipps user")
		Error(w, http.StatusInternalServerError, "failed to create or update user")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(userID, clubID, roles)
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

	var clubID string
	err = h.db.QueryRow(r.Context(),
		`SELECT id FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to resolve club")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID string
	err = h.db.QueryRow(r.Context(),
		`INSERT INTO users (club_id, email, password_hash, full_name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		clubID, req.Email, hash, req.FullName,
	).Scan(&userID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert user")
		Error(w, http.StatusConflict, "user already exists or registration failed")
		return
	}

	_, err = h.db.Exec(r.Context(),
		`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'applicant')`,
		userID, clubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to assign default role")
		Error(w, http.StatusInternalServerError, "internal error")
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

	var userID, clubID, passwordHash string
	err := h.db.QueryRow(r.Context(),
		`SELECT u.id, u.club_id, u.password_hash
		 FROM users u
		 JOIN clubs c ON c.id = u.club_id
		 WHERE u.email = $1 AND c.slug = $2`,
		req.Email, h.config.ClubSlug,
	).Scan(&userID, &clubID, &passwordHash)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	roles, err := h.getUserRoles(r.Context(), userID, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch user roles")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(userID, clubID, roles)
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

	var clubID string
	err = h.db.QueryRow(r.Context(),
		`SELECT club_id FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&clubID)
	if err != nil {
		Error(w, http.StatusUnauthorized, "user not found")
		return
	}

	roles, err := h.getUserRoles(r.Context(), claims.UserID, clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch user roles")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, err := h.jwt.GenerateAccessToken(claims.UserID, clubID, roles)
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

	var fullName, email string
	_ = h.db.QueryRow(r.Context(),
		`SELECT full_name, email FROM users WHERE id = $1`, claims.UserID,
	).Scan(&fullName, &email)

	JSON(w, http.StatusOK, meResponse{
		UserID:   claims.UserID,
		ClubID:   claims.ClubID,
		Roles:    claims.Roles,
		FullName: fullName,
		Email:    email,
	})
}

func (h *AuthHandler) getUserRoles(ctx context.Context, userID, clubID string) ([]string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT role FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying user roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scanning role: %w", err)
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (h *AuthHandler) upsertVippsUser(ctx context.Context, info *auth.VippsUserInfo) (string, string, []string, error) {
	var clubID string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&clubID)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolving club: %w", err)
	}

	var userID string
	err = h.db.QueryRow(ctx,
		`SELECT id FROM users WHERE vipps_sub = $1 AND club_id = $2`,
		info.Sub, clubID,
	).Scan(&userID)

	if err == pgx.ErrNoRows {
		err = h.db.QueryRow(ctx,
			`INSERT INTO users (club_id, vipps_sub, email, full_name, phone, address_line, postal_code, city)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING id`,
			clubID, info.Sub, info.Email, info.Name, info.Phone,
			info.Address.Street, info.Address.PostalCode, info.Address.City,
		).Scan(&userID)
		if err != nil {
			return "", "", nil, fmt.Errorf("inserting vipps user: %w", err)
		}

		_, err = h.db.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'applicant')`,
			userID, clubID,
		)
		if err != nil {
			return "", "", nil, fmt.Errorf("assigning default role: %w", err)
		}

		return userID, clubID, []string{"applicant"}, nil
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("looking up vipps user: %w", err)
	}

	_, err = h.db.Exec(ctx,
		`UPDATE users SET email = $1, full_name = $2, phone = $3,
		 address_line = $4, postal_code = $5, city = $6,
		 updated_at = now()
		 WHERE id = $7`,
		info.Email, info.Name, info.Phone,
		info.Address.Street, info.Address.PostalCode, info.Address.City,
		userID,
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("updating vipps user: %w", err)
	}

	roles, err := h.getUserRoles(ctx, userID, clubID)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetching roles: %w", err)
	}

	return userID, clubID, roles, nil
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
	UserID   string   `json:"user_id"`
	ClubID   string   `json:"club_id"`
	Roles    []string `json:"roles"`
	FullName string   `json:"full_name"`
	Email    string   `json:"email"`
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
