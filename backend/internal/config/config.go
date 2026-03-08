package config

import (
	"os"
	"strconv"
	"time"
)

const ClubSlug = "brygge"

type Config struct {
	Port        int
	DatabaseURL string
	RedisURL    string
	ClubSlug    string

	// Authentication
	JWTSecret        string
	JWTAccessExpiry  time.Duration
	JWTRefreshExpiry time.Duration

	// Vipps
	VippsClientID     string
	VippsClientSecret string
	VippsCallbackURL  string
	VippsTestMode     bool

	// Vipps ePayment
	VippsMSN             string
	VippsSubscriptionKey string
	VippsWebhookSecret   string

	// Frontend
	FrontendURL string

	// Object storage
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	// Dendrite (Matrix)
	DendriteInternalURL  string
	DendriteServiceToken string

	// Web Push (VAPID)
	VAPIDPublicKey  string
	VAPIDPrivateKey string

	// Optional integrations
	ResendAPIKey    string
	AnthropicAPIKey string

	// Feature flags
	Features Features
}

type Features struct {
	Bookings       bool
	Projects       bool
	Calendar       bool
	Commerce       bool
	Communications bool
}

func Load() Config {
	return Config{
		Port:        envInt("PORT", 8080),
		DatabaseURL: envStr("DATABASE_URL", "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"),
		RedisURL:    envStr("REDIS_URL", "redis://localhost:6379/0"),
		ClubSlug: ClubSlug,

		JWTSecret:        envStr("JWT_SECRET", ""),
		JWTAccessExpiry:  envDuration("JWT_ACCESS_EXPIRY", 15*time.Minute),
		JWTRefreshExpiry: envDuration("JWT_REFRESH_EXPIRY", 7*24*time.Hour),

		VippsClientID:     envStr("VIPPS_CLIENT_ID", ""),
		VippsClientSecret: envStr("VIPPS_CLIENT_SECRET", ""),
		VippsCallbackURL:  envStr("VIPPS_CALLBACK_URL", ""),
		VippsTestMode:     envBool("VIPPS_TEST_MODE", true),

		VippsMSN:             envStr("VIPPS_MSN", ""),
		VippsSubscriptionKey: envStr("VIPPS_SUBSCRIPTION_KEY", ""),
		VippsWebhookSecret:   envStr("VIPPS_WEBHOOK_SECRET", ""),

		FrontendURL: envStr("FRONTEND_URL", "http://localhost:5173"),

		S3Endpoint:  envStr("S3_ENDPOINT", ""),
		S3Bucket:    envStr("S3_BUCKET", "brygge"),
		S3AccessKey: envStr("S3_ACCESS_KEY", ""),
		S3SecretKey: envStr("S3_SECRET_KEY", ""),

		DendriteInternalURL:  envStr("DENDRITE_INTERNAL_URL", "http://dendrite:8008"),
		DendriteServiceToken: envStr("DENDRITE_SERVICE_TOKEN", ""),

		VAPIDPublicKey:  envStr("VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey: envStr("VAPID_PRIVATE_KEY", ""),

		ResendAPIKey:    envStr("RESEND_API_KEY", ""),
		AnthropicAPIKey: envStr("ANTHROPIC_API_KEY", ""),

		Features: Features{
			Bookings:       envBool("FEATURE_BOOKINGS", true),
			Projects:       envBool("FEATURE_PROJECTS", true),
			Calendar:       envBool("FEATURE_CALENDAR", true),
			Commerce:       envBool("FEATURE_COMMERCE", true),
			Communications: envBool("FEATURE_COMMUNICATIONS", true),
		},
	}
}

// VippsBaseURL returns the Vipps API base URL for server-to-server calls.
func (c *Config) VippsBaseURL() string {
	if c.VippsTestMode && c.VippsMSN == "" {
		return envStr("VIPPS_MOCK_URL", "http://vipps-mock:8090")
	}
	if c.VippsTestMode {
		return "https://apitest.vipps.no"
	}
	return "https://api.vipps.no"
}

// VippsBrowserURL returns the Vipps base URL for browser redirects.
// In mock mode this is localhost:8090 (accessible from browser), otherwise same as VippsBaseURL.
func (c *Config) VippsBrowserURL() string {
	if c.VippsTestMode && c.VippsMSN == "" {
		return envStr("VIPPS_MOCK_BROWSER_URL", "http://localhost:8090")
	}
	return c.VippsBaseURL()
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

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
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
