package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// loginFailDedupeWindow is how long one anonymous auth.login_failed row
// stands for its (client IP, method, reason): further ones in the window are
// not written.
const loginFailDedupeWindow = time.Minute

// Login methods recorded as details.method on auth.login_success and
// auth.login_failed (BRY-659).
const (
	loginMethodMagicLink   = "magic_link"
	loginMethodPasskey     = "passkey"
	loginMethodJoinConfirm = "join_confirm"
)

// Failure reasons recorded as details.reason on auth.login_failed. The
// magic-link ones mirror the 410 codes the verify page gets.
const (
	loginFailInvalid                  = "invalid"
	loginFailUsed                     = "used"
	loginFailExpired                  = "expired"
	loginFailPasskeyUnknownCredential = "passkey_unknown_credential"
	loginFailPasskeyVerification      = "passkey_verification_failed"
)

// loginFailReason maps a magic-link refusal code to its audit reason.
func loginFailReason(code string) string {
	switch code {
	case codeLoginTokenUsed:
		return loginFailUsed
	case codeLoginTokenExpired:
		return loginFailExpired
	default:
		return loginFailInvalid
	}
}

// auditLoginSuccess records a login once its session exists. The details
// carry only the method: no token, hash, session id, credential id or
// address, since anyone with the admin role can read the audit log.
func auditLoginSuccess(ctx context.Context, a *audit.Service, r *http.Request, userID, clubID, method string) {
	if a == nil {
		return
	}
	a.LogAction(ctx, clubID, userID, r.RemoteAddr, audit.ActionLoginSuccess, "session", "",
		map[string]string{"method": method})
}

// auditLoginFailed records a login attempt that presented a credential and
// was refused. userID is "" when the credential names no one (an unknown
// token or passkey); the row then has no actor and is scoped to clubSlug's
// club. Requests refused before a credential is read (rate limit, wrong
// content type, malformed body) are not recorded: the rate limiter's log
// covers that volume.
//
// A row with no actor is written at most once per loginFailDedupeWindow per
// client IP bucket (an IPv6 /64, see middleware.IPBucket), method and reason
// (dedupe, may be nil), so random tokens from one address cannot flood the
// table. A row naming a user is always written.
func auditLoginFailed(ctx context.Context, a *audit.Service, dedupe *redis.Client, r *http.Request, userID, clubID, clubSlug, method, reason string) {
	if a == nil {
		return
	}
	if userID == "" && !firstAnonymousLoginFail(ctx, dedupe, r, method, reason) {
		return
	}
	if clubID == "" {
		clubID = a.ClubIDForSlug(ctx, clubSlug)
	}
	a.LogAction(ctx, clubID, userID, r.RemoteAddr, audit.ActionLoginFailed, "session", "",
		map[string]string{"method": method, "reason": reason})
}

// firstAnonymousLoginFail reports whether no anonymous failure row for the
// request's IP, method and reason was written in the last window, claiming
// the window if so. Without Redis, or when Redis errors, it says yes: an
// extra row is better than a lost one, and the route's rate limiter still
// bounds the volume.
func firstAnonymousLoginFail(ctx context.Context, rdb *redis.Client, r *http.Request, method, reason string) bool {
	if rdb == nil {
		return true
	}
	ok, err := rdb.SetNX(ctx, loginFailDedupeKey(r, method, reason), 1, loginFailDedupeWindow).Result()
	if err != nil {
		return true
	}
	return ok
}

// loginFailDedupeKey is the Redis key of the anonymous-failure dedupe. The
// client is its rate-limit IP bucket, so an IPv6 client counts per /64 and
// cannot dodge the dedupe by rotating addresses within its prefix.
func loginFailDedupeKey(r *http.Request, method, reason string) string {
	return "loginfail:" + middleware.IPBucket(r.RemoteAddr) + ":" + method + ":" + reason
}
