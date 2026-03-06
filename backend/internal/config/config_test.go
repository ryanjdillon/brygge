package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 8080},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://brygge:brygge@localhost:5432/brygge?sslmode=disable"},
		{"RedisURL", cfg.RedisURL, "redis://localhost:6379/0"},
		{"ClubSlug", cfg.ClubSlug, "default"},
		{"JWTAccessExpiry", cfg.JWTAccessExpiry, 15 * time.Minute},
		{"JWTRefreshExpiry", cfg.JWTRefreshExpiry, 7 * 24 * time.Hour},
		{"VippsTestMode", cfg.VippsTestMode, true},
		{"S3Bucket", cfg.S3Bucket, "brygge"},
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
	t.Setenv("JWT_SECRET", "super-secret")
	t.Setenv("CLUB_SLUG", "my-club")
	t.Setenv("VIPPS_CLIENT_ID", "vipps-id")
	t.Setenv("VIPPS_TEST_MODE", "false")
	t.Setenv("S3_BUCKET", "custom-bucket")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	cfg := Load()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 9090},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://custom:custom@db:5432/custom"},
		{"JWTSecret", cfg.JWTSecret, "super-secret"},
		{"ClubSlug", cfg.ClubSlug, "my-club"},
		{"VippsClientID", cfg.VippsClientID, "vipps-id"},
		{"VippsTestMode", cfg.VippsTestMode, false},
		{"S3Bucket", cfg.S3Bucket, "custom-bucket"},
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

func TestLoadDuration(t *testing.T) {
	t.Setenv("JWT_ACCESS_EXPIRY", "30m")
	t.Setenv("JWT_REFRESH_EXPIRY", "48h")

	cfg := Load()

	if cfg.JWTAccessExpiry != 30*time.Minute {
		t.Errorf("JWTAccessExpiry = %v, want %v", cfg.JWTAccessExpiry, 30*time.Minute)
	}
	if cfg.JWTRefreshExpiry != 48*time.Hour {
		t.Errorf("JWTRefreshExpiry = %v, want %v", cfg.JWTRefreshExpiry, 48*time.Hour)
	}
}
