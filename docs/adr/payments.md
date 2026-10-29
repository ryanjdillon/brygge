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
