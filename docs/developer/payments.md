# Online payments: providers

How Brygge charges a checkout order through a payment service, and the decisions taken for adding card payments with Stripe. For Vipps settlement reconciliation and GL posting, see [`backend/internal/accounting/README.md`](../../backend/internal/accounting/README.md); for testing Vipps locally, [vipps-local-testing.md](vipps-local-testing.md).

## The model

Everything a payer can pay online is an **order** (`orders`): a faktura pay link (with `order_sources` rows pointing at the invoices it settles), a harbor QR guest payment, or a checkout with ad-hoc catalog lines (`order_lines`). An order is durable; a **payment** at the provider is a short-lived attempt to pay it. A payer who abandons an attempt clicks again and gets a fresh one on the same order.

```
/betal/{token}, /qr/{token}, checkout pay
        │  gate: providers.Configured()
        ▼
mintOrderPayment(ctx, providerName, orderID)        handlers/order_pay.go
        │  amount cap + imported-receivable gate, orderTotal, guest?
        ▼
payments.Provider.CreatePayment(Request) → Result   e.g. vipps.PaymentProvider
        │  reference stored on the order, payer redirected
        ▼
provider webhook (signature verified, event stored)  e.g. handlers/vipps_webhook.go
        │  reference → order, provider-only facts (payer name)
        ▼
orderSettlement.markOrderPaid / markOrderFailed      handlers/order_settle.go
```

### `internal/payments`

The provider-neutral seam ([`payments.go`](../../backend/internal/payments/payments.go)):

- **`Provider`** — `Name()`, `CreatePayment(ctx, Request) (Result, error)`, `Refund(ctx, reference, amountOre, reason) (providerRefundID, error)`. `Refund` may return `ErrNotSupported`.
- **`Canceller`** — optional; voids an authorized, uncaptured payment.
- **`Request`** — order id, amount in øre, currency, the full description (the provider shortens it to its own limit), whether the order is a guest order, and the return URL. **`Result`** — the redirect URL and the provider's reference.
- **`Registry`** — the providers this deployment has credentials for. `Get(name)` and `Configured()`. A nil registry is valid and empty, which is how an instance without payment credentials runs.

The package holds no database code. Deciding what is owed, enforcing the club's cap, and recording the outcome stay in the handlers so every provider gets the same rules.

### Provider adapters

`vipps.PaymentProvider` ([`internal/vipps/provider.go`](../../backend/internal/vipps/provider.go)) wraps the ePayment client. It owns what is specific to Vipps:

- the `chk-<32 hex>` reference, which is also the idempotency key (Vipps rejects a reused reference, so each attempt is new);
- the 100-byte `paymentDescription` limit;
- asking guests (and only guests) for their name via profile sharing;
- `WEB_REDIRECT` user flow.

`Refund` returns `ErrNotSupported`: Brygge cannot issue a Vipps refund yet (BRY-14). `CancelPayment` implements `Canceller`.

Only payment creation goes through the registry. Settlement sync (`AccountingHandler`) and the webhook (capture on AUTHORIZED, `GetPayment` for the payer name) keep the concrete `*vipps.Client`, because those are Vipps-specific operations with no neutral equivalent yet.

### Marking an order paid

`orderSettlement` ([`handlers/order_settle.go`](../../backend/internal/handlers/order_settle.go)) is the only code that changes an order's payment state, and it is only called from a signature-verified webhook (see the invariant "Only the payment provider can mark an order paid" in [reference/invariants.md](reference/invariants.md)).

- `markOrderPaid(ctx, orderID, capturedAt, payerName)` — `orders.status = 'paid'`, `paid_at = now()` only from `pending`; `guest_name = payerName` only on a guest order with no name; `invoices.vipps_paid_at = capturedAt` (zero: now) on each source invoice that has no marker yet. It never sets `payment_id`, posts to the GL, sends email or writes audit: the settlement import owns those.
- `markOrderFailed(ctx, orderID)` — `status = 'failed'` only from `pending`.

Each write is guarded and runs on its own, so a redelivered or out-of-order event is a no-op and one failed write does not undo the others.

### Still Vipps-shaped

These carry Vipps in their names and are left for the data-model change (BRY-441):

- `orders.vipps_reference` holds whichever provider's reference was minted last; an order has no record of earlier attempts, so a late capture of an older attempt does not match.
- `invoices.vipps_paid_at` is the "received, awaiting settlement" marker for any provider.
- `vipps_webhook_events` is Vipps' event store.
- The gate's error codes and texts (`vipps_unavailable`, `vipps_over_limit`, "Vipps is not available") and the checkout GET's `vipps_blocked` response field are matched by the frontend; renaming them is BRY-444.
- `clubs.vipps_max_amount` and `invoices.vipps_payment_link_opt_in` are the online-payment cap and per-faktura override.

## Adding a provider

1. Implement `payments.Provider` (and `Canceller` if it can void) in the provider's own package, next to its API client.
2. Register it in `cmd/api/main.go` when its credentials are present. Secrets go in `/etc/brygge/env` (see the env layering rules in [configuration.md](configuration.md)).
3. Add its webhook handler: verify the signature, persist the event with a dedupe key, resolve the order from the reference, then call `markOrderPaid` / `markOrderFailed`. Do not write order or invoice payment state anywhere else.
4. Let the payer choose it: callers of `mintOrderPayment` pass the provider name.
5. Book its fees and clearing balance with the accounting owner before going live.

## Card payments with Stripe: decisions

Taken by the owner on 2026-10-27. They bound the Stripe work; change them here first if they change.

1. **Hosted Stripe Checkout** (redirect). Card data never touches Brygge, so PCI scope stays SAQ-A and the CSP needs no change.
2. **Methods in v1:** card, Apple Pay and Google Pay, through `automatic_payment_methods`. Klarna and Stripe's own Vipps method are out.
3. **`/betal/{token}` becomes a choice page** — Vipps or "Kort / Apple Pay / Google Pay" — when both are available, and goes straight through when only one is.
4. **Email button** reads "Betal nå" with "Vipps eller kort" beneath it, and links to the choice page.
5. **One cap for all online payment.** `vipps_max_amount` becomes `online_max_amount`, and the per-faktura Auto/On/Off override applies to both providers. The club carries the fees (EEA rules restrict surcharging).
6. **One Stripe account per deployment**, secrets in `/etc/brygge/env`, no Stripe Connect.
7. **Refunds:** an in-app refund button for Stripe in v1. Refunds made in the Stripe Dashboard are still picked up by webhook and booked. Vipps refunds stay with BRY-14, through the same `Refund` interface.
8. **Accounting:** card fees go to account 7700, as Vipps fees do. The Stripe balance sits in a new clearing receivable, 1580, until payout.
9. **Payouts** daily in NOK to the club's main bank account. Stripe's email receipt is on. Checkout collects the guest's name and email for `orders.guest_name`.

### Sequencing

BRY-442 (this provider seam) → BRY-441 (provider-neutral data model, payment attempts) → BRY-443 (Stripe Checkout and webhook) → then in parallel BRY-444 (payer UI, error-code renames), BRY-445 (accounting), BRY-446 (refunds) and BRY-447 (operations).
