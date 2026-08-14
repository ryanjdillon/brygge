package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoUser is returned when a WebAuthn handle resolves to no member (e.g. a
// discoverable-login assertion for a credential whose user was deleted).
var ErrNoUser = errors.New("passkey: user not found")

// Store persists WebAuthn credentials and user handles.
type Store struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// CredentialInfo is the display/management view of a stored credential (no key
// material), for the Security page listing.
type CredentialInfo struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Transports     []string   `json:"transports"`
	BackupEligible bool       `json:"backup_eligible"`
	BackupState    bool       `json:"backup_state"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
}

// EnsureHandle returns the member's WebAuthn handle, minting one on first use.
// Race-safe: concurrent callers converge on a single stored handle.
func (s *Store) EnsureHandle(ctx context.Context, userID string) ([]byte, error) {
	var h []byte
	if err := s.db.QueryRow(ctx,
		`SELECT webauthn_handle FROM users WHERE id = $1`, userID,
	).Scan(&h); err != nil {
		return nil, err
	}
	if len(h) > 0 {
		return h, nil
	}
	nh, err := newHandle()
	if err != nil {
		return nil, err
	}
	// COALESCE keeps a handle another transaction may have set in the meantime.
	if err := s.db.QueryRow(ctx,
		`UPDATE users SET webauthn_handle = COALESCE(webauthn_handle, $2)
		 WHERE id = $1 RETURNING webauthn_handle`,
		userID, nh,
	).Scan(&h); err != nil {
		return nil, err
	}
	return h, nil
}

// LoadUser builds a webauthn.User for the given member, minting the handle if
// absent and loading all their credentials.
func (s *Store) LoadUser(ctx context.Context, userID string) (*User, error) {
	handle, err := s.EnsureHandle(ctx, userID)
	if err != nil {
		return nil, err
	}
	var email, displayName string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(email, ''),
		        COALESCE(NULLIF(full_name, ''),
		                 NULLIF(TRIM(COALESCE(first_name, '') || ' ' || COALESCE(last_name, '')), ''),
		                 COALESCE(email, ''))
		   FROM users WHERE id = $1`,
		userID,
	).Scan(&email, &displayName); err != nil {
		return nil, err
	}
	creds, err := s.loadCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &User{id: userID, handle: handle, email: email, displayName: displayName, creds: creds}, nil
}

// UserByHandle resolves a discoverable-login assertion's user handle back to a
// member (Phase 2 login).
func (s *Store) UserByHandle(ctx context.Context, handle []byte) (*User, error) {
	var userID string
	err := s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE webauthn_handle = $1 AND deactivated_at IS NULL`, handle,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return s.LoadUser(ctx, userID)
}

func (s *Store) loadCredentials(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	rows, err := s.db.Query(ctx,
		`SELECT credential FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("passkey: decode credential: %w", err)
		}
		creds = append(creds, c)
	}
	return creds, rows.Err()
}

// ListCredentials returns the management view of a member's credentials.
func (s *Store) ListCredentials(ctx context.Context, userID string) ([]CredentialInfo, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, transports, backup_eligible, backup_state, created_at, last_used_at
		   FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CredentialInfo{}
	for rows.Next() {
		var ci CredentialInfo
		if err := rows.Scan(&ci.ID, &ci.Name, &ci.Transports, &ci.BackupEligible, &ci.BackupState, &ci.CreatedAt, &ci.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

// CountForUser reports how many credentials a member has (for "is a passkey
// enrolled?" checks).
func (s *Store) CountForUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM webauthn_credentials WHERE user_id = $1`, userID,
	).Scan(&n)
	return n, err
}

// SaveCredential persists a newly registered credential.
func (s *Store) SaveCredential(ctx context.Context, userID, clubID string, cred *webauthn.Credential, name string) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO webauthn_credentials
		   (user_id, club_id, credential_id, credential, sign_count, aaguid,
		    transports, backup_eligible, backup_state, name)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		userID, clubID, cred.ID, string(raw), int64(cred.Authenticator.SignCount),
		cred.Authenticator.AAGUID, transportStrings(cred), cred.Flags.BackupEligible,
		cred.Flags.BackupState, name,
	)
	return err
}

// TouchCredential updates the mutable state after a successful assertion: the
// signature counter, backup state, and last-used time, keyed by credential_id.
func (s *Store) TouchCredential(ctx context.Context, cred *webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE webauthn_credentials
		    SET credential = $2, sign_count = $3, backup_state = $4, last_used_at = now()
		  WHERE credential_id = $1`,
		cred.ID, string(raw), int64(cred.Authenticator.SignCount), cred.Flags.BackupState,
	)
	return err
}

// RenameCredential sets the user-facing label. Scoped to the owner.
func (s *Store) RenameCredential(ctx context.Context, userID, id, name string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE webauthn_credentials SET name = $3 WHERE id = $1 AND user_id = $2`, id, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteCredential removes one credential owned by the member.
func (s *Store) DeleteCredential(ctx context.Context, userID, id string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteAllForUser wipes every credential for a member (admin reset backstop),
// returning how many were removed.
func (s *Store) DeleteAllForUser(ctx context.Context, userID string) (int, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func transportStrings(cred *webauthn.Credential) []string {
	out := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		out = append(out, string(t))
	}
	return out
}
