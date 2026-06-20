package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear any ambient config env (CI sets DATABASE_URL/REDIS_URL job-wide
	// for the integration tests) so the defaults are exercised hermetically.
	for _, k := range []string{
		"PORT", "DATABASE_URL", "REDIS_URL", "CLUB_SLUG",
		"VIPPS_TEST_MODE", "S3_BUCKET_DOCS", "DENDRITE_INTERNAL_URL",
	} {
		t.Setenv(k, "")
	}

	cfg := Load()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 8080},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"},
		{"RedisURL", cfg.RedisURL, "redis://localhost:6379/0"},
		{"ClubSlug", cfg.ClubSlug, "brygge"},
		{"VippsTestMode", cfg.VippsTestMode, true},
		{"S3BucketDocs", cfg.S3BucketDocs, "brygge"},
		{"DendriteInternalURL", cfg.DendriteInternalURL, "http://dendrite:8008"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://custom:custom@db:5432/custom")
	t.Setenv("VIPPS_TEST_MODE", "false")
	t.Setenv("S3_BUCKET_DOCS", "custom-bucket")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	cfg := Load()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 9090},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://custom:custom@db:5432/custom"},
		{"ClubSlug", cfg.ClubSlug, "brygge"},
		{"VippsTestMode", cfg.VippsTestMode, false},
		{"S3BucketDocs", cfg.S3BucketDocs, "custom-bucket"},
		{"AnthropicAPIKey", cfg.AnthropicAPIKey, "sk-ant-test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestCleanBaseURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://klokkarvikbaatlag.no", "https://klokkarvikbaatlag.no"},
		{`https://klokkarvikbaatlag.no"`, "https://klokkarvikbaatlag.no"},
		{`"https://klokkarvikbaatlag.no"`, "https://klokkarvikbaatlag.no"},
		{"https://klokkarvikbaatlag.no/", "https://klokkarvikbaatlag.no"},
		{`  https://x.no"  `, "https://x.no"},
		{"http://localhost:5173", "http://localhost:5173"},
	}
	for _, tt := range tests {
		if got := cleanBaseURL(tt.in); got != tt.want {
			t.Errorf("cleanBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLoadFrontendURLSanitised(t *testing.T) {
	// Env layering can leave a literal trailing quote on the value;
	// it must not leak into magic-link URLs or the post-login redirect.
	t.Setenv("FRONTEND_URL", `https://klokkarvikbaatlag.no"`)
	if got := Load().FrontendURL; got != "https://klokkarvikbaatlag.no" {
		t.Errorf("FrontendURL = %q, want %q", got, "https://klokkarvikbaatlag.no")
	}
}

func TestLoadDBPoolDuration(t *testing.T) {
	t.Setenv("DB_MAX_CONN_LIFETIME", "20m")

	cfg := Load()

	if cfg.DBMaxConnLifetime != 20*time.Minute {
		t.Errorf("DBMaxConnLifetime = %v, want %v", cfg.DBMaxConnLifetime, 20*time.Minute)
	}
}
