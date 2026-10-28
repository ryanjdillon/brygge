# Online payments: providers

How Brygge charges a checkout order through a payment service, and the decisions taken for adding card payments with Stripe. For Vipps settlement reconciliation and GL posting, see [`backend/internal/accounting/README.md`](../../backend/internal/accounting/README.md); for testing Vipps locally, [vipps-local-testing.md](vipps-local-testing.md).

## The model

Everything a payer can pay online is an **order** (`orders`): a faktura pay link (with `order_sources` rows pointing at the invoices it settles), a harbor QR guest payment, or a checkout with ad-hoc catalog lines (`order_lines`). An order is durable; a **payment attempt** (`payment_attempts`) is one short-lived payment at a provider to pay it. A payer who abandons an attempt clicks again and gets a fresh one on the same order; the earlier attempt keeps its row and reference, so if the payer completes it after all, its webhook still finds the order. The newest attempt with a provider reference is the order's **current** one.

```
/betal/{token}, /qr/{token}, checkout pay
        │  gate: providers.Configured(); provider: the payer's choice or defaultProvider()
        ▼
mintOrderPayment(ctx, providerName, orderID, cancelURL)   handlers/order_pay.go
        │  amount cap + imported-receivable gate, orderTotal, guest? member email
        │  payment_attempts row inserted (created)
        ▼
payments.Provider.CreatePayment(Request) → Result   vipps / stripe .PaymentProvider
        │  reference stored on the attempt (and orders.vipps_reference for Vipps),
        │  payer redirected; on error the attempt is marked failed
        ▼
payer returns to /betal/{token}/kvittering           polls GET /checkout/{token}
        ▼
provider webhook (signature verified, event stored)  handlers/{vipps,stripe}_webhook.go
        │  reference (or metadata attempt id) → attempt → order, provider-only facts
        ▼
orderSettlement: authorizeAttempt / markAttemptCaptured / markAttemptFailed
                                                     handlers/order_settle.go
```

### `internal/payments`

The provider-neutral seam ([`payments.go`](../../backend/internal/payments/payments.go)):

- **`Provider`** — `Name()`, `CreatePayment(ctx, Request) (Result, error)`, `Refund(ctx, reference, amountOre, reason) (providerRefundID, error)`. `Refund` may return `ErrNotSupported`.
- **`Canceller`** — optional; voids an authorized, uncaptured payment.
- **`Request`** — order id, attempt id (unique per attempt, usable as an idempotency key), amount in øre, currency, the full description (the provider shortens it to its own limit), whether the order is a guest order, the linked member's email (empty for guests), the return URL (the order's receipt page) and the cancel URL (the page the payer came from). **`Result`** — the redirect URL and the provider's reference.
- **`Registry`** — the providers this deployment has credentials for. `Get(name)`, `Names()` and `Configured()`. A nil registry is valid and empty, which is how an instance without payment credentials runs.

The package holds no database code. Deciding what is owed, enforcing the club's cap, and recording the outcome stay in the handlers so every provider gets the same rules.

### Provider adapters

`vipps.PaymentProvider` ([`internal/vipps/provider.go`](../../backend/internal/vipps/provider.go)) wraps the ePayment client. It owns what is specific to Vipps:

- the `chk-<32 hex>` reference, which is also the idempotency key (Vipps rejects a reused reference, so each attempt is new);
- the 100-byte `paymentDescription` limit;
- asking guests (and only guests) for their name via profile sharing;
- `WEB_REDIRECT` user flow.

`Refund` returns `ErrNotSupported`: Brygge cannot issue a Vipps refund yet (BRY-14). `CancelPayment` implements `Canceller`. It ignores the attempt id, cancel URL and payer email: Vipps returns to the receipt page on abort too.

`stripe.PaymentProvider` ([`internal/stripe/provider.go`](../../backend/internal/stripe/provider.go)) wraps a hand-rolled client ([`internal/stripe/client.go`](../../backend/internal/stripe/client.go): form-encoded calls with the secret key as Bearer and `Stripe-Version` pinned to `stripe.APIVersion`, no stripe-go dependency; the webhook endpoint should be created on the same version). It creates a hosted Checkout Session:

- `mode=payment`, `payment_method_types[]=card` only (Apple Pay and Google Pay ride on card), one line item with the øre amount and the description cut to 120 runes (the PaymentIntent description to Stripe's 1000);
- `customer_email` for a member whose email parses as a bare address (Stripe rejects the whole session on an invalid one); otherwise, and for a guest, the payer types it on the Stripe page;
- `payment_intent_data[capture_method]=manual`, so a completed session is an authorization;
- `metadata` and `payment_intent_data[metadata]` = `attempt_id`, `order_id`, `club_slug` (never the order token: it is the pay link's capability);
- `success_url` = the receipt page, `cancel_url` = the page the payer came from, `expires_at` = now + 60 minutes;
- the attempt id as `Idempotency-Key`, so a retried create returns the same session.

The reference is the session id (`cs_…`). `CancelPayment` retrieves the session: an authorized PaymentIntent (`requires_capture`) is cancelled, an open session is expired, a captured one returns `ErrNotCancellable`. `Refund` returns `ErrNotSupported` until BRY-446.

Payment creation goes through the registry, and each webhook voids a stale authorization through its adapter's `Canceller`. Settlement sync (`AccountingHandler`) and the Vipps webhook's other calls (capture on AUTHORIZED, `GetPayment` for a guest's name) keep the concrete `*vipps.Client`, and the Stripe webhook captures through `*stripe.Client`, because those are provider-specific operations with no neutral equivalent yet.

### Choosing the provider

`InvoiceHandler.defaultProvider()` is Vipps when it is configured, otherwise the one provider that is, so a Stripe-only deployment works end to end. `POST /checkout/{token}/pay` takes an optional `{"provider": "stripe"}` body and `POST /qr/{token}/pay` a `provider` field; a name that is not registered answers 400 `unknown payment provider`. `GET /betal/{token}` and the faktura email links use the default. Stripe is registered only when both `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` are set; one without the other logs a startup warning and registers nothing, because a payment Brygge never hears back about is never marked paid.

### Returning to the receipt page

Every provider returns the payer to `/betal/{token}/kvittering` (QR orders too, through their own order token), and `GET /betal/{token}` sends an already-paid order there. The page polls `GET /api/v1/checkout/{token}` with backoff (2, 3, 5, 8, then 10 s; at most 15 polls or about 90 s, inside the endpoint's per-IP limit) and shows the receipt (`paid_amount`, the faktura and lines), the pending spinner, a failed state with a link back to `/betal/{token}`, or, when no verdict arrives in time, that the payment is recorded as soon as the confirmation arrives and the payer can open the link again later to see the receipt (Brygge sends no receipt email of its own). It never assumes success: Vipps returns there on abort as well.

### Marking an order paid

`orderSettlement` ([`handlers/order_settle.go`](../../backend/internal/handlers/order_settle.go)) is the only code that changes an order's payment state, and it is only called from a signature-verified webhook (see the invariants "Only the payment provider can mark an order paid" and "A payment attempt that is not needed is never captured" in [reference/invariants.md](reference/invariants.md)). It works per attempt:

- `attemptByReference(ctx, clubID, provider, reference)` — the attempt (with its status, amount and currency), its order and whether the order is a guest order. `attemptByID(ctx, clubID, provider, id)` does the same from an attempt id a provider echoes back in metadata.
- `authorizeAttempt(ctx, attempt)` — one transaction holding the order row decides: skip (this attempt is already `captured` or `cancelled`: a replay), cancel (the order is paid or refunded, or another attempt is `authorized` or `captured`), or capture. The answer is written in the same transaction: `authorized` for capture, `cancelled` for a void, so no later event can read a voided attempt as payable. On cancel the webhook then voids the payment through `payments.Canceller`, best-effort (a failed void is logged; the authorization lapses at the provider). On capture it captures, and `releaseAuthorization` returns the attempt to `created` if the capture fails, so it doesn't block a retry (Stripe first asks where the PaymentIntent stands, see below). Only one of two concurrent authorizations is captured; if that capture then fails, the payer whose authorization was voided retries. Without the provider's credentials (no client, no `Canceller`) its webhook neither captures nor voids and leaves the attempt as it is; the authorization lapses at the provider.
- `markAttemptCaptured(ctx, attempt, capturedAt, payerName)` — the attempt becomes `captured`; the order becomes `paid` from `pending` or `failed` (a warning when from `failed`) with `paid_at = capturedAt` (zero: now) and `provider` set to the attempt's. If another attempt already paid the order, the order is left alone and the double payment is logged at error level for a refund. `guest_name = payerName` only on a guest order with no name (Vipps looks the name up for guest orders only). Each source invoice with no marker yet gets `online_paid_at = capturedAt` and `online_paid_provider`, and for Vipps also `vipps_paid_at`. It never sets `payment_id`, posts to the GL, sends email or writes audit: the settlement import owns those.
- `markAttemptFailed(ctx, attempt)` — the attempt becomes `failed` (from `created` or `authorized`); the order becomes `failed` only if the attempt is its current one (newest with a reference) and it is still `pending`.
- `backfillGuestEmail(ctx, attempt, email)` — the email a provider collected, on a guest order with none.

### Stripe events

`POST /api/v1/stripe/webhooks` ([`handlers/stripe_webhook.go`](../../backend/internal/handlers/stripe_webhook.go), [`stripe_webhook_dispatch.go`](../../backend/internal/handlers/stripe_webhook_dispatch.go)) verifies `Stripe-Signature` (HMAC-SHA256 over `"<t>.<body>"`, any `v1`, 300 s tolerance, 1 MiB cap; an empty secret rejects everything with 401) before decoding, stores the event in `payment_webhook_events` under the session id (or PaymentIntent id for `payment_intent.*` / `charge.*`), and dispatches it even when it was already stored. The club comes from the attempt, else the instance club. It answers 5xx only when the event could not be stored.

| Event | Action |
|-------|--------|
| `checkout.session.completed`, `checkout.session.async_payment_succeeded` | Session `complete` with a PaymentIntent: compare `amount_total` and currency with the attempt (mismatch: error log, void, `markAttemptFailed`), then `authorizeAttempt`. Capture: capture the PaymentIntent (key `capture-<attempt id>`; already captured counts as success), then `markAttemptCaptured` (now, payer name from `customer_details`) and `backfillGuestEmail`. A failed capture retrieves the PaymentIntent: still `requires_capture` → cancel it (the card hold is released at once) and `markAttemptFailed`; `succeeded` with the full amount → `markAttemptCaptured`; anything else, or a failed lookup or void → `releaseAuthorization` (the hold lapses; a later intent event settles it). Cancel: attempt already `cancelled` by `authorizeAttempt`, then `CancelPayment` best-effort. |
| `checkout.session.expired`, `checkout.session.async_payment_failed` | `markAttemptFailed`, unless the attempt is already `captured` or `cancelled`. |
| `payment_intent.canceled` | Attempt from `metadata.attempt_id`; `markAttemptFailed` unless already `captured` or `cancelled` (Brygge's own void arrives here too and must not fail an order another attempt pays). |
| `payment_intent.succeeded` | Attempt from `metadata.attempt_id`; `markAttemptCaptured` at the event time only when `amount_received` and currency equal the attempt's (a partial capture is logged at error level and pays nothing). A safety net for a capture whose answer was lost. |
| `charge.refunded`, `charge.dispute.*`, anything else | Stored only (refunds: BRY-446). |

The mint records the provider's answer (the reference, or `failed`) on a context detached from the payer's request, so a dropped request can't lose a reference to a payment the provider created.

Each write is guarded and runs on its own, so a redelivered or out-of-order event is a no-op and one failed write does not undo the others.

### Data model

- `payment_attempts` — `order_id`, `provider`, `provider_reference` (unique per provider once set), `status` (`created`, `authorized`, `captured`, `failed`, `cancelled`), `amount`, `currency`. The source of truth for provider references.
- `payment_refunds` — refunds of a captured attempt (`requested`, `succeeded`, `failed`). Table only; BRY-446 writes it.
- `payment_webhook_events` — raw events for providers other than Vipps, deduplicated on `(club_id, provider, event_id)`. The Stripe webhook writes it. Vipps keeps `vipps_webhook_events`.
- `invoices.online_paid_at` / `online_paid_provider` — the "received, awaiting settlement" marker for any provider. `invoices.online_payment_opt_in` and `clubs.online_max_amount` — the per-faktura override and the club's cap, for every provider. `clubs.payment_fee_account` — fees for every provider (default 7700). `clubs.stripe_clearing_account` — 1580, the card balance until payout.

The legacy columns `invoices.vipps_paid_at`, `invoices.vipps_payment_link_opt_in`, `clubs.vipps_max_amount` and `clubs.vipps_fee_account` are still written with the same values and read by nothing; BRY-450 drops them. `orders.vipps_reference` is still written on every Vipps mint and is not legacy: the settlement reconciliation in `internal/accounting` matches Vipps charges on it until BRY-445.

### Still Vipps-shaped

- `orders.vipps_reference` holds the latest Vipps attempt's reference for the settlement reconciliation (BRY-445).
- `vipps_webhook_events` is Vipps' event store.
- The gate's error codes and texts (`vipps_unavailable`, `vipps_over_limit`, "Vipps is not available"), the checkout GET's `vipps_blocked` response field, the `PATCH /invoices/{id}/vipps-link` route, the `invoice.vipps_link_set` audit action and the JSON fields `vipps_paid_at`, `vipps_payment_link_opt_in`, `vipps_max_amount` and `vipps_fee_account` are matched by the frontend; renaming them is BRY-444. They are fed from and write to the provider-neutral columns.

## Adding a provider

1. Implement `payments.Provider` (and `Canceller` if it can void) in the provider's own package, next to its API client.
2. Register it in `cmd/api/main.go` when its credentials are present. Secrets go in `/etc/brygge/env` (see the env layering rules in [configuration.md](configuration.md)).
3. Add its webhook handler: verify the signature, persist the event in `payment_webhook_events`, resolve the attempt with `attemptByReference` (or `attemptByID`), then call `authorizeAttempt` (if the provider authorizes before capturing) / `markAttemptCaptured` / `markAttemptFailed`. Do not write order or invoice payment state anywhere else.
4. Let the payer choose it: the pay routes resolve a requested name with `resolveProvider` and pass it to `mintOrderPayment`.
5. Book its fees and clearing balance with the accounting owner before going live.

## Card payments with Stripe: decisions

Taken by the owner on 2026-10-27. They bound the Stripe work; change them here first if they change.

1. **Hosted Stripe Checkout** (redirect). Card data never touches Brygge, so PCI scope stays SAQ-A and the CSP needs no change.
2. **Methods in v1:** card, Apple Pay and Google Pay, through `payment_method_types=["card"]` (the wallets ride on card). Not `automatic_payment_methods`, so a Dashboard toggle cannot add Klarna, Link or Stripe's own Vipps method, which are out.
3. **`/betal/{token}` becomes a choice page** — Vipps or "Kort / Apple Pay / Google Pay" — when both are available, and goes straight through when only one is.
4. **Email button** reads "Betal nå" with "Vipps eller kort" beneath it, and links to the choice page.
5. **One cap for all online payment.** `vipps_max_amount` becomes `online_max_amount`, and the per-faktura Auto/On/Off override applies to both providers. The club carries the fees (EEA rules restrict surcharging).
6. **One Stripe account per deployment**, secrets in `/etc/brygge/env`, no Stripe Connect.
7. **Refunds:** an in-app refund button for Stripe in v1. Refunds made in the Stripe Dashboard are still picked up by webhook and booked. Vipps refunds stay with BRY-14, through the same `Refund` interface.
8. **Accounting:** card fees go to account 7700, as Vipps fees do. The Stripe balance sits in a new clearing receivable, 1580, until payout.
9. **Payouts** daily in NOK to the club's main bank account. Stripe's email receipt is on. Checkout collects the guest's name and email for `orders.guest_name`.

### Sequencing

BRY-442 (this provider seam) → BRY-441 (provider-neutral data model, payment attempts) → BRY-443 (Stripe Checkout and webhook) → then in parallel BRY-444 (payer UI, error-code renames), BRY-445 (accounting), BRY-446 (refunds) and BRY-447 (operations).
