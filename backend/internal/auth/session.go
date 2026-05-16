package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// sessionIdleWindow is how long a session stays valid without
	// activity. Each validated request slides it forward (throttled by
	// sessionExtendThreshold), but never past sessionAbsoluteCap.
	sessionIdleWindow = 12 * time.Hour
	// sessionAbsoluteCap is the hard ceiling measured from login: even
	// a continuously-active session must re-authenticate after this,
	// so a stolen session cookie cannot be kept alive indefinitely.
	sessionAbsoluteCap = 7 * 24 * time.Hour
	// sessionExtendThreshold throttles the sliding-window write so an
	// active session updates expires_at at most ~once per minute
	// instead of on every request.
	sessionExtendThreshold = time.Minute
)

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

	// Strip port from RemoteAddr (e.g. "172.18.0.1:49068" → "172.18.0.1")
	host, _, _ := net.SplitHostPort(ip)
	if host == "" {
		host = ip
	}

	expiresAt := time.Now().Add(sessionIdleWindow)
	_, err = s.db.Exec(ctx,
		`INSERT INTO sessions (id, user_id, club_id, expires_at, ip_address, user_agent)
		 VALUES ($1, $2, $3, $4, $5::inet, $6)`,
		id, userID, clubID, expiresAt, host, userAgent,
	)
	if err != nil {
		return "", fmt.Errorf("inserting session: %w", err)
	}

	return id, nil
}

// SessionInfo carries the per-request session state that's separate
// from the Claims principal: when TOTP was last verified for this
// session (nil = never), and whether the user has TOTP enrolled at all.
// The two answer different questions: enrolled drives "should we let
// them through the admin gate at all"; verifiedAt drives "is the gate
// open right now".
type SessionInfo struct {
	TOTPVerifiedAt *time.Time
	TOTPEnabled    bool
}

// ValidateSession checks a session ID and returns the associated claims.
// Returns ErrSessionNotFound if the session is invalid or expired.
func (s *SessionService) ValidateSession(ctx context.Context, sessionID string) (*Claims, *SessionInfo, error) {
	var userID, clubID string
	var info SessionInfo
	var createdAt, expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`SELECT s.user_id, s.club_id, s.totp_verified_at, COALESCE(u.totp_enabled, false),
		        s.created_at, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1`,
		sessionID,
	).Scan(&userID, &clubID, &info.TOTPVerifiedAt, &info.TOTPEnabled, &createdAt, &expiresAt)
	if err == pgx.ErrNoRows {
		return nil, nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("querying session: %w", err)
	}

	// Two independent bounds: the idle window (expires_at, slides
	// forward on activity) and the absolute cap (created_at + cap,
	// fixed at login). Either lapsing ends the session.
	now := time.Now()
	absoluteDeadline := createdAt.Add(sessionAbsoluteCap)
	if !now.Before(expiresAt) || !now.Before(absoluteDeadline) {
		return nil, nil, ErrSessionNotFound
	}

	// Slide the idle window forward, clamped to the absolute cap.
	// Best-effort + throttled: a failed or skipped write doesn't
	// invalidate the still-valid session for this request.
	newExpiry := now.Add(sessionIdleWindow)
	if newExpiry.After(absoluteDeadline) {
		newExpiry = absoluteDeadline
	}
	if newExpiry.Sub(expiresAt) > sessionExtendThreshold {
		_, _ = s.db.Exec(ctx,
			`UPDATE sessions SET expires_at = $2 WHERE id = $1`,
			sessionID, newExpiry,
		)
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

	roles := []string{}
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
	return claims, &info, nil
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
	tag, err := s.db.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < NOW() OR created_at < $1`,
		time.Now().Add(-sessionAbsoluteCap),
	)
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
