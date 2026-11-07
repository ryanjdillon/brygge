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

// loginFailStageDedupeWindow is how long one staged auth.login_failed row
// (a peek refusal) stands for its subject, method, reason and stage: a
// member who reopens an old email writes one row an hour, not one per open,
// while repeated attempts still show up hourly.
const loginFailStageDedupeWindow = time.Hour

// Login methods recorded as details.method on auth.login_success and
// auth.login_failed (BRY-659).
const (
	loginMethodMagicLink   = "magic_link"
	loginMethodPasskey     = "passkey"
	loginMethodJoinConfirm = "join_confirm"
)

// Stages recorded as details.stage on a magic-link auth.login_failed when
// the refusal came from somewhere other than the login itself. A refusal by
// POST /auth/verify carries no stage; one by the verify page's peek
// (POST /auth/verify/peek, BRY-660) carries loginStagePeek.
const loginStagePeek = "peek"

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
// covers that volume. stage, when not "", is recorded as details.stage.
//
// Without a stage (the login itself), a row with no actor is written at most
// once per loginFailDedupeWindow per client IP bucket (an IPv6 /64, see
// middleware.IPBucket), method and reason (dedupe, may be nil), so random
// tokens from one address cannot flood the table, and a row naming a user is
// always written. With a stage (a peek), every row is deduped for
// loginFailStageDedupeWindow per subject (the user for an attributed row, the
// IP bucket otherwise), method, reason and stage. Without Redis, or when it
// errors, the row is written.
func auditLoginFailed(ctx context.Context, a *audit.Service, dedupe *redis.Client, r *http.Request, userID, clubID, clubSlug, method, reason, stage string) {
	if a == nil {
		return
	}
	switch {
	case stage != "":
		if !firstLoginFailInWindow(ctx, dedupe, stagedLoginFailDedupeKey(r, userID, method, reason, stage), loginFailStageDedupeWindow) {
			return
		}
	case userID == "":
		if !firstAnonymousLoginFail(ctx, dedupe, r, method, reason) {
			return
		}
	}
	if clubID == "" {
		clubID = a.ClubIDForSlug(ctx, clubSlug)
	}
	details := map[string]string{"method": method, "reason": reason}
	if stage != "" {
		details["stage"] = stage
	}
	a.LogAction(ctx, clubID, userID, r.RemoteAddr, audit.ActionLoginFailed, "session", "", details)
}

// firstAnonymousLoginFail reports whether no anonymous failure row for the
// request's IP, method and reason was written in the last window, claiming
// the window if so. Without Redis, or when Redis errors, it says yes: an
// extra row is better than a lost one, and the route's rate limiter still
// bounds the volume.
func firstAnonymousLoginFail(ctx context.Context, rdb *redis.Client, r *http.Request, method, reason string) bool {
	return firstLoginFailInWindow(ctx, rdb, loginFailDedupeKey(r, method, reason), loginFailDedupeWindow)
}

// firstLoginFailInWindow claims key for window and reports whether it was
// free. Without Redis, or when Redis errors, it says yes.
func firstLoginFailInWindow(ctx context.Context, rdb *redis.Client, key string, window time.Duration) bool {
	if rdb == nil {
		return true
	}
	ok, err := rdb.SetNX(ctx, key, 1, window).Result()
	if err != nil {
		return true
	}
	return ok
}

// stagedLoginFailDedupeKey is the Redis key of the staged-failure dedupe.
// The subject is the user the refused link names, or, for a token that
// names no one, the client's rate-limit IP bucket.
func stagedLoginFailDedupeKey(r *http.Request, userID, method, reason, stage string) string {
	subject := "ip:" + middleware.IPBucket(r.RemoteAddr)
	if userID != "" {
		subject = "user:" + userID
	}
	return "loginfail:" + stage + ":" + subject + ":" + method + ":" + reason
}

// loginFailDedupeKey is the Redis key of the anonymous-failure dedupe. The
// client is its rate-limit IP bucket, so an IPv6 client counts per /64 and
// cannot dodge the dedupe by rotating addresses within its prefix.
func loginFailDedupeKey(r *http.Request, method, reason string) string {
	return "loginfail:" + middleware.IPBucket(r.RemoteAddr) + ":" + method + ":" + reason
}
