package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port        int
	DatabaseURL string
	RedisURL    string
	ClubSlug    string

	// Authentication
	JWTSecret       string
	JWTAccessExpiry time.Duration
	JWTRefreshExpiry time.Duration

	// Vipps
	VippsClientID     string
	VippsClientSecret string
	VippsCallbackURL  string

	// Object storage
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	// Optional integrations
	ResendAPIKey    string
	AnthropicAPIKey string
}

func Load() Config {
	return Config{
		Port:        envInt("PORT", 8080),
		DatabaseURL: envStr("DATABASE_URL", "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"),
		RedisURL:    envStr("REDIS_URL", "redis://localhost:6379/0"),
		ClubSlug:    envStr("CLUB_SLUG", "default"),

		JWTSecret:        envStr("JWT_SECRET", ""),
		JWTAccessExpiry:  envDuration("JWT_ACCESS_EXPIRY", 15*time.Minute),
		JWTRefreshExpiry: envDuration("JWT_REFRESH_EXPIRY", 7*24*time.Hour),

		VippsClientID:     envStr("VIPPS_CLIENT_ID", ""),
		VippsClientSecret: envStr("VIPPS_CLIENT_SECRET", ""),
		VippsCallbackURL:  envStr("VIPPS_CALLBACK_URL", ""),

		S3Endpoint:  envStr("S3_ENDPOINT", ""),
		S3Bucket:    envStr("S3_BUCKET", "brygge"),
		S3AccessKey: envStr("S3_ACCESS_KEY", ""),
		S3SecretKey: envStr("S3_SECRET_KEY", ""),

		ResendAPIKey:    envStr("RESEND_API_KEY", ""),
		AnthropicAPIKey: envStr("ANTHROPIC_API_KEY", ""),
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
