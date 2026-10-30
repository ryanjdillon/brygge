// Package devtoken issues and verifies short-lived developer bearer tokens
// for the read-only dev-query endpoint (BRY-115).
//
// A token lets an external tool — typically an AI agent that cannot use the
// operator's SSH key — run read-only, PII-masked SQL over HTTPS. The token is
// only as powerful as the brygge_dev_ro role behind the endpoint (a real
// privilege boundary, default-deny, PII masked — see
// docs/developer/security/dev-query.md), so the design goal here is a short,
// auditable, revocable credential rather than a strong one.
//
// Only sha256(token) is stored. A 32-byte random token has no low-entropy
// guess surface, so a fast hash is the correct lookup key; a slow KDF on
// every request would just be a memory/CPU DoS lever.
package devtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Prefix marks a string as a Brygge developer token, so it is greppable
	// in logs and secret scanners and cheap to reject before any DB lookup.
	Prefix = "bry_dev_"

	// ScopeDBQuery authorises POST /api/v1/dev/query.
	ScopeDBQuery = "db_query"

	// MinterRole is the role a user must hold to mint a token, and must keep
	// holding for the token to authenticate. The token routes (POST mint,
	// DELETE revoke and GET list under /admin/dev/tokens in cmd/api/main.go)
	// gate on this constant, so there is one place to change it.
	MinterRole = "admin"

	// DefaultTTL and MaxTTL bound a token's life. An agent session lasts
	// hours, not weeks; a leaked token should die on its own quickly.
	DefaultTTL = 24 * time.Hour
	MaxTTL     = 7 * 24 * time.Hour

	// purgeGrace keeps expired/revoked tokens visible in the admin list for
	// a while before the periodic purge deletes them. The audit log keeps
	// the token id regardless.
	purgeGrace = 7 * 24 * time.Hour

	maxNameLen = 100
	// displayLen is how much of the plaintext is kept (as token_prefix) so
	// an admin can match a listed token to the one an agent is using.
	displayLen = len(Prefix) + 6
)

var (
	// ErrInvalid covers every authentication failure — unknown, malformed,
	// expired, revoked, wrong-scope, or minted by a user who is no longer an
	// active admin of the club — so callers can't distinguish them.
	ErrInvalid  = errors.New("invalid or expired developer token")
	ErrNotFound = errors.New("developer token not found")
	ErrTTL      = fmt.Errorf("token lifetime must be at most %s", MaxTTL)
	ErrName     = fmt.Errorf("token name is required (max %d characters)", maxNameLen)
)

// Token is a stored token's metadata. The plaintext is never part of it.
type Token struct {
	ID         string     `json:"id"`
	ClubID     string     `json:"club_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	CreatedBy  *string    `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

const tokenColumns = `id::text, club_id::text, name, token_prefix, scope,
	created_by::text, created_at, expires_at, last_used_at, revoked_at` // #nosec G101 -- a SELECT column list; token_prefix is a non-secret display prefix

func scanToken(row pgx.Row) (Token, error) {
	var t Token
	err := row.Scan(&t.ID, &t.ClubID, &t.Name, &t.Prefix, &t.Scope,
		&t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

// Mint creates a db_query token and returns its plaintext exactly once. A
// zero ttl means DefaultTTL; anything above MaxTTL is rejected.
func (s *Service) Mint(ctx context.Context, clubID, createdBy, name string, ttl time.Duration) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return "", Token{}, ErrName
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > MaxTTL {
		return "", Token{}, ErrTTL
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", Token{}, fmt.Errorf("generate token: %w", err)
	}
	plaintext := Prefix + base64.RawURLEncoding.EncodeToString(buf)

	var creator *string
	if createdBy != "" {
		creator = &createdBy
	}
	tok, err := scanToken(s.db.QueryRow(ctx,
		`INSERT INTO developer_tokens (club_id, name, token_prefix, token_hash, scope, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now() + make_interval(secs => $7))
		 RETURNING `+tokenColumns,
		clubID, name, plaintext[:displayLen], hashToken(plaintext), ScopeDBQuery, creator, ttl.Seconds()))
	if err != nil {
		return "", Token{}, fmt.Errorf("insert token: %w", err)
	}
	return plaintext, tok, nil
}

// Authenticate resolves a live token for the given scope and stamps its
// last use. A token is live only while its minter is still an active member
// of the token's club holding MinterRole. Deactivating a member, removing
// their admin role and disabling their TOTP revoke their tokens outright;
// this check is the backstop for paths that do not (direct SQL, a future
// role writer), ending access on the next request.
// Any refusal returns ErrInvalid; a database error is returned wrapped and
// never authenticates.
func (s *Service) Authenticate(ctx context.Context, plaintext, scope string) (Token, error) {
	if !strings.HasPrefix(plaintext, Prefix) || len(plaintext) <= displayLen {
		return Token{}, ErrInvalid
	}
	tok, err := scanToken(s.db.QueryRow(ctx,
		`UPDATE developer_tokens SET last_used_at = now()
		 WHERE token_hash = $1 AND scope = $2
		   AND revoked_at IS NULL AND expires_at > now()
		   AND EXISTS (
		     SELECT 1 FROM users u
		     WHERE u.id = developer_tokens.created_by
		       AND u.club_id = developer_tokens.club_id
		       AND u.deactivated_at IS NULL
		       AND EXISTS (
		         SELECT 1 FROM user_roles r
		         WHERE r.user_id = u.id
		           AND r.club_id = developer_tokens.club_id
		           AND r.role = $3::user_role))
		 RETURNING `+tokenColumns,
		hashToken(plaintext), scope, MinterRole))
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, ErrInvalid
	}
	if err != nil {
		return Token{}, fmt.Errorf("authenticate token: %w", err)
	}
	return tok, nil
}

// List returns the club's tokens, newest first, including recently expired
// or revoked ones (until the purge removes them).
func (s *Service) List(ctx context.Context, clubID string) ([]Token, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+tokenColumns+` FROM developer_tokens
		 WHERE club_id = $1 ORDER BY created_at DESC LIMIT 200`, clubID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	out := make([]Token, 0)
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke disables a live token immediately. Revoking an unknown, foreign or
// already-revoked token returns ErrNotFound.
func (s *Service) Revoke(ctx context.Context, clubID, id string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE developer_tokens SET revoked_at = now()
		 WHERE id::text = $1 AND club_id = $2 AND revoked_at IS NULL`, id, clubID)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeExpired deletes tokens that expired or were revoked more than the
// grace period ago.
func (s *Service) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM developer_tokens
		 WHERE expires_at < now() - make_interval(secs => $1)
		    OR revoked_at < now() - make_interval(secs => $1)`, purgeGrace.Seconds())
	if err != nil {
		return 0, fmt.Errorf("purge tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
