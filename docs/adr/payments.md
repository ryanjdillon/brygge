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

### PAY-14: A webhook never acknowledges an event whose lookup or settlement failed

- **Status:** accepted · 2026-10-30 · BRY-512
- **Decision:** a payment webhook answers 5xx on a database error in any step that decides or records the event's effect: the lookups (BRY-452) and the settlement itself, i.e. the authorization decision, the capture's writes, the failed-attempt writes and the release of an attempt after a failed capture. A decision that cannot be made never falls back to capturing. A capture's attempt, order and faktura-marker writes are one transaction (fakturas locked first, then the order, then the attempt), so a failure leaves none of them, and the writes that record an outcome after a provider call run on a context detached from the request. Provider-call failures, deterministic mismatches, guarded writes that change no row and descriptive data (guest name and email, card brand) are not errors and keep their handling.
- **Why:** the writes used to log and answer 200, so the provider stopped retrying and a captured payment could stay unpaid on its order, while a failed decision captured without the order lock and could take a duplicate payment that then needs refunding. Redelivery is free: the event is stored once, dispatch runs every time and the writes are guarded. Making the capture's writes atomic keeps a retry from finding an attempt already `captured` next to an unpaid order, which the decision would skip.
- **Consequences:** a database that stays down is retried at the providers' pace and logs an error per delivery. After a capture whose settlement failed, the retry calls the provider under the same idempotency key (Stripe `capture2-<attempt id>`, Vipps `capture-<reference>`) so the money is taken once. A new provider's webhook follows the same rule.

### PAY-15: The Payments page filters by per-club groups of price items

- **Status:** accepted · 2026-10-30 · BRY-522
- **Decision:** the chips of the payment overview (`GET /harbor/recent-payments`) are the club's filter groups, not a fixed list in code.
  - A group is a name, an icon from a fixed list, a position and a set of price items. A price item is in at most one group. Every club has two built-in groups: *Faktura* (orders that pay a faktura) and *Annet* (the fallback, always last, cannot be disabled or deleted).
  - An order is in the enabled group with the lowest position among the groups that hold one of its order-line items and the Faktura built-in when it pays a faktura; otherwise in Annet. One module, `internal/paymentfilters`, decides this for the overview and for the SMS alerts (BRY-521); nobody re-implements it.
  - A group has one name, typed by the board and shown as typed in every locale. A seeded group has no name but a label key, which the SPA translates (`recentPayments.kind.<key>`); "use the default name" goes back to it.
  - The slug is the stable id (`?kind=`, device storage, the admin API). It is made from the name at create and never changes. The seeded slugs equal the old kind ids, so links and stored choices keep working.
  - Writes (create, rename, icon, enable, items, order, delete) are open to board, harbor master, treasurer and admin, behind the 12-hour `/admin` step-up and **without** a fresh TOTP. Chair, vice chair and secretary read the groups like any admin viewer.
- **Why:** the harbor master reads the page to see what to collect and what to do, and which payments belong together is the club's call (a "Slipping" chip, a kiosk). A closed list in code meant a deploy per club per chip. One group per item keeps "which chip shows this payment" a property of the item; the position settles an order that holds items from several groups.
- **Consequences:**
  - **Existing clubs get groups from their own data (PLAT-1).** Migration 000123 creates Guest, Motorhome and Slipping groups only for a club that has a price item the old rules matched (category containing `guest`, category containing `motorhome` or `bobil`, name containing `slipping`; every category, archived items included) and assigns those items. A club with no such item gets only the two built-ins. The Slipping group ports the interim name rule the overview already had, so nothing changes at deploy; it is a deliberate exception to the operator-step consequence of PLAT-1, not a precedent for seeding a default. No Butikk (merch) group is seeded: no price-item category ever contained `merch`, so that chip was always empty. A club can make one by hand.
  - **Only order lines classify.** The group of an order comes from its `order_lines`; what a faktura bills (`invoice_lines` behind `order_sources`) never counts. An order that pays a faktura is Faktura unless one of its order lines (a faktura checkout with an add-on) holds an item of a group that ranks above Faktura, and then it is that group. A price item that can never be on an order line is billed on fakturas only and assigning it changes nothing. Whether it can is read from data, not from the category alone: its category is offered by the checkout (everything except `harbor_membership`, `slip_fee` and `broadcast`; a `membership` item can be an add-on), or an active QR poster sells it (a poster may use any price item), or an order line already has it. The editor hints "Bare faktura" only on an item that is none of these.
  - **Groups apply to past payments too.** The group is decided when the list is read, so moving an item or deleting a group reclassifies the payments already made; the confirm dialogs say so.
  - **A new price item is unassigned until someone assigns it**, and its payments show under Annet. The Payments page tells those who may assign (a count linking to the settings) and the settings page lists the unassigned items, but only active items that have been sold, i.e. an active QR poster sells them or an order line has them, so a club that leaves other service items ungrouped on purpose is not nagged; the picker still lists every price.
  - **A new group ranks above Faktura.** A group made on the settings page is placed just before the Faktura built-in (after the groups already above it), and the positions are renumbered 10, 20, ... in the same locked transaction, so it behaves like the groups the migration seeds: a faktura checkout with that group's add-on shows as that group. The up and down arrows can move it below Faktura.
  - **No fresh TOTP on the writes.** They change display configuration only, nothing about money, bookings or members; the precedent is `/admin/pricing` create, update and delete, which sit behind the step-up without a fresh gate. This is to be revisited in BRY-521: once alert rules route SMS by group, assigning an item decides who is told about a payment, and the rule writes (and perhaps item assignment) may need the fresh gate.
  - A deleted group's items become unassigned; whether BRY-521 rules that reference a group block its deletion is BRY-521's decision.
