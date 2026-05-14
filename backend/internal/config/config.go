package config

import (
	"math"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port        int
	DatabaseURL string
	RedisURL    string
	ClubSlug    string
	ClubName    string // Human-readable club name used in email subjects etc.
	Domain      string // Public domain (DOMAIN env) — used in email footers and canonical URLs

	// Vipps ePayment
	VippsTestMode        bool
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

	// TOTP
	TOTPEncryptionKey string // 32-byte hex-encoded key for encrypting TOTP secrets

	// Optional integrations
	// SMTP (self-hosted mail).
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	EmailFrom    string
	EmailReplyTo string
	AnthropicAPIKey   string

	// Stalwart admin (DIL-275/276). Empty values disable the shared-inbox
	// reconciler; the rest of the app keeps working unaffected.
	StalwartAdminURL      string
	StalwartAdminUser     string
	StalwartAdminPassword string
	StalwartAdminToken    string // pre-encoded "Basic …" (alternative to user+pass)
	BoardMailboxesPath           string
	StalwartMailboxPasswordsPath string
	ReconcilerDryRun             bool

	// Database pool
	DBMaxConns          int32
	DBMinConns          int32
	DBMaxConnLifetime   time.Duration
	DBMaxConnIdleTime   time.Duration
	DBStatementTimeout  string

	// Feature flags
	Features Features
}

type Features struct {
	Bookings       bool
	Projects       bool
	Calendar       bool
	Commerce       bool
	Communications bool
	Accounting     bool
	DemoAuth       bool
}

func Load() Config {
	return Config{
		Port:        envInt("PORT", 8080),
		DatabaseURL: envStr("DATABASE_URL", "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"),
		RedisURL:    envStr("REDIS_URL", "redis://localhost:6379/0"),
		ClubSlug: envStr("CLUB_SLUG", "brygge"),
		ClubName: envStr("CLUB_NAME", ""),
		Domain:   envStr("DOMAIN", "localhost"),

		VippsTestMode:        envBool("VIPPS_TEST_MODE", true),
		VippsMSN:             envStr("VIPPS_MSN", ""),
		VippsSubscriptionKey: envStr("VIPPS_SUBSCRIPTION_KEY", ""),
		VippsWebhookSecret:   envStr("VIPPS_WEBHOOK_SECRET", ""),

		DBMaxConns:         clampInt32(envInt("DB_MAX_CONNS", 20)),
		DBMinConns:         clampInt32(envInt("DB_MIN_CONNS", 2)),
		DBMaxConnLifetime:  envDuration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		DBMaxConnIdleTime:  envDuration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
		DBStatementTimeout: envStr("DB_STATEMENT_TIMEOUT", "30000"),

		FrontendURL: envStr("FRONTEND_URL", "http://localhost:5173"),

		S3Endpoint:  envStr("S3_ENDPOINT", ""),
		S3Bucket:    envStr("S3_BUCKET", "brygge"),
		S3AccessKey: envStr("S3_ACCESS_KEY", ""),
		S3SecretKey: envStr("S3_SECRET_KEY", ""),

		DendriteInternalURL:  envStr("DENDRITE_INTERNAL_URL", "http://dendrite:8008"),
		DendriteServiceToken: envStr("DENDRITE_SERVICE_TOKEN", ""),

		VAPIDPublicKey:  envStr("VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey: envStr("VAPID_PRIVATE_KEY", ""),

		TOTPEncryptionKey: envStr("TOTP_ENCRYPTION_KEY", ""),

		SMTPHost:     envStr("SMTP_HOST", ""),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPUsername: envStr("SMTP_USERNAME", ""),
		SMTPPassword: envStr("SMTP_PASSWORD", ""),
		EmailFrom:    envStr("EMAIL_FROM", ""),
		EmailReplyTo: envStr("EMAIL_REPLY_TO", ""),
		AnthropicAPIKey:   envStr("ANTHROPIC_API_KEY", ""),

		StalwartAdminURL:      envStr("STALWART_ADMIN_URL", ""),
		StalwartAdminUser:     envStr("STALWART_ADMIN_USER", "admin"),
		StalwartAdminPassword: envStr("STALWART_ADMIN_PASSWORD", ""),
		StalwartAdminToken:    envStr("STALWART_ADMIN_TOKEN", ""),
		BoardMailboxesPath:           envStr("BRYGGE_MAILBOXES_PATH", ""),
		StalwartMailboxPasswordsPath: envStr("STALWART_MAILBOX_PASSWORDS_PATH", ""),
		ReconcilerDryRun:             envBool("BRYGGE_RECONCILER_DRY_RUN", false),

		Features: Features{
			Bookings:       envBool("FEATURE_BOOKINGS", true),
			Projects:       envBool("FEATURE_PROJECTS", true),
			Calendar:       envBool("FEATURE_CALENDAR", true),
			Commerce:       envBool("FEATURE_COMMERCE", true),
			Communications: envBool("FEATURE_COMMUNICATIONS", true),
			Accounting:     envBool("FEATURE_ACCOUNTING", false),
			DemoAuth:       envBool("FEATURE_DEMO_AUTH", false),
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

func clampInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
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
