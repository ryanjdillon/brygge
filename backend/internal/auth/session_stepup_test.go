package auth

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// StampStepUp + ValidateSession: a passkey step-up stamps the shared timestamp
// and records the method, and ValidateSession reports the member has a passkey.
func TestValidateSessionReportsPasskeyAndStepUp(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	svc := NewSessionService(pool)

	sessionID, err := svc.CreateSession(ctx, userID, clubID, "10.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// No passkey yet, no step-up.
	_, info, err := svc.ValidateSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if info.HasPasskey {
		t.Error("HasPasskey should be false before any credential is registered")
	}
	if info.TOTPVerifiedAt != nil || info.MFAMethod != "" {
		t.Errorf("unexpected step-up state: verifiedAt=%v method=%q", info.TOTPVerifiedAt, info.MFAMethod)
	}

	// Register a passkey (minimal row — the query only checks existence).
	if _, err := pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, club_id, credential_id, credential)
		 VALUES ($1, $2, $3, '{}'::jsonb)`,
		userID, clubID, []byte("cred-1"),
	); err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	// Stamp a passkey step-up.
	if err := svc.StampStepUp(ctx, sessionID, "passkey"); err != nil {
		t.Fatalf("StampStepUp: %v", err)
	}

	_, info, err = svc.ValidateSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ValidateSession #2: %v", err)
	}
	if !info.HasPasskey {
		t.Error("HasPasskey should be true after registering a credential")
	}
	if info.TOTPVerifiedAt == nil {
		t.Error("TOTPVerifiedAt should be set after a step-up")
	}
	if info.MFAMethod != "passkey" {
		t.Errorf("MFAMethod = %q, want passkey", info.MFAMethod)
	}
}
