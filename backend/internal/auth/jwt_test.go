package auth

import (
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/config"
)

func newTestJWTService(secret string, accessExpiry, refreshExpiry time.Duration) *JWTService {
	return NewJWTService(&config.Config{
		JWTSecret:        secret,
		JWTAccessExpiry:  accessExpiry,
		JWTRefreshExpiry: refreshExpiry,
	})
}

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := newTestJWTService("test-secret-key", 15*time.Minute, 7*24*time.Hour)

	userID := "user-123"
	clubID := "club-456"
	roles := []string{"member"}

	token, err := svc.GenerateAccessToken(userID, clubID, roles)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error = %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("UserID = %q, want %q", claims.UserID, userID)
	}
	if claims.ClubID != clubID {
		t.Errorf("ClubID = %q, want %q", claims.ClubID, clubID)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "member" {
		t.Errorf("Roles = %v, want %v", claims.Roles, roles)
	}
	if claims.Issuer != "brygge" {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, "brygge")
	}
}

func TestGenerateAndValidateRefreshToken(t *testing.T) {
	svc := newTestJWTService("test-secret-key", 15*time.Minute, 7*24*time.Hour)

	userID := "user-789"

	token, err := svc.GenerateRefreshToken(userID)
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}

	claims, err := svc.ValidateRefreshToken(token)
	if err != nil {
		t.Fatalf("ValidateRefreshToken() error = %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("UserID = %q, want %q", claims.UserID, userID)
	}
}

func TestExpiredAccessToken(t *testing.T) {
	svc := newTestJWTService("test-secret-key", 1*time.Millisecond, 7*24*time.Hour)

	token, err := svc.GenerateAccessToken("user-1", "club-1", []string{"member"})
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	_, err = svc.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("ValidateAccessToken() expected error for expired token, got nil")
	}
}

func TestInvalidAccessToken(t *testing.T) {
	svc := newTestJWTService("test-secret-key", 15*time.Minute, 7*24*time.Hour)

	_, err := svc.ValidateAccessToken("not-a-valid-jwt-token")
	if err == nil {
		t.Fatal("ValidateAccessToken() expected error for garbage token, got nil")
	}
}

func TestWrongSigningKey(t *testing.T) {
	svc1 := newTestJWTService("secret-key-one", 15*time.Minute, 7*24*time.Hour)
	svc2 := newTestJWTService("secret-key-two", 15*time.Minute, 7*24*time.Hour)

	token, err := svc1.GenerateAccessToken("user-1", "club-1", []string{"member"})
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	_, err = svc2.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("ValidateAccessToken() expected error for wrong signing key, got nil")
	}
}

func TestAccessTokenContainsAllRoles(t *testing.T) {
	svc := newTestJWTService("test-secret-key", 15*time.Minute, 7*24*time.Hour)

	roles := []string{"member", "styre", "admin", "treasurer"}

	token, err := svc.GenerateAccessToken("user-1", "club-1", roles)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error = %v", err)
	}

	if len(claims.Roles) != len(roles) {
		t.Fatalf("got %d roles, want %d", len(claims.Roles), len(roles))
	}

	roleSet := make(map[string]bool, len(claims.Roles))
	for _, r := range claims.Roles {
		roleSet[r] = true
	}
	for _, r := range roles {
		if !roleSet[r] {
			t.Errorf("role %q not found in claims", r)
		}
	}
}
