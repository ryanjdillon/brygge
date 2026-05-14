package mail

// Per-user Stalwart principal provisioning (DIL-321). Maps each
// Brygge user to a Stalwart `individual` principal with a stored
// service password, so the DIL-277 read path can authenticate as the
// user and the DIL-276 reconciler can target the user's JMAP id in
// shareWith grants.
//
// Stalwart 0.15 has no OAuth grant that lets an admin mint a token
// on a user's behalf — verified in DIL-276 verification — so
// per-user Basic auth using a server-generated service password is
// the practical authentication mechanism. The password lives
// encrypted-at-rest in `user_mail_credentials` (TOTP_ENCRYPTION_KEY
// reused) and never leaves the backend.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
)

type UserProvisioner struct {
	db         *pgxpool.Pool
	admin      *AdminClient
	audit      *audit.Service
	encKey     []byte // 32-byte AES key (TOTP_ENCRYPTION_KEY)
	clubDomain string // local domain Stalwart serves (e.g. klokkarvikbaatlag.no)
	log        zerolog.Logger
}

func NewUserProvisioner(db *pgxpool.Pool, admin *AdminClient, auditSvc *audit.Service, encKey []byte, clubDomain string, log zerolog.Logger) *UserProvisioner {
	return &UserProvisioner{
		db:         db,
		admin:      admin,
		audit:      auditSvc,
		encKey:     encKey,
		clubDomain: clubDomain,
		log:        log.With().Str("component", "user-provisioner").Logger(),
	}
}

// principalSlug derives a stable Stalwart principal name from a
// Brygge user UUID. We don't use the email local-part because (a)
// collisions across clubs are easy and (b) email rebinds shouldn't
// rotate the JMAP id. Format: `bu<12 hex>` — 14 chars, lowercase
// alphanumeric only (Stalwart's principal-name validator is strict
// about non-alphanumerics — verified by probe).
func principalSlug(userID string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(userID, "-", ""), "_", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "bu" + strings.ToLower(clean)
}

// EnsureUserPrincipal is idempotent. If the user already has a row
// in user_mail_credentials, returns (slug, nil). Otherwise creates a
// Stalwart principal (or reclaims an orphan with the same slug),
// stores the encrypted password, and writes an audit entry.
//
// Safe to call from a detached goroutine — every step is bounded
// and failures are logged at the call site via the returned error.
func (p *UserProvisioner) EnsureUserPrincipal(ctx context.Context, userID, email string) (slug string, err error) {
	if userID == "" || email == "" {
		return "", errors.New("EnsureUserPrincipal: userID and email required")
	}

	// Fast path: already provisioned.
	if existing, ok, err := p.lookupCredentials(ctx, userID); err != nil {
		return "", fmt.Errorf("lookup credentials: %w", err)
	} else if ok {
		return existing, nil
	}

	slug = principalSlug(userID)
	password, err := generateServicePassword()
	if err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}

	exists, err := p.principalExists(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("check stalwart principal: %w", err)
	}

	if exists {
		if err := p.setSecret(ctx, slug, password); err != nil {
			return "", fmt.Errorf("rotate stalwart secret: %w", err)
		}
	} else {
		if err := p.createPrincipal(ctx, slug, email, password); err != nil {
			return "", fmt.Errorf("create stalwart principal: %w", err)
		}
	}

	encrypted, err := auth.Encrypt(p.encKey, []byte(password))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}

	if _, err := p.db.Exec(ctx, `
		INSERT INTO user_mail_credentials (user_id, jmap_user, jmap_password_encrypted)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			jmap_user = EXCLUDED.jmap_user,
			jmap_password_encrypted = EXCLUDED.jmap_password_encrypted,
			updated_at = now()
	`, userID, slug, encrypted); err != nil {
		return "", fmt.Errorf("store credentials: %w", err)
	}

	if p.audit != nil {
		p.audit.Log(ctx, audit.Entry{
			Action:     audit.ActionUserMailProvisioned,
			Resource:   "user",
			ResourceID: userID,
			Details:    map[string]any{"jmap_user": slug},
		})
	}
	p.log.Info().Str("user_id", userID).Str("slug", slug).Msg("provisioned stalwart principal")
	return slug, nil
}

// DeleteUserPrincipal removes the Stalwart principal AND the
// credentials row. ON DELETE CASCADE on user_mail_credentials would
// remove the row when the user is deleted, but Stalwart wouldn't
// know about that — so callers must invoke this before deleting the
// users row.
func (p *UserProvisioner) DeleteUserPrincipal(ctx context.Context, userID string) error {
	slug, ok, err := p.lookupCredentials(ctx, userID)
	if err != nil {
		return fmt.Errorf("lookup credentials: %w", err)
	}
	if !ok {
		return nil // not provisioned, nothing to do
	}

	if err := p.deletePrincipal(ctx, slug); err != nil {
		return fmt.Errorf("delete stalwart principal: %w", err)
	}
	if _, err := p.db.Exec(ctx, `DELETE FROM user_mail_credentials WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete credentials row: %w", err)
	}
	if p.audit != nil {
		p.audit.Log(ctx, audit.Entry{
			Action:     audit.ActionUserMailDeprovisioned,
			Resource:   "user",
			ResourceID: userID,
			Details:    map[string]any{"jmap_user": slug},
		})
	}
	p.log.Info().Str("user_id", userID).Str("slug", slug).Msg("deprovisioned stalwart principal")
	return nil
}

// Credentials returns the slug + decrypted password for a user, or
// (_, _, false, nil) when no row exists. Used by the DIL-277 read
// path to authenticate JMAP calls as the user.
func (p *UserProvisioner) Credentials(ctx context.Context, userID string) (user, password string, ok bool, err error) {
	var encrypted []byte
	err = p.db.QueryRow(ctx, `
		SELECT jmap_user, jmap_password_encrypted
		FROM user_mail_credentials WHERE user_id = $1
	`, userID).Scan(&user, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	plain, err := auth.Decrypt(p.encKey, encrypted)
	if err != nil {
		return "", "", false, fmt.Errorf("decrypt: %w", err)
	}
	return user, string(plain), true, nil
}

func (p *UserProvisioner) lookupCredentials(ctx context.Context, userID string) (slug string, ok bool, err error) {
	err = p.db.QueryRow(ctx,
		`SELECT jmap_user FROM user_mail_credentials WHERE user_id = $1`,
		userID,
	).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return slug, true, nil
}

// principalExists hits the admin REST API by slug. We can't reuse
// AdminClient.LookupPrincipal because it expects an email and strips
// the @domain — passing a bare slug works (no @ → returned as-is)
// but the contract is too tight a fit; keep this explicit.
func (p *UserProvisioner) principalExists(ctx context.Context, slug string) (bool, error) {
	id, err := p.admin.LookupPrincipal(ctx, slug)
	if err != nil {
		return false, err
	}
	return id != "", nil
}

func (p *UserProvisioner) createPrincipal(ctx context.Context, slug, realEmail, password string) error {
	// Stalwart only accepts emails on a domain it serves. The user's
	// real email (e.g. gmail.com) lives in users.email for Brygge
	// auth — Stalwart's principal carries a synthetic local address
	// `<slug>@<club_domain>`. The user authenticates JMAP by `name`
	// (slug) + password; their real email plays no role on the
	// Stalwart side.
	//
	// Pre-hash with bcrypt before POSTing (DIL-322). Stalwart's
	// admin REST POST persists `secrets` verbatim — it does NOT
	// auto-hash on principal create, only on later PATCHes. Passing
	// a bcrypt hash explicitly avoids storing the plaintext in
	// Stalwart's RocksDB as well as our encrypted DB row.
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	synthetic := slug + "@" + p.clubDomain
	body := map[string]any{
		"type":        "individual",
		"name":        slug,
		"emails":      []string{synthetic},
		"secrets":     []string{string(hashed)},
		"description": fmt.Sprintf("Brygge user %s (managed_by=brygge-user)", realEmail),
		"roles":       []string{"user"},
		"quota":       0,
	}
	return p.admin.doJSON(ctx, "POST", "/api/principal", body, nil)
}

func (p *UserProvisioner) setSecret(ctx context.Context, slug, password string) error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	body := []map[string]any{
		{"action": "set", "field": "secrets", "value": []string{string(hashed)}},
	}
	return p.admin.doJSON(ctx, "PATCH", "/api/principal/"+slug, body, nil)
}

func (p *UserProvisioner) deletePrincipal(ctx context.Context, slug string) error {
	return p.admin.doJSON(ctx, "DELETE", "/api/principal/"+slug, nil, nil)
}

// generateServicePassword returns a 32-character URL-safe random
// string. Stalwart accepts plaintext over the admin API and stores
// it hashed; the plaintext also lives encrypted-at-rest in our DB
// so the backend can re-present it on JMAP login.
func generateServicePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 24 random bytes -> 32 base64 chars (URL-safe, no padding).
	return base64.RawURLEncoding.EncodeToString(b), nil
}

