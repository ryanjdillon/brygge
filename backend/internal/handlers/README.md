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
- The actor IP is `r.RemoteAddr`. Never read `X-Real-IP`, `X-Forwarded-For` or `True-Client-IP` in a handler; `middleware.ClientIP` has already resolved the trusted value (see [invariants](../../../docs/developer/reference/invariants.md#the-client-ip-is-rremoteaddr-set-only-from-a-trusted-proxy))

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

## Payment providers

Online payment goes through the provider-neutral interfaces in [`internal/payments`](../payments/payments.go); the full design and the card-payment decisions are in [`docs/developer/payments.md`](../../../docs/developer/payments.md).

- **Registry.** `InvoiceHandler` holds a `*payments.Registry` (`h.providers`), built in `cmd/api/main.go` from the providers the instance has credentials for. A nil or empty registry means bank transfer (KID) only: `h.providers.Configured()` is the gate on every pay route and on `ensureFakturaOrderToken`. The gate's error codes are provider-neutral: `online_payment_unavailable` (409 on the admin pay-link routes when no provider is configured) and `online_payment_over_limit` (409 when the invoice may not be paid online: over the cap or forced off); the public pay routes answer "online payment is not available" / "could not start online payment" (JSON) or the Norwegian plain-text equivalents on `GET /betal/{token}`. The frontend matches on the codes, so renaming one means changing `useCopyPayLink.ts` in the same commit.
- **Minting.** `mintOrderPayment(ctx, providerName, orderID, cancelURL)` (`order_pay.go`) owns everything provider-neutral: the amount cap and imported-receivable gate (`orderHasOnlineBlockedSource`), the total and description (`orderTotal`), whether the order is a guest order and the member's email (`orderPayer`), the return URL (the order's `/betal/{token}/kvittering` receipt page), and the `payment_attempts` row: inserted (`created`) before the provider is called, its id passed to the provider, marked `failed` if the call fails, and given the returned reference after (Vipps references are also copied to `orders.vipps_reference`, which reconciliation matches on). The provider owns its reference format, length limits and API mapping. Pay routes pick the provider with `resolveProvider` (the payer's optional `provider`, else `defaultProvider()`: Vipps when configured, otherwise the one that is; an unregistered name is 400). The pages learn what to offer from `availableProviders()` (default first) in the checkout and QR GETs; see [docs/developer/payments.md](../../../docs/developer/payments.md#choosing-the-provider).
- **Stripe adapter.** `stripe.PaymentProvider` (`internal/stripe/provider.go`) creates a card-only Checkout Session with manual capture; the session id is the reference. `StripeWebhookHandler` (`stripe_webhook.go`: verify, store in `payment_webhook_events`; `stripe_webhook_dispatch.go`: event → settlement) captures through `*stripe.Client` and voids through the adapter's `Canceller`; `payout.*` events go to `accounting.IngestStripePayout` (set with `SetLedger`) and never book. `HandleStripeSync` (`stripe_balance_sync.go`, `POST /accounting/stripe/sync`, money-gated in `MountLedgerRoutes`, audited `accounting.stripe_balance_synced`) runs the same ingest for the whole balance and then books the ready charges and fees as the caller and clears the payouts that are ready (`TrySyncAndBookStripeBalance`; the response is the ingest result plus `booking` and `payouts`; a failed ingest is 502, a booking pass or payout sweep failing after a good ingest is 200 with `booking.error` / `payouts.error`), answering 409 while another Stripe sync or booking for the club runs; `HandleListStripeReview` (`GET /accounting/stripe/review`, open to every accounting role like the other read lists) lists the rows held for review; the bank sync and bank import responses carry the booking pass and payout sweep they ran as `stripe_booking` and `stripe_payouts`; the Tildel list (`ListUnmatchedBankRows`) carries `provider_payout` and, for a Stripe payout deposit, `provider_payout_reason` and `provider_payout_by_hand`; the payout webhook ingests in a goroutine after answering (`payouts` WaitGroup, for tests). The event table is in [`docs/developer/payments.md`](../../../docs/developer/payments.md#stripe-events).
- **Vipps adapter.** `vipps.PaymentProvider` (`internal/vipps/provider.go`) wraps `*vipps.Client`. Payment creation goes through the registry, and the webhook voids stale authorizations through the adapter's `payments.Canceller`; `AccountingHandler` (settlement sync) and `VippsWebhookHandler` (capture, payer name) keep the concrete client for the rest.
- **Settlement.** `orderSettlement` (`order_settle.go`) is the only code that marks an order paid or failed, and it works per attempt: `attemptByReference`, then `authorizeAttempt` (capture / skip / void, decided and recorded on the attempt under the order's row lock; the provider void after it is best-effort), `releaseAuthorization` after a failed capture, `markAttemptCaptured`, `markAttemptFailed`. Each provider's webhook handler verifies the signature, resolves the attempt from its reference, collects what only it knows (Vipps: the consented payer name via `GetPayment`, for guest orders only), and calls them. See the invariant "A payment attempt that is not needed is never captured". Webhooks live per provider: `vipps_webhook.go` (verify, dedupe into `vipps_webhook_events`) and `vipps_webhook_dispatch.go` (event → settlement).

### Common misses

- Every settlement write is guarded (attempt and order status, `online_paid_at IS NULL`, empty `guest_name`) because providers redeliver webhooks and `vipps_webhook.go` dispatches even when the event row already existed. Keep a new write idempotent the same way.
- `orders.paid_at` and `invoices.online_paid_at` are both the provider's capture time (now, when the event has none), so the harbor overview's date filter and the faktura marker agree.
- The webhook resolves references through `payment_attempts`, not `orders.vipps_reference`: an order keeps every attempt, and only the newest is on the order.
- A new pay route must use the `Configured()` gate *and* go through `mintOrderPayment`, never call a provider directly, or it skips the amount cap.
- A completed Stripe session is an authorization, not a capture: it goes through `authorizeAttempt` like a Vipps AUTHORIZED, and only `markAttemptCaptured` after a successful capture (or `payment_intent.succeeded`) pays the order. Never treat `checkout.session.completed` as paid.
- Stripe stores the first response to an `Idempotency-Key` for 24 hours, errors (including 5xx) too, and replays it. Retrying a failed capture with `capture-<attempt id>` only repeats the failure, so after one the webhook retrieves the PaymentIntent and acts on its status instead.
- A provider's cancellation event can be Brygge's own void of a stale authorization. Check the attempt is not already `captured` or `cancelled` before `markAttemptFailed`, or the order another attempt is paying is failed.
- Return URLs go to the receipt page, which polls the order; it must not show success on its own, since Vipps returns there on abort too.

## Common files worth knowing

- `responses.go` — `Error`, `JSON` helpers
- `auth.go` — `HandleMe` (`/session/me`), exposes `fresh_totp_window_ms` so the SPA countdown stays in sync
- `features.go` — `/features` endpoint; reads per-club DB columns with env-var fallback
- `order_pay.go` + `checkout_public.go` + `harbor_qr_public.go` + `payables_public.go` — public pay links and checkout; `order_settle.go` — marking orders paid (see Payment providers above)
- `invoices.go` + `invoices_bulk_actions.go` + `invoices_bulk.go` — faktura lifecycle. Bulk-create lives separately because it uses a different request shape (per-user lines) than the per-row actions.
- `invoice_credit.go` — credit-note links (BRY-342): candidates, link (fresh TOTP) and unlink. A link only records the settlement and never posts; decide "is this owed?" with `finance.OwedByMemberSQL`, never `payment_id`. The candidates endpoint doubles as the manual search (BRY-393): `?any=1` drops the payer and amount predicates, `?q=` matches faktura number or payer name (LIKE-escaped), and the list is capped at 25 with `truncated`. A cross-payer link needs `confirm_other_member` unless the credit note has no payer.
- `accounting.go` — bank imports, Vipps reconciliation endpoints, journal entries, periods
- `club_settings.go` — settings split across modules now (site / economy / harbor / motorhome), but the GET/PATCH backing all of them is in this file

## Common changes

- Adding a route → [`../../../docs/developer/checklists/add-route.md`](../../../docs/developer/checklists/add-route.md)
- Adding a bulk action → [`../../../docs/developer/checklists/add-bulk-action.md`](../../../docs/developer/checklists/add-bulk-action.md)
- Adding an audit action → [`../../../docs/developer/checklists/add-audit-action.md`](../../../docs/developer/checklists/add-audit-action.md)
- Adding an endpoint that changes an invoice → refuse imported fakturas (`import_source IS NOT NULL`) with `refuseImportedInvoice`, or skip them in a bulk action. `sent_at IS NULL` is not a safe proxy for "draft": imported rows have it too. See the "Imported fakturas are read-only" invariant.
