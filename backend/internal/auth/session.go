package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sessionExpiry = 30 * 24 * time.Hour

var ErrSessionNotFound = errors.New("session not found or expired")

// SessionService manages server-side sessions stored in PostgreSQL.
type SessionService struct {
	db *pgxpool.Pool
}

func NewSessionService(db *pgxpool.Pool) *SessionService {
	return &SessionService{db: db}
}

// CreateSession generates a new session and returns its ID.
func (s *SessionService) CreateSession(ctx context.Context, userID, clubID, ip, userAgent string) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}

	expiresAt := time.Now().Add(sessionExpiry)
	_, err = s.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, club_id, expires_at, ip_address, user_agent)
		 VALUES ($1, $2, $3, $4, $5::inet, $6)`,
		id, userID, clubID, expiresAt, ip, userAgent,
	)
	if err != nil {
		return "", fmt.Errorf("inserting session: %w", err)
	}

	return id, nil
}

// ValidateSession checks a session ID and returns the associated claims.
// Returns ErrSessionNotFound if the session is invalid or expired.
func (s *SessionService) ValidateSession(ctx context.Context, sessionID string) (*Claims, *time.Time, error) {
	var userID, clubID string
	var totpVerifiedAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT user_id, club_id, totp_verified_at FROM sessions
		 WHERE id = $1 AND expires_at > NOW()`,
		sessionID,
	).Scan(&userID, &clubID, &totpVerifiedAt)
	if err == pgx.ErrNoRows {
		return nil, nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("querying session: %w", err)
	}

	// Fetch user roles
	rows, err := s.db.Query(ctx,
		`SELECT role FROM user_roles WHERE user_id = $1 AND club_id = $2`,
		userID, clubID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("querying user roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, nil, fmt.Errorf("scanning role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating roles: %w", err)
	}

	claims := &Claims{
		UserID: userID,
		ClubID: clubID,
		Roles:  roles,
	}
	return claims, totpVerifiedAt, nil
}

// DeleteSession removes a single session (logout).
func (s *SessionService) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}

// DeleteUserSessions removes all sessions for a user (logout everywhere).
func (s *SessionService) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// StampTOTP marks the session as TOTP-verified.
func (s *SessionService) StampTOTP(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE sessions SET totp_verified_at = NOW() WHERE id = $1`,
		sessionID,
	)
	return err
}

// PurgeExpired removes all expired sessions.
func (s *SessionService) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}
