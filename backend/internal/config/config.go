package config

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port        int
	DatabaseURL string
	// DevQueryDatabaseURL is a SEPARATE DSN that connects as the
	// restricted, read-only `brygge_dev_ro` LOGIN role, used only by the
	// /admin/dev/query endpoint. It must be a different connection from
	// DatabaseURL (which connects as `brygge`) — that separation is the
	// privilege boundary: a session that is already `brygge_dev_ro` has
	// no higher role to `RESET ROLE` back to. Empty disables the endpoint.
	DevQueryDatabaseURL string
	// DevQueryTokensEnabled mounts POST /api/v1/dev/query for developer
	// bearer tokens (BRY-115) and allows admins to mint them. Off by default
	// so the credential-only route only exists on clubs that opted in.
	DevQueryTokensEnabled bool
	RedisURL              string
	ClubSlug              string
	ClubName              string // Human-readable club name used in email subjects etc.
	Domain                string // Public domain (DOMAIN env) — used in email footers and canonical URLs

	// Vipps ePayment
	VippsTestMode        bool
	VippsClientID        string
	VippsClientSecret    string
	VippsMSN             string
	VippsSubscriptionKey string
	VippsWebhookSecret   string

	// Stripe Checkout (card payments). Both are secrets; Stripe is offered
	// only when both are set.
	StripeSecretKey     string
	StripeWebhookSecret string

	// Frontend
	FrontendURL string

	// TrustedProxyCIDRs lists the reverse proxies whose X-Forwarded-For is
	// believed (comma-separated CIDRs). Empty means loopback only; parsed
	// by middleware.ParseTrustedProxies at startup.
	TrustedProxyCIDRs string

	// Object storage
	S3Endpoint    string
	S3BucketDocs  string
	S3BucketLegal string
	S3AccessKey   string
	S3SecretKey   string

	// Dendrite (Matrix)
	DendriteInternalURL  string
	DendriteServiceToken string

	// Web Push (VAPID)
	VAPIDPublicKey  string
	VAPIDPrivateKey string

	// TOTP
	TOTPEncryptionKey string // 32-byte hex-encoded key for encrypting TOTP secrets
	// Per-action freshness window for RequireFreshTOTP gates. Default
	// 10m. Tunable via AUTH_FRESH_TOTP_WINDOW (e.g. 5m, 15m). Surfaced
	// to the SPA via /session/me so the in-context countdown stays in
	// sync.
	FreshTOTPWindow time.Duration

	// Throttle between bulk-reminder sends (per recipient). The bulk
	// handler enqueues jobs; a single in-process worker drains the
	// queue with this sleep between iterations. Tunable via
	// BULK_SEND_THROTTLE. Set to 0 to disable (mostly for tests).
	BulkSendThrottle time.Duration

	// Optional integrations
	// SMTP (self-hosted mail).
	SMTPHost        string
	SMTPPort        int
	SMTPUsername    string
	SMTPPassword    string
	EmailFrom       string
	EmailReplyTo    string
	AnthropicAPIKey string

	// Stalwart admin (DIL-275/276). Empty values disable the shared-inbox
	// reconciler; the rest of the app keeps working unaffected.
	StalwartAdminURL             string
	StalwartAdminUser            string
	StalwartAdminPassword        string
	StalwartAdminToken           string // pre-encoded "Basic …" (alternative to user+pass)
	BoardMailboxesPath           string
	StalwartMailboxPasswordsPath string
	ReconcilerDryRun             bool

	// Database pool
	DBMaxConns         int32
	DBMinConns         int32
	DBMaxConnLifetime  time.Duration
	DBMaxConnIdleTime  time.Duration
	DBStatementTimeout string

	// Feature flags
	Features Features
}

type Features struct {
	Bookings   bool
	Projects   bool
	Calendar   bool
	Commerce   bool
	Accounting bool
	DemoAuth   bool
}

func Load() Config {
	return Config{
		Port:                  envInt("PORT", 8080),
		DatabaseURL:           envStr("DATABASE_URL", "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"),
		DevQueryDatabaseURL:   envStr("DEV_QUERY_DATABASE_URL", ""),
		DevQueryTokensEnabled: envBool("DEV_QUERY_TOKENS_ENABLED", false),
		RedisURL:              envStr("REDIS_URL", "redis://localhost:6379/0"),
		ClubSlug:              envStr("CLUB_SLUG", "brygge"),
		ClubName:              envStr("CLUB_NAME", ""),
		Domain:                envStr("DOMAIN", "localhost"),

		VippsTestMode:        envBool("VIPPS_TEST_MODE", true),
		VippsClientID:        envStr("VIPPS_CLIENT_ID", ""),
		VippsClientSecret:    envStr("VIPPS_CLIENT_SECRET", ""),
		VippsMSN:             envStr("VIPPS_MSN", ""),
		VippsSubscriptionKey: envStr("VIPPS_SUBSCRIPTION_KEY", ""),
		VippsWebhookSecret:   envStr("VIPPS_WEBHOOK_SECRET", ""),

		StripeSecretKey:     envStr("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: envStr("STRIPE_WEBHOOK_SECRET", ""),

		DBMaxConns:         clampInt32(envInt("DB_MAX_CONNS", 20)),
		DBMinConns:         clampInt32(envInt("DB_MIN_CONNS", 2)),
		DBMaxConnLifetime:  envDuration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		DBMaxConnIdleTime:  envDuration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
		DBStatementTimeout: envStr("DB_STATEMENT_TIMEOUT", "30000"),

		FrontendURL:       cleanBaseURL(envStr("FRONTEND_URL", "http://localhost:5173")),
		TrustedProxyCIDRs: envStr("TRUSTED_PROXY_CIDRS", ""),

		S3Endpoint:    envStr("S3_ENDPOINT", ""),
		S3BucketDocs:  envStr("S3_BUCKET_DOCS", "brygge"),
		S3BucketLegal: envStr("S3_BUCKET_LEGAL", ""),
		S3AccessKey:   envStr("S3_ACCESS_KEY", ""),
		S3SecretKey:   envStr("S3_SECRET_KEY", ""),

		DendriteInternalURL:  envStr("DENDRITE_INTERNAL_URL", "http://dendrite:8008"),
		DendriteServiceToken: envStr("DENDRITE_SERVICE_TOKEN", ""),

		VAPIDPublicKey:  envStr("VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey: envStr("VAPID_PRIVATE_KEY", ""),

		TOTPEncryptionKey: envStr("TOTP_ENCRYPTION_KEY", ""),
		FreshTOTPWindow:   envDuration("AUTH_FRESH_TOTP_WINDOW", 10*time.Minute),
		BulkSendThrottle:  envDuration("BULK_SEND_THROTTLE", 1*time.Second),

		SMTPHost:        envStr("SMTP_HOST", ""),
		SMTPPort:        envInt("SMTP_PORT", 587),
		SMTPUsername:    envStr("SMTP_USERNAME", ""),
		SMTPPassword:    envStr("SMTP_PASSWORD", ""),
		EmailFrom:       envStr("EMAIL_FROM", ""),
		EmailReplyTo:    envStr("EMAIL_REPLY_TO", ""),
		AnthropicAPIKey: envStr("ANTHROPIC_API_KEY", ""),

		StalwartAdminURL:             envStr("STALWART_ADMIN_URL", ""),
		StalwartAdminUser:            envStr("STALWART_ADMIN_USER", "admin"),
		StalwartAdminPassword:        envStr("STALWART_ADMIN_PASSWORD", ""),
		StalwartAdminToken:           envStr("STALWART_ADMIN_TOKEN", ""),
		BoardMailboxesPath:           envStr("BRYGGE_MAILBOXES_PATH", ""),
		StalwartMailboxPasswordsPath: envStr("STALWART_MAILBOX_PASSWORDS_PATH", ""),
		ReconcilerDryRun:             envBool("BRYGGE_RECONCILER_DRY_RUN", false),

		Features: Features{
			Bookings:   envBool("FEATURE_BOOKINGS", true),
			Projects:   envBool("FEATURE_PROJECTS", true),
			Calendar:   envBool("FEATURE_CALENDAR", true),
			Commerce:   envBool("FEATURE_COMMERCE", true),
			Accounting: envBool("FEATURE_ACCOUNTING", false),
			DemoAuth:   envBool("FEATURE_DEMO_AUTH", false),
		},
	}
}

// VippsBaseURL returns the Vipps API base URL for server-to-server calls.
func (c *Config) VippsBaseURL() string {
	if c.VippsTestMode {
		// An explicit VIPPS_MOCK_URL (local dev compose) wins so a dummy MSN can
		// still be set — that lets the API build a Vipps client (gated on MSN)
		// while pointing every call at the local mock instead of apitest.
		if u := strings.TrimSpace(os.Getenv("VIPPS_MOCK_URL")); u != "" {
			return u
		}
		return "https://apitest.vipps.no"
	}
	return "https://api.vipps.no"
}

// VippsLive reports whether Vipps is set up for real payments: a merchant
// serial number is configured and the instance is not in test mode (the local
// mock and the Vipps test API). A test sales unit's payments are not the
// club's real payments, so nothing real is there to link to.
func (c *Config) VippsLive() bool {
	return strings.TrimSpace(c.VippsMSN) != "" && !c.VippsTestMode
}

// StripeCredentials reports whether both Stripe secrets are set (complete)
// and whether only one of them is (partial). A payment Brygge cannot hear
// back about would never be marked paid, so a key without its webhook secret
// must not enable Stripe.
func (c *Config) StripeCredentials() (complete, partial bool) {
	key, hook := c.StripeSecretKey != "", c.StripeWebhookSecret != ""
	return key && hook, key != hook
}

// VippsBrowserURL returns the Vipps base URL for browser redirects.
// In mock mode this is localhost:8090 (accessible from browser), otherwise same as VippsBaseURL.
func (c *Config) VippsBrowserURL() string {
	if c.VippsTestMode && strings.TrimSpace(os.Getenv("VIPPS_MOCK_URL")) != "" {
		return envStr("VIPPS_MOCK_BROWSER_URL", "http://localhost:8090")
	}
	return c.VippsBaseURL()
}

// cleanBaseURL strips stray surrounding quotes, whitespace, and a
// trailing slash from a configured base URL. Env layering (systemd
// EnvironmentFile, tfvars-derived values) can leave a literal quote on
// the value; FrontendURL is string-concatenated with paths in several
// places (magic-link URL, post-login redirect), so a stray quote or
// trailing slash silently breaks login.
func cleanBaseURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, "/")
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
