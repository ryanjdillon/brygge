package passkey

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func fakeCredential(id string) *webauthn.Credential {
	return &webauthn.Credential{
		ID:                []byte(id),
		PublicKey:         []byte("cose-public-key-bytes"),
		AttestationType:   "none",
		AttestationFormat: "none",
		Transport:         []protocol.AuthenticatorTransport{protocol.Internal, protocol.Hybrid},
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    false,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    bytes.Repeat([]byte{0x01}, 16),
			SignCount: 0,
		},
	}
}

func TestStoreCredentialRoundTrip(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, email := testutil.SeedUser(t, pool, clubID, []string{"board"})
	store := NewStore(pool)

	// LoadUser mints a handle and reports zero credentials initially.
	u, err := store.LoadUser(ctx, userID)
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if len(u.WebAuthnID()) != handleBytes {
		t.Errorf("handle length = %d, want %d", len(u.WebAuthnID()), handleBytes)
	}
	if u.WebAuthnName() != email {
		t.Errorf("WebAuthnName = %q, want %q", u.WebAuthnName(), email)
	}
	if u.WebAuthnDisplayName() != "Test User" {
		t.Errorf("WebAuthnDisplayName = %q, want %q", u.WebAuthnDisplayName(), "Test User")
	}
	if len(u.WebAuthnCredentials()) != 0 {
		t.Fatalf("expected no credentials yet, got %d", len(u.WebAuthnCredentials()))
	}
	handle := u.WebAuthnID()

	// The handle is stable across loads.
	u2, err := store.LoadUser(ctx, userID)
	if err != nil {
		t.Fatalf("LoadUser #2: %v", err)
	}
	if !bytes.Equal(u2.WebAuthnID(), handle) {
		t.Error("handle changed between loads")
	}

	// Save a credential.
	cred := fakeCredential("cred-abc")
	if err := store.SaveCredential(ctx, userID, clubID, cred, "iPhone"); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	// It comes back through the webauthn.User adapter, decoded intact.
	u3, err := store.LoadUser(ctx, userID)
	if err != nil {
		t.Fatalf("LoadUser #3: %v", err)
	}
	got := u3.WebAuthnCredentials()
	if len(got) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(got))
	}
	if !bytes.Equal(got[0].ID, cred.ID) || !bytes.Equal(got[0].PublicKey, cred.PublicKey) {
		t.Error("credential id/public key did not round-trip")
	}

	// Management view.
	list, err := store.ListCredentials(ctx, userID)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(list) != 1 || list[0].Name != "iPhone" || !list[0].BackupEligible || list[0].BackupState {
		t.Fatalf("unexpected list view: %+v", list)
	}
	if len(list[0].Transports) != 2 {
		t.Errorf("transports = %v, want 2", list[0].Transports)
	}

	if n, err := store.CountForUser(ctx, userID); err != nil || n != 1 {
		t.Fatalf("CountForUser = %d, %v; want 1", n, err)
	}

	// UserByHandle resolves the discoverable-login path back to the member.
	byHandle, err := store.UserByHandle(ctx, handle)
	if err != nil {
		t.Fatalf("UserByHandle: %v", err)
	}
	if byHandle.UserID() != userID {
		t.Errorf("UserByHandle resolved to %q, want %q", byHandle.UserID(), userID)
	}

	// TouchCredential persists the bumped counter + backup state.
	cred.Authenticator.SignCount = 5
	cred.Flags.BackupState = true
	if err := store.TouchCredential(ctx, cred); err != nil {
		t.Fatalf("TouchCredential: %v", err)
	}
	u4, err := store.LoadUser(ctx, userID)
	if err != nil {
		t.Fatalf("LoadUser #4: %v", err)
	}
	if sc := u4.WebAuthnCredentials()[0].Authenticator.SignCount; sc != 5 {
		t.Errorf("sign count = %d, want 5", sc)
	}

	// Rename, then delete.
	credRowID := list[0].ID
	if err := store.RenameCredential(ctx, userID, credRowID, "Yubikey"); err != nil {
		t.Fatalf("RenameCredential: %v", err)
	}
	relist, _ := store.ListCredentials(ctx, userID)
	if relist[0].Name != "Yubikey" {
		t.Errorf("name after rename = %q, want Yubikey", relist[0].Name)
	}
	if err := store.DeleteCredential(ctx, userID, credRowID); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if n, _ := store.CountForUser(ctx, userID); n != 0 {
		t.Errorf("count after delete = %d, want 0", n)
	}

	// DeleteAllForUser reports how many it removed.
	if err := store.SaveCredential(ctx, userID, clubID, fakeCredential("cred-2"), "Backup"); err != nil {
		t.Fatalf("SaveCredential #2: %v", err)
	}
	removed, err := store.DeleteAllForUser(ctx, userID)
	if err != nil || removed != 1 {
		t.Fatalf("DeleteAllForUser = %d, %v; want 1", removed, err)
	}
}
