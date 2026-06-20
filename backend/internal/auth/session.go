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
	// sessionIdleWindow is the default idle timeout. Per-club overrides
	// are stored in club_settings and loaded at session validation time.
	sessionIdleWindow = SessionIdleWindowDefault
	// sessionAbsoluteCap is the default hard ceiling from created_at.
	sessionAbsoluteCap = SessionAbsoluteCapDefault
	// sessionExtendThreshold throttles the sliding-window write so an
	// active session updates expires_at at most ~once per minute.
	sessionExtendThreshold = time.Minute

	// Exported defaults (used by handlers to report current effective value).
	SessionIdleWindowDefault    = 12 * time.Hour
	SessionAbsoluteCapDefault   = 7 * 24 * time.Hour
	AdminTOTPWindowDefault      = 12 * time.Hour

	// Clamp bounds for per-club overrides (server-enforced regardless of DB value).
	MinSessionIdleWindow  = 30 * time.Minute
	MaxSessionIdleWindow  = 30 * 24 * time.Hour
	MaxSessionAbsoluteCap = 90 * 24 * time.Hour // also used by PurgeExpired
	MinAdminTOTPWindow    = 5 * time.Minute
	MaxAdminTOTPWindow    = 24 * time.Hour
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

	idleWindow := s.clubIdleWindow(ctx, clubID)
	expiresAt := time.Now().Add(idleWindow)
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
// session (nil = never), whether the user has TOTP enrolled at all,
// and per-club timeout overrides loaded from club_settings.
type SessionInfo struct {
	TOTPVerifiedAt  *time.Time
	TOTPEnabled     bool
	// Per-club timeout overrides. Zero means "use the code default."
	// AdminTOTPWindow is applied on every request (TOTP gate re-evaluated
	// each time). IdleWindow/AbsoluteCap affect the next expires_at slide
	// and are used for new session initial expiry.
	AdminTOTPWindow time.Duration
	IdleWindow      time.Duration
	AbsoluteCap     time.Duration
}

// ValidateSession checks a session ID and returns the associated claims.
// Returns ErrSessionNotFound if the session is invalid or expired.
// Per-club timeout settings are loaded in the same query and stored in
// SessionInfo for downstream middleware (e.g. RequireAdminTOTP).
func (s *SessionService) ValidateSession(ctx context.Context, sessionID string) (*Claims, *SessionInfo, error) {
	var userID, clubID string
	var info SessionInfo
	var createdAt, expiresAt time.Time
	var idleMin, capMin, adminTOTPMin *int
	err := s.db.QueryRow(ctx,
		`SELECT s.user_id, s.club_id, s.totp_verified_at, COALESCE(u.totp_enabled, false),
		        s.created_at, s.expires_at,
		        MAX(CASE WHEN cs.key = 'session_idle_minutes'          THEN (cs.value::text)::int END),
		        MAX(CASE WHEN cs.key = 'session_absolute_cap_minutes'  THEN (cs.value::text)::int END),
		        MAX(CASE WHEN cs.key = 'admin_totp_minutes'            THEN (cs.value::text)::int END)
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 LEFT JOIN club_settings cs ON cs.club_id = s.club_id
		     AND cs.key IN ('session_idle_minutes', 'session_absolute_cap_minutes', 'admin_totp_minutes')
		 WHERE s.id = $1
		 GROUP BY s.user_id, s.club_id, s.totp_verified_at, u.totp_enabled, s.created_at, s.expires_at`,
		sessionID,
	).Scan(&userID, &clubID, &info.TOTPVerifiedAt, &info.TOTPEnabled,
		&createdAt, &expiresAt, &idleMin, &capMin, &adminTOTPMin)
	if err == pgx.ErrNoRows {
		return nil, nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("querying session: %w", err)
	}

	// Resolve per-club overrides with server-side clamps.
	idleWindow := clampSessionDuration(minutesToDuration(idleMin, sessionIdleWindow),
		MinSessionIdleWindow, MaxSessionIdleWindow)
	absoluteCap := clampSessionDuration(minutesToDuration(capMin, sessionAbsoluteCap),
		idleWindow, MaxSessionAbsoluteCap)
	adminTOTP := clampSessionDuration(minutesToDuration(adminTOTPMin, AdminTOTPWindowDefault),
		MinAdminTOTPWindow, MaxAdminTOTPWindow)
	info.IdleWindow = idleWindow
	info.AbsoluteCap = absoluteCap
	info.AdminTOTPWindow = adminTOTP

	// Two independent bounds: the idle window (expires_at, slides
	// forward on activity) and the absolute cap (created_at + cap,
	// fixed at login). Either lapsing ends the session.
	now := time.Now()
	absoluteDeadline := createdAt.Add(absoluteCap)
	if !now.Before(expiresAt) || !now.Before(absoluteDeadline) {
		return nil, nil, ErrSessionNotFound
	}

	// Slide the idle window forward, clamped to the absolute cap.
	// Best-effort + throttled: a failed or skipped write doesn't
	// invalidate the still-valid session for this request.
	newExpiry := now.Add(idleWindow)
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

// PurgeExpired removes all expired sessions. Uses MaxSessionAbsoluteCap
// as the global ceiling so sessions with a longer per-club cap are not
// prematurely purged.
func (s *SessionService) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < NOW() OR created_at < $1`,
		time.Now().Add(-MaxSessionAbsoluteCap),
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

// clubIdleWindow fetches the per-club session idle window from club_settings,
// clamped to allowed bounds. Returns the default on any error.
func (s *SessionService) clubIdleWindow(ctx context.Context, clubID string) time.Duration {
	var minutes *int
	_ = s.db.QueryRow(ctx,
		`SELECT (value::text)::int FROM club_settings
		 WHERE club_id = $1 AND key = 'session_idle_minutes'`,
		clubID,
	).Scan(&minutes)
	return clampSessionDuration(minutesToDuration(minutes, sessionIdleWindow),
		MinSessionIdleWindow, MaxSessionIdleWindow)
}

func minutesToDuration(minutes *int, fallback time.Duration) time.Duration {
	if minutes == nil {
		return fallback
	}
	return time.Duration(*minutes) * time.Minute
}

func clampSessionDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}
