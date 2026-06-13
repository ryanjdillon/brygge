# `handlers/` — HTTP handlers for the admin and public API

One file per resource. Each handler is a method on a struct that holds dependencies (db, config, optional email/audit/log). Constructors return `*XHandler`; route registration in `cmd/api/main.go` wires them up.

## File layout convention

| File pattern | Contents |
|---|---|
| `<resource>.go` | All handlers for one resource (list, get, create, update, delete, custom). E.g. `invoices.go`, `accounting.go`, `bookings.go`. |
| `<resource>_bulk_actions.go` | `POST {ids: []}` style endpoints. Keep them separate so the per-row handler file stays scoped to one resource at a time. |
| `<resource>_test.go` | Tests for handlers in `<resource>.go`. Integration tests skip via `testutil.SkipIfNoDB`. |
| `helpers.go`, `responses.go` | Cross-resource utilities: `Error()`, `JSON()`, shared scan helpers. |

When in doubt, add to the existing file. A handler that doesn't fit any existing resource probably means a new resource — give it its own file.

## Handler struct convention

```go
type InvoiceHandler struct {
    db     *pgxpool.Pool
    config *config.Config
    email  email.Sender   // optional, may be nil in tests
    audit  *audit.Service // optional, may be nil in tests
    log    zerolog.Logger
}

func NewInvoiceHandler(
    db *pgxpool.Pool,
    cfg *config.Config,
    emailClient email.Sender,
    auditService *audit.Service,
    log zerolog.Logger,
) *InvoiceHandler {
    return &InvoiceHandler{
        db:     db,
        config: cfg,
        email:  emailClient,
        audit:  auditService,
        log:    log.With().Str("handler", "invoices").Logger(),
    }
}
```

Notes:

- `log.With().Str("handler", "…")` so log lines self-identify
- `email` and `audit` are optional — tests may pass nil. Always check `if h.audit != nil` before logging
- The `db` field is a pool, not a Tx. Methods that need a transaction call `h.db.BeginTx` and pass the Tx through

## Response shape conventions

Always use the helpers in `responses.go`:

```go
Error(w, http.StatusBadRequest, "ids is required")
JSON(w, http.StatusOK, map[string]any{"items": rows})
```

Never bare `http.Error` (skips JSON wrapping) or hand-marshal `json.NewEncoder(w).Encode(...)`. Consistency matters for SPA error parsing.

For PDF and binary responses, set `Content-Type` + `Content-Disposition` + `w.Write(bytes)` directly. See `HandleGetInvoicePDF` for the template.

## Audit pattern

```go
if h.audit != nil {
    h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
        audit.ActionInvoiceEmailed, "invoice", invoiceID,
        map[string]any{"email": deliverTo, "invoice_number": invoiceNumber})
}
```

- Always behind `if h.audit != nil`
- Use existing `audit.Action*` constants; never inline literals
- See [`../../../docs/developer/reference/audit-actions.md`](../../../docs/developer/reference/audit-actions.md) for the full list
- Never let the audit error bubble — the operation succeeded, log the failure and move on

## TOTP gating

Mutations beyond simple lookups get `RequireFreshTOTPDefault()` at route registration:

```go
r.With(
    middleware.RequireRole("treasurer", "admin"),
    middleware.RequireFreshTOTPDefault(),
).Post("/.../invoices/bulk-reminder", invoiceHandler.HandleBulkSendReminder)
```

Don't reinvent TOTP checks inside the handler — the middleware enforces and the SPA's `totpAwareFetch` re-prompts transparently on 403.

## Bulk action conventions

`POST {ids: []}` returns `{processed, skipped, failures: [...]}`. Two burns from past PRs:

1. **`Failures` must be initialized as `[]string{}`** — Go's nil slice serializes to JSON `null`, which crashes frontends doing `result.failures.length`. See [DIL-369 burn fixed in `ae46117`].
2. **Type names must be distinct** — there's already a `bulkInvoiceRequest`. Use `bulkInvoiceActionRequest` or similar.

Full checklist: [`../../../docs/developer/checklists/add-bulk-action.md`](../../../docs/developer/checklists/add-bulk-action.md).

## Auth flow inside a handler

```go
ctx := r.Context()
claims := middleware.GetClaims(ctx)
if claims == nil {
    Error(w, http.StatusUnauthorized, "authentication required")
    return
}
// claims.UserID, claims.ClubID, claims.Roles are always populated here
```

`middleware.GetClaims` returns nil if `AuthenticateSession` didn't run — usually means a public route accidentally called `GetClaims`, not that the user is unauthenticated. The session middleware sets a non-nil claims for every authenticated request.

For TOTP freshness checks inside a handler (e.g. conditionally requiring TOTP based on the request body), use `middleware.IsFreshTOTP(ctx, window)`.

## Resource scoping

Every query that touches club-scoped data must include `club_id = $X`:

```go
WHERE i.id = $1 AND i.club_id = $2
```

Even if `claims.ClubID` is the only club the user can access (typical for single-tenant deploys), the scope clause is non-negotiable. It's defense in depth against the day we support multi-club admin.

## Common files worth knowing

- `responses.go` — `Error`, `JSON` helpers
- `auth.go` — `HandleMe` (`/session/me`), exposes `fresh_totp_window_ms` so the SPA countdown stays in sync
- `features.go` — `/features` endpoint; reads per-club DB columns with env-var fallback
- `invoices.go` + `invoices_bulk_actions.go` + `invoices_bulk.go` — faktura lifecycle. Bulk-create lives separately because it uses a different request shape (per-user lines) than the per-row actions.
- `accounting.go` — bank imports, Vipps reconciliation endpoints, journal entries, periods
- `club_settings.go` — settings split across modules now (site / economy / harbor / motorhome), but the GET/PATCH backing all of them is in this file

## Common changes

- Adding a route → [`../../../docs/developer/checklists/add-route.md`](../../../docs/developer/checklists/add-route.md)
- Adding a bulk action → [`../../../docs/developer/checklists/add-bulk-action.md`](../../../docs/developer/checklists/add-bulk-action.md)
- Adding an audit action → [`../../../docs/developer/checklists/add-audit-action.md`](../../../docs/developer/checklists/add-audit-action.md)
