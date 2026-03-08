package auth

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/brygge-klubb/brygge/internal/config"
)

func newJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type Claims struct {
	jwt.RegisteredClaims
	UserID string   `json:"user_id"`
	ClubID string   `json:"club_id"`
	Roles  []string `json:"roles"`
}

type RefreshClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

type JWTService struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

func NewJWTService(cfg *config.Config) *JWTService {
	return &JWTService{
		secret:        []byte(cfg.JWTSecret),
		accessExpiry:  cfg.JWTAccessExpiry,
		refreshExpiry: cfg.JWTRefreshExpiry,
	}
}

func (s *JWTService) GenerateAccessToken(userID, clubID string, roles []string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        newJTI(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessExpiry)),
			Issuer:    "brygge",
		},
		UserID: userID,
		ClubID: clubID,
		Roles:  roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("signing access token: %w", err)
	}
	return signed, nil
}

func (s *JWTService) GenerateRefreshToken(userID string) (string, error) {
	now := time.Now()
	claims := RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        newJTI(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshExpiry)),
			Issuer:    "brygge",
		},
		UserID: userID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("signing refresh token: %w", err)
	}
	return signed, nil
}

func (s *JWTService) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing access token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid access token claims")
	}
	return claims, nil
}

func (s *JWTService) ValidateRefreshToken(tokenString string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &RefreshClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing refresh token: %w", err)
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid refresh token claims")
	}
	return claims, nil
}
