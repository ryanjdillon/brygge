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

A handler that serves a whole sub-area under `/admin` can own its mount: `WaitingListHandler.MountAdminRoutes(r, roleGate, freshGate)` (BRY-477) registers the list behind the role gate alone (the parent `/admin` group supplies the 12-hour `RequireAdminTOTP`) and enroll, offer and reorder behind `freshGate` too. Admin routes never live beside the member routes: a list that returns member contact details outside `/admin` skips the step-up. `TestWaitingListAdminRouteGates` walks every route and fails when one is added without a gate decision.

`GDPRHandler.MountAdminRoutes(r, roleGate, freshGate)` (BRY-481, BRY-509) follows the same pattern: the deletion-request queue and legal-document editor sit behind an admin-only role gate (GDPR is one function, held by the site admin; the SPA page is admin-only too), inside the `/admin` step-up. `POST …/deletion-requests/{id}/process` erases or anonymises a member irreversibly, so it alone also takes `freshGate`; publishing a legal document changes no member's recorded consent, so the editor stays on the step-up. A member's own export, deletion request and consents stay on `/members/me`. A request cannot be processed before `grace_end` (14 days after the request) while Art. 12(3) allows one month, so anything listing requests as work shows them from `grace_end` and calls them overdue at request + 1 month (see `docs/developer/security/member-lifecycle.md`). `TestGDPRAdminRouteGates` walks the mount, proves board is refused and that the process route answers `totp_fresh_required` inside the step-up window.

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

- **Registry.** `InvoiceHandler` holds a `*payments.Registry` (`h.providers`), built in `cmd/api/main.go` from the providers the instance has credentials for. A nil or empty registry means bank transfer (KID) only: `h.providers.Configured()` is the gate on every pay route and on `ensureFakturaOrderToken`. The gate's error codes are provider-neutral: `online_payment_unavailable` (409 on the admin pay-link routes when no provider is configured) and `online_payment_over_limit` (409 when the invoice may not be paid online: over the cap or forced off), and `amount_below_minimum` (409 when the amount is under the provider's smallest charge, `payments.ErrAmountBelowMinimum`: on the public pay POSTs the chosen provider's, where the pages offer only `providersFor(total)` so it guards a stale page; on the admin pay-link route every configured provider's); the public pay routes answer "online payment is not available" / "could not start online payment" (JSON) or the Norwegian plain-text equivalents on `GET /betal/{token}`. The frontend matches on the codes, so renaming one means changing `useCopyPayLink.ts`, `PayInvoiceView.vue` and `QrCheckoutView.vue` in the same commit.
- **Minting.** `mintOrderPayment(ctx, providerName, orderID, cancelURL)` (`order_pay.go`) owns everything provider-neutral: the amount cap and imported-receivable gate (`orderHasOnlineBlockedSource`), the provider's minimum charge (`payments.CheckMinimum`, before the attempt row exists), the total and description (`orderTotal`), whether the order is a guest order and the member's email (`orderPayer`), the return URL (the order's `/betal/{token}/kvittering` receipt page), and the `payment_attempts` row: inserted (`created`) before the provider is called, its id passed to the provider, marked `failed` if the call fails, and given the returned reference after (Vipps references are also copied to `orders.vipps_reference`, which reconciliation matches on). The provider owns its reference format, length limits and API mapping. Pay routes pick the provider with `resolveProvider` (the payer's optional `provider`, else `defaultProvider()`: Vipps when configured, otherwise the one that is; an unregistered name is 400). The pages learn what to offer from `availableProviders()` (default first) in the checkout and QR GETs, narrowed to `providersFor(total)` where the total is known; see [docs/developer/payments.md](../../../docs/developer/payments.md#choosing-the-provider).
- **Stripe adapter.** `stripe.PaymentProvider` (`internal/stripe/provider.go`) creates a card-only Checkout Session with manual capture; the session id is the reference. `StripeWebhookHandler` (`stripe_webhook.go`: verify, store in `payment_webhook_events`; `stripe_webhook_dispatch.go`: event → settlement) captures through `*stripe.Client` and voids through the adapter's `Canceller`; `payout.*` events go to `accounting.IngestStripePayout` (set with `SetLedger`) and never book; `charge.refunded` / `charge.refund.updated` events (`stripe_refund_webhook.go`) place their payment the same way, list the charge's refunds from Stripe and upsert `payments.UpsertProviderRefund` (a refund list or payment lookup that fails answers 5xx; the table is the only thing written); `charge.dispute.*` events (`stripe_dispute_webhook.go`) find their payment through the dispute's PaymentIntent (`resolveDispute`, both through `resolveIntentPayment` in `stripe_intent_payment.go`, which also decides the club the event is stored under), upsert `payments.UpsertDispute` and, on the dispute's first event, email `clubs.treasurer_email` through the mailer set with `SetMailer` (best effort, never fails the event); a dispute that cannot be stored answers 5xx, and `dispatch` returns that error. Dashboard links come from `stripeDashboardURL(cfg, path)`. `HandleStripeSync` (`stripe_balance_sync.go`, `POST /accounting/stripe/sync`, money-gated in `MountLedgerRoutes`, audited `accounting.stripe_balance_synced`) runs the same ingest for the whole balance and then books the ready charges and fees as the caller and clears the payouts that are ready (`TrySyncAndBookStripeBalance`; the response is the ingest result plus `booking` and `payouts`; a failed ingest is 502, a booking pass or payout sweep failing after a good ingest is 200 with `booking.error` / `payouts.error`), answering 409 while another Stripe sync or booking for the club runs; `HandleListStripeReview` (`GET /accounting/stripe/review`, open to every accounting role like the other read lists) lists the rows held for review; `stripe_transactions.go` serves the manual actions on those rows (BRY-466): `HandleListStripeTransactions` (`GET /accounting/stripe/transactions?status=needs_review|dismissed&limit=&offset=`, every accounting role, a `shared.PaginatedResponse` whose items carry `allowed_actions` computed by the service so the SPA never re-derives the rules), `HandleStripeTxnPotentialInvoices` (`GET …/transactions/{txnID}/potential-invoices`) and `HandleListStripeAssignableAccounts` (`GET /accounting/stripe/assignable-accounts`) for its dialogs, and `POST …/transactions/{txnID}/assign-invoice`, `/assign-account`, `/dismiss`, `/undismiss` (money-gated in `MountLedgerRoutes`, audited `stripe_txn.assigned_invoice`, `stripe_txn.assigned_account`, `stripe_txn.dismissed`, `stripe_txn.undismissed` on resource `stripe_balance_transaction`; a repeated dismiss or undismiss that changed nothing is not audited), answering an `accounting.StripeTxnRefusal` as 409 `{error, code, reason}`, a busy Stripe slot as 409 `busy` and an unknown row or faktura (or a non-uuid id) as 404 (`stripeTxnFailed`); `HandleListStripePayouts` (`GET /accounting/stripe/payouts?cleared=false`, every accounting role; only `cleared=false` is accepted) lists the uncleared paid NOK payouts with their members' counts and net for the clearing picker, and `HandleClearStripePayout` (`stripe_payouts.go`, `POST /accounting/stripe/payouts/{payoutID}/clear` `{bank_row_id}`, money-gated in `MountLedgerRoutes`, audited `stripe_payout.cleared`) clears one by hand (`accounting.ClearStripePayoutManually`), answering a refusal as 409 with its code (`open_members` for `members_open`, `open_balance` for `balance_open`), a Stripe slot still busy after a 5 s wait as 409 `busy` and an unknown payout or row as 404; the bank sync and bank import responses carry the booking pass and payout sweep they ran as `stripe_booking` and `stripe_payouts`; the Tildel list (`ListUnmatchedBankRows`) carries `provider_payout` and, for a Stripe payout deposit, `provider_payout_reason` and `provider_payout_by_hand`, and the four manual assigns in `bank_rows.go` answer a provider payout deposit (Vipps or Stripe) with 409 `provider_payout` (`providerPayoutConflict`: `provider`, `bank_row_id`); the payout webhook ingests in a goroutine after answering (`payouts` WaitGroup, for tests). The event table is in [`docs/developer/payments.md`](../../../docs/developer/payments.md#stripe-events).
- **Vipps adapter.** `vipps.PaymentProvider` (`internal/vipps/provider.go`) wraps `*vipps.Client`. Payment creation goes through the registry, and the webhook voids stale authorizations through the adapter's `payments.Canceller`; `AccountingHandler` (settlement sync) and `VippsWebhookHandler` (capture, payer name) keep the concrete client for the rest.
- **Settlement.** `orderSettlement` (`order_settle.go`) is the only code that marks an order paid or failed, and it works per attempt: `attemptByReference` (found / not found / lookup error: a lookup error makes the webhook answer 5xx), then `authorizeAttempt` (capture / skip / void, decided and recorded on the attempt under the order's row lock; the provider void after it is best-effort), `releaseAuthorization` after a failed capture, `markAttemptCaptured`, `markAttemptFailed`. Every one of them returns its database error and the dispatch passes it up, so the webhook answers 5xx and the provider redelivers; never log-and-continue in this path, and never fall back to capturing when a decision fails. Only descriptive writes (guest name and email, card brand) are log-only. A capture's writes share one transaction (`settleCapture`) so a retry sees either all of them or none. Each provider's webhook handler verifies the signature, resolves the attempt from its reference, collects what only it knows (Vipps: the consented payer name via `GetPayment`, for guest orders only), and calls them. See the invariant "A payment attempt that is not needed is never captured". Webhooks live per provider: `vipps_webhook.go` (verify, dedupe into `vipps_webhook_events`) and `vipps_webhook_dispatch.go` (event → settlement).

### Common misses

- Every settlement write is guarded (attempt and order status, `online_paid_at IS NULL`, empty `guest_name`) because providers redeliver webhooks and `vipps_webhook.go` dispatches even when the event row already existed. Keep a new write idempotent the same way.
- `orders.paid_at` and `invoices.online_paid_at` are both the provider's capture time (now, when the event has none), so the harbor overview's date filter and the faktura marker agree.
- The webhook resolves references through `payment_attempts`, not `orders.vipps_reference`: an order keeps every attempt, and only the newest is on the order.
- A new pay route must use the `Configured()` gate *and* go through `mintOrderPayment`, never call a provider directly, or it skips the amount cap.
- A completed Stripe session is an authorization, not a capture: it goes through `authorizeAttempt` like a Vipps AUTHORIZED, and only `markAttemptCaptured` after a successful capture (or `payment_intent.succeeded`) pays the order. Never treat `checkout.session.completed` as paid.
- Stripe stores the first response to an `Idempotency-Key` for 24 hours, errors (including 5xx) too, and replays it. Retrying a failed capture with `capture2-<attempt id>` only repeats the failure, so after one the webhook retrieves the PaymentIntent and acts on its status instead. Stripe also refuses a key reused with different parameters (`idempotency_error`), so the key carries a version (`captureKeyPrefix`) that is bumped whenever the capture's parameters change; a refused key is never treated as a failed capture (which would void the hold) — `CapturePaymentIntent` re-reads the PaymentIntent and captures it under a derived key.
- A provider's cancellation event can be Brygge's own void of a stale authorization. Check the attempt is not already `captured` or `cancelled` before `markAttemptFailed`, or the order another attempt is paying is failed.
- Return URLs go to the receipt page, which polls the order; it must not show success on its own, since Vipps returns there on abort too.
- How a Stripe payment was made (`payment_attempts.card_brand` / `card_wallet`) is recorded by `recordAttemptCard` *after* settlement and is best-effort. `CapturePaymentIntent` answers a 2xx it cannot decode as success with a nil PaymentIntent, and `ChargeRef` / `PaymentMethodDetails` decode an odd shape as empty, so a change in Stripe's charge object can never fail a capture or a balance page. Keep it that way when reading more of the charge.
- A filter on the harbor overview (`harbor_payments.go`; `kind` takes one kind or a comma-separated list) belongs in the SQL, before `LIMIT`: filtering rows in Go after the limit let newer rows of another kind fill the page and hide the ones asked for.

## Common files worth knowing

- `responses.go` — `Error`, `JSON` helpers
- `auth.go` — `HandleMe` (`/session/me`), exposes `fresh_totp_window_ms` so the SPA countdown stays in sync
- `features.go` — `/features` endpoint; reads per-club DB columns with env-var fallback
- `members.go` `HandleReportIssue` — `POST /members/me/slip/issues` files a task in the `harbor-maintenance` project, so it answers 404 `feature disabled` while `cfg.Features.Projects` is off (handler guard, not a route mount: it shares the `/members` group). The SPA hides the report button on the DB-resolved `projects` flag. BRY-515's reporting module takes over per-module intake.
- `order_pay.go` + `checkout_public.go` + `harbor_qr_public.go` + `payables_public.go` — public pay links and checkout; `order_settle.go` — marking orders paid (see Payment providers above)
- `invoices.go` + `invoices_bulk_actions.go` + `invoices_bulk.go` — faktura lifecycle. Bulk-create lives separately because it uses a different request shape (per-user lines) than the per-row actions.
- `invoice_credit.go` — credit-note links (BRY-342): candidates, link (fresh TOTP) and unlink. A link only records the settlement and never posts; decide "is this owed?" with `finance.OwedByMemberSQL`, never `payment_id`. The candidates endpoint doubles as the manual search (BRY-393): `?any=1` drops the payer and amount predicates, `?q=` matches faktura number or payer name (LIKE-escaped), and the list is capped at 25 with `truncated`. A cross-payer link needs `confirm_other_member` unless the credit note has no payer. A document a card refund issued (a credit note, or the debit faktura after a failed refund; `refundDocumentSQL`, linked through `refund_documents`) is refused by candidates, link, void and delete with 409 `refund_document` (a faktura with refund credit notes also refuses a manual credit link, 409 `refund_credited`), and the faktura list reports a refund credit note as `credit_state: "refund"` with the faktura it credits and `refund_document: true`.
- `invoice_refund_document_sender.go` — `RefundDocumentSender`, the delivery worker's sender for `invoice_refund_document` rows the Stripe booking pass queues: renders and stores the document's PDF (`loadStoredInvoicePDF`, shared with the PDF regenerate) and emails it when the row has an address. Registered in `cmd/api/main.go` with the invoice reminder sender, whose worker also gets the accounting service's kick (`SetDeliveryKick`).
- `accounting.go` — bank imports, Vipps reconciliation endpoints, journal entries, periods. The journal void goes through `VoidLedgerEntry`, never `VoidJournalEntry`: an entry booked from a Stripe row is refused with 409 `{code: "stripe_entry", reason}` and one a bank row or Stripe payout points at with 409 `{code: "bank_entry", reason}`; they are undone from the Stripe row or Tildel instead. Refusals log at Info/Warn, only unexpected failures at Error
- `order_refunds.go` — the refund button's API (BRY-497): `OrderRefundHandler.Mount` registers `/admin/orders/{orderID}/refund` (POST: treasurer/admin plus `RequireFreshTOTPDefault`, audited `order.refund_requested`) and `/refunds` (GET: treasurer/admin), mounted in `cmd/api/main.go` under the accounting flag. A refusal from `payments.RefundService` is 409 `{error, code}` (`RefundRefusal`), invalid input 400, an unknown order or attempt 404, a refund Stripe refused 502 `refund_failed` with `reason`, an unconfirmed one 202. The handler also defines `refundableOrders` / `mayRefund`, which `HarborHandler.markRefundable` (`can_refund` per payment on `recent-payments`) and `InvoiceHandler.refundOrdersFor` (`refund_order_id` per card-paid faktura on the faktura list) use so the SPA shows the button only to a treasurer or admin for an order that can be refunded. The flow, codes and sweep are in [`docs/developer/payments.md`](../../../docs/developer/payments.md#refunds).
- `invoice_reissue.go` — "Ny faktura" after a refund (BRY-498): `HandleReissueAfterRefund`, `POST /admin/financials/invoices/{invoiceID}/reissue-after-refund` (treasurer/admin plus `RequireFreshTOTPDefault`, accounting flag, audited `invoice.reissued_after_refund`). `accounting.ReissueAfterRefund` stores the draft and the link under the credited faktura's lock; the handler then renders the PDF (`ensureInvoicePDF`, shared with `RefundDocumentSender`) and sends and books it through `emailNewInvoice`, the step the single create form uses too. 201 the new faktura (`sent_at` null when it could not be emailed; `warnings` when a step after the commit failed), 404, 409 `{error, code}` `not_refunded` / `already_reissued`. Due date is `defaultFakturaDueDays` (21) from today, the forms' default.
- `refund_surface.go` — how refunds and disputes show in the lists (BRY-498): `refundStateOf`, the Dashboard link helpers (`stripePaymentDashboardURL`, `stripeDisputeDashboardURL`, `disputesNeedingResponsePath`), and the lookups behind `InvoiceHandler.addRefundState` (faktura list: `refund_state`, `refunded_amount`, `refund_credit_notes`, `credit_state` `refunded`, `can_reissue`, `reissued_invoice_*`, and, for a treasurer/admin only, `dispute_status`, `payment_url` and `dispute_url`; reads only the page's fakturas) and `MembersHandler.addMemberRefundState` (the member list: the state and the credit notes only). `HandleRecentPayments` lists orders `paid` or `refunded` and adds `refund_state`, `refunded_amount`, and, for a treasurer/admin only, `dispute_status`, `dispute_due_by`, the links and `open_disputes` / `open_disputes_url`. Which source each list reads is in [`invariants.md`](../../../docs/developer/reference/invariants.md).
- `stripe_transactions.go` — the Stripe row list (`status=needs_review|dismissed|booked`) and its manual actions (assign, dismiss, undismiss, unassign, rebook). Every action is money-gated in `MountLedgerRoutes`, audited `stripe_txn.*`, and answers a refusal through `stripeTxnFailed`: 409 `{error, code, reason}`, 409 `busy`, 404 for an unknown row or faktura
- `contact.go` — the public contact form (BRY-475), `POST /contact` on its own 5/min per-IP limiter (key `contact:<ip>`, so it does not spend the magic-link login budget). It mails the message synchronously through the API's `email.Sender` with `SendWithHeaders`: To is `contactRecipient` (club `secretary_email`, else `chairman_email`; the one function to change when BRY-482's function allocation lands), From is the app's configured sender, and the submitter's address goes only in Reply-To. Subject and name lose every control character (header injection), fields are capped (`contactMax*`, 400) and the body is capped (413). A filled `website` honeypot answers 200 without sending or logging. Failures never claim success: 502 `contact_delivery_failed`, 503 `contact_recipient_not_configured` / `mail_not_configured`, all logged at Error without name, email, subject or message text. The sender is nil without SMTP, so the handler checks it before use.
- `club_settings.go` — settings split across modules now (site / economy / harbor / motorhome), but the GET/PATCH backing all of them is in this file

## Common changes

- Adding a route → [`../../../docs/developer/checklists/add-route.md`](../../../docs/developer/checklists/add-route.md)
- Adding a bulk action → [`../../../docs/developer/checklists/add-bulk-action.md`](../../../docs/developer/checklists/add-bulk-action.md)
- Adding an audit action → [`../../../docs/developer/checklists/add-audit-action.md`](../../../docs/developer/checklists/add-audit-action.md)
- Adding a path that creates an invoice → number it through `accounting.NextInvoiceNumberTx` (or create it with `accounting.CreateInvoiceTx`) in the transaction that inserts it, never with your own `MAX(invoice_number) + 1`: the numbering lock is what keeps a refund's credit note and a treasurer's faktura from sharing a number. See the "Faktura numbers come from one path" invariant.
- Adding an endpoint that changes an invoice → refuse imported fakturas (`import_source IS NOT NULL`) with `refuseImportedInvoice`, or skip them in a bulk action. `sent_at IS NULL` is not a safe proxy for "draft": imported rows have it too. See the "Imported fakturas are read-only" invariant.
