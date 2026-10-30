# Payment decisions

See [README.md](README.md) for the format. How payments work: [developer/payments.md](../developer/payments.md). How they are booked: [accounting.md](accounting.md).

PAY-1 to PAY-9 were taken together on 2026-10-27 (BRY-440) and bound the Stripe card-payment work.

### PAY-1: Hosted Stripe Checkout

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** card payments go through Stripe's hosted Checkout page (a redirect).
- **Why:** card data never touches Brygge, so PCI scope stays SAQ-A and the CSP needs no change.

### PAY-2: Card, Apple Pay and Google Pay only

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** v1 offers card, Apple Pay and Google Pay through `payment_method_types=["card"]` (the wallets ride on card), not `automatic_payment_methods`.
- **Why:** a Dashboard toggle can then not add Klarna, Link or Stripe's own Vipps method, which are out.

### PAY-3: The payer chooses the provider on the faktura page

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** when both are available, the payer chooses Vipps or "Kort · Apple Pay · Google Pay" on the `/betal/{token}` page; with only one there is a single button. The choice page is the existing itemised faktura page: nothing redirects past it, so the payer always sees what they are paying first. The email button reads "Betal nå" with "Vipps eller kort" beneath it.

### PAY-4: One cap for all online payment; the club carries the fees

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** one per-club cap, `online_max_amount`, applies to every provider, and the per-faktura Auto/On/Off override applies to both. Provider fees are not passed on to the payer.
- **Why:** EEA rules restrict surcharging.

### PAY-5: One Stripe account per deployment

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** each deployment has its own Stripe account, with its secrets in the deployment's secret env file. No Stripe Connect.

### PAY-6: Refunds through Brygge, as well as from the dashboard

- **Status:** accepted · 2026-10-27 · BRY-440, BRY-446
- **Decision:** v1 has an in-app refund button for Stripe payments, for the treasurer or an admin with a fresh second factor. Each click is safe to repeat and is audited. Refunds made in the Stripe Dashboard are still picked up and booked. Vipps refunds come later (BRY-14) through the same optional provider interface; until then the button is hidden for Vipps payments. How a refund is booked: ACC-2 to ACC-5.

### PAY-7: Card fees and the clearing account

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** card fees go to the payment fee account (7700), as Vipps fees do. The Stripe balance sits on a clearing receivable (1580) until payout. Both accounts are per-club settings.

### PAY-8: Daily payouts in NOK

- **Status:** accepted · 2026-10-27 · BRY-440
- **Decision:** Stripe pays out daily in NOK to the club's main bank account. Stripe's email receipt is on. Checkout collects a guest's name and email.

### PAY-9: Card is offered only for amounts the provider can charge

- **Status:** accepted · 2026-10-28 · BRY-470
- **Decision:** a provider with a minimum amount (Stripe: 3 kr in NOK) is not offered below it; the payer sees the other options.

### PAY-10: A full refund of a booking order cancels the booking

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** when an order that paid for a booking (a guest slip or a room) is refunded in full, the booking is cancelled and the slip or room is freed.
- **Consequences:** online orders don't create bookings yet (QR guest payments carry no booking), so this applies once a booking checkout exists.

### PAY-11: Brygge links to the Stripe Dashboard for what only Stripe can do

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** where only Stripe can act (answering a dispute with evidence, looking into a payment), Brygge links to the page in the Stripe Dashboard instead of building the screen. A link is `https://dashboard.stripe.com/<path>`, with `/test` after the domain when the configured key is a test key, and never contains the account id.
- **Why:** the evidence for a dispute lives in Stripe, and an account id in a link would put it in every email and screenshot.
- **Consequences:** `stripe.DashboardURL` builds the links; the first use is the dispute link in the treasurer's email, the second the link to the payment (`/payments/<pi_…>`) in the refund dialog (BRY-497).

### PAY-12: A refund request is sent again under its own key, and only a Stripe error is a refusal

- **Status:** accepted · 2026-10-29 · BRY-497
- **Decision:** a stored refund request the provider has no refund for is sent again under the same request key while the key is young (under 23 hours), by the replay of the click and by the sweep; after 24 hours with nothing found it is marked failed. A refund is never sent under a new key. Only a parseable Stripe error object (it has a `type`) answered with a 4xx that does not mean "try again" is a refusal that fails the request; an unparseable body, a 408, a 409, a 429 (rate limit, lock timeout), an idempotency error, a key still in use and every 5xx leave the outcome unknown, and the request stays requested.
- **Why:** a request that never left (a crash between the commit and the call, a lost commit answer) must not hold its amount for a day, and the provider's idempotency makes a second send under the same key safe. A 4xx from a proxy or edge in front of a request that did arrive must not be read as "Stripe refused": failing the row would let the person refund again under a new key and refund twice.
- **Consequences:** `RefundService` resends from the replay and the sweep (`refundResendWindow`, 23 hours; give-up 24 hours). `stripe.classifyRefundError` holds the rule. The refund list is read to the end (up to ten pages) or the look-up fails, so "not found" is never a truncated list.

### PAY-13: Online payment is NOK only; other currencies are refused at mint

- **Status:** accepted · 2026-10-30 · BRY-452
- **Decision:** `mintOrderPayment` and the QR pay route refuse an order or price item whose currency is not NOK with 409 `unsupported_currency`, for every provider. A blank currency reads as NOK, as the column default does.
- **Why:** the ledger, the reconciliation of both providers and the minimum-amount table assume NOK. Passing another currency through would charge a payer correctly at the provider and book it at the wrong value.
- **Consequences:** a club that prices a QR item in another currency gets a payment page that says it can only be paid in kroner and to ask the harbor master. Supporting another currency is a separate piece of work across booking and reconciliation.
