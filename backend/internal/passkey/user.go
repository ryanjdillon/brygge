package passkey

import "github.com/go-webauthn/webauthn/webauthn"

// User adapts a brygge member to the webauthn.User interface. The WebAuthn ID
// is the opaque handle (never email/name), so the authenticator stores nothing
// that correlates to a person.
type User struct {
	id          string
	clubID      string
	handle      []byte
	email       string
	displayName string
	creds       []webauthn.Credential
}

// webauthn.User interface.
func (u *User) WebAuthnID() []byte                         { return u.handle }
func (u *User) WebAuthnName() string                       { return u.email }
func (u *User) WebAuthnDisplayName() string                { return u.displayName }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// UserID is the brygge user UUID (our own identifier, not the WebAuthn handle).
func (u *User) UserID() string { return u.id }

// ClubID is the club the member belongs to — needed to create a session on
// passkey login.
func (u *User) ClubID() string { return u.clubID }
