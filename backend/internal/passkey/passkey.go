// Package passkey wires WebAuthn (passkeys) into the app: the relying-party
// configuration derived from app config, a credential store backed by Postgres,
// and a Redis-backed challenge store for in-flight ceremonies. Handlers for the
// registration/step-up/login ceremonies live in internal/handlers.
package passkey

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/brygge-klubb/brygge/internal/config"
)

// handleBytes is the length of a WebAuthn user handle. Opaque, non-PII, stable
// per account; 32 random bytes is well within the 64-byte spec ceiling.
const handleBytes = 32

// New builds the relying party from app config.
func New(cfg *config.Config) (*webauthn.WebAuthn, error) {
	wc, err := rpConfig(cfg)
	if err != nil {
		return nil, err
	}
	return webauthn.New(wc)
}

// rpConfig derives the WebAuthn relying-party config. Because a deployment is a
// single club on a single host, the RP ID is unambiguously the host of
// FrontendURL (scheme and port stripped) — klokkarvikbaatlag.no in prod,
// localhost in dev — and the sole permitted origin is FrontendURL itself.
func rpConfig(cfg *config.Config) (*webauthn.Config, error) {
	origin := strings.TrimRight(cfg.FrontendURL, "/")
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("passkey: invalid FRONTEND_URL %q: %w", cfg.FrontendURL, err)
	}

	display := cfg.ClubName
	if display == "" {
		display = "Brygge"
	}

	return &webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: display,
		RPOrigins:     []string{origin},
		// Defaults for every ceremony; individual ceremonies tighten these
		// (step-up requires UV, login is discoverable).
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	}, nil
}

// newHandle returns a fresh random WebAuthn user handle.
func newHandle() ([]byte, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
