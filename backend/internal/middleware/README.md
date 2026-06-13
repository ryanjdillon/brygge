# `middleware/` — HTTP middleware (auth, sessions, TOTP, rate-limit, metrics)

Where chi route gating lives. Roughly one middleware per concern, ordered carefully — see "Middleware order" below.

## Files

| File | What |
|---|---|
| `auth.go` | `AuthenticateSession` — reads the session cookie, validates against `sessions`, populates `claims` + `SessionInfo` in the request context. |
| `session.go` | `RequireRole`, `RequireAdminTOTP`, `RequireFreshTOTP`, `RequireFreshTOTPDefault`, `IsFreshTOTP`, `SetFreshTOTPWindow`, `FreshTOTPWindow`. |
| `ratelimit.go` | Per-IP / per-user sliding-window rate limit using Redis. |
| `metrics.go` | Prometheus / OTel HTTP request metrics. |

## Middleware order

```go
r.Use(chimw.RequestID)
r.Use(chimw.RealIP)
r.Use(requestLogger(log))
r.Use(metrics.Middleware)
r.Use(middleware.RateLimit(...))
r.Use(middleware.AuthenticateSession(...))  // populates ctx
// then per-route:
r.With(middleware.RequireRole(...)).With(middleware.RequireFreshTOTPDefault()).Post(...)
```

⚠️ **Don't reorder TOTP gates and `AuthenticateSession`.** `RequireFreshTOTP` and `RequireAdminTOTP` both read `SessionInfo` from the context that `AuthenticateSession` populates. Reversing the order produces nil-pointer dereferences in dev and silent admin-bypass in prod. This is listed in [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## TOTP gating

Two flavors:

### `RequireAdminTOTP(sessionService) func(http.Handler) http.Handler`

12-hour step-up window. Used at the top of every `/admin/*` route group. On a browser navigation, returns a 302 to `/admin/verify-totp?next=…`. On XHR/fetch, returns 403 + `{"error":"totp_required"}` so the SPA can render a modal.

### `RequireFreshTOTPDefault() func(http.Handler) http.Handler`

10-minute window by default (configurable via `AUTH_FRESH_TOTP_WINDOW` env var). Used for sensitive mutations. On failure, returns 403 + `{"error":"totp_fresh_required"}`; the SPA's `totpAwareFetch` catches this, prompts via the in-context modal, and retries the request transparently.

There's also `RequireFreshTOTP(window time.Duration)` for the rare case where a specific route needs a custom window. Almost everything should use `RequireFreshTOTPDefault()` — see [DIL-344] for why the configurable default was introduced.

The window is set at server startup with `middleware.SetFreshTOTPWindow(cfg.FreshTOTPWindow)`. The SPA reads the current value off `/session/me`'s `fresh_totp_window_ms` field so the countdown timer stays in sync.

### `IsFreshTOTP(ctx, window) bool`

In-handler check for conditional gating — e.g. "this route only requires step-up if the request body has a `delete_all` flag." Reads `SessionInfo.TOTPVerifiedAt` from the context.

## Adding a new gate

If you need a gate that doesn't fit the three flavors above:

1. Write a new `func RequireXyz(...) func(http.Handler) http.Handler` in `session.go`
2. Mirror the structure of `RequireFreshTOTP`: read `SessionInfo` from ctx, decide pass/fail, write the appropriate error shape
3. If the gate has a configurable window, surface it on `/session/me` so the SPA can render countdowns
4. Add to [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md) the "always after AuthenticateSession" rule

## Browser-navigation detection

`isBrowserNavigation(r)` checks `Sec-Fetch-Mode: navigate` + `Accept: text/html` to distinguish a top-level browser hit from an XHR. Used to choose between 302-redirect and 403-JSON responses. Don't reinvent this — the heuristic is non-obvious (some browsers omit `Sec-Fetch-Mode`).

## Context keys

`AuthenticateSession` populates two keys:

- `claims *jwt.Claims` (legacy name, struct is session-backed now) — `UserID`, `ClubID`, `Roles[]`
- `SessionInfo` — `TOTPEnabled`, `TOTPVerifiedAt *time.Time`

Read with `middleware.GetClaims(ctx)` and `middleware.GetSessionInfo(ctx)`. Both return nil if the middleware didn't run — usually a routing mistake, not a real "unauthenticated" case.

## Invariants

- `AuthenticateSession` runs before any `RequireXxx` gate
- `totp_verified_at` is the source of truth — never trust client timers
- TOTP middleware never returns 200 silently; failure paths always return 401 or 403 with a JSON error code the SPA can parse

Full list: [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).
