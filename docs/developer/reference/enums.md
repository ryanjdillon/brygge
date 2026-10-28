# Enums and lookups reference

**One file, every Postgres enum, every Go iota set, every category vocabulary, and the bridges between them.** When two parts of the codebase disagree on what to call a thing — bookings UI says `bobil_spot` but `resources.type` says `motorhome_spot` — the bug surfaces here as a row labeled "distinct vocabularies." If you're an agent and you need a value, look here before inventing one.

**Sources of truth** (and where to edit when adding a new value):

- Postgres enums: `backend/migrations/*.up.sql` (numbered)
- Go constants: `backend/internal/<pkg>/*.go`
- Frontend vocab maps: `frontend/src/composables/usePricing.ts`, `frontend/src/composables/useAccountTypes.ts`, etc.

When you add a new enum value, edit the source first, then update this doc in the same commit. Drift to this doc shows up in PR review.

---

## Postgres enums

### `user_role` — `users.role`

```
applicant   member   slip_holder   board   harbor_master   treasurer   admin
```

Used by `middleware.RequireRole(...)`. Note: `applicant` and `member` are user-tier; `board`, `harbor_master`, `treasurer`, `admin` are admin-tier; `slip_holder` is a billing/assignment shorthand, not a permission level.

### `slip_status` — `slips.status`

```
vacant   occupied   reserved   maintenance
```

### `waiting_list_status` — `waiting_list.status`

```
active   offered   accepted   expired   withdrawn
```

### `payment_type` — `payments.type`

```
dues   harbor_membership   slip_fee   booking   merchandise
```

**`dues` is the payment-side name for annual membership.** The invoice-side `price_items.category` calls the same concept `membership`. The mapping table below resolves both.

### `payment_status` — `payments.status`

```
pending   completed   failed   refunded
```

### `resource_type` — `resources.type`

```
guest_slip   motorhome_spot   club_room   other   seasonal_rental   slip_hoist   shared_slip
```

⚠️ **Vocabulary collision**: the bookings UI uses `bobil_spot` as a *label* (`frontend/src/views/BookView.vue`, `useBookings`), but the underlying `resources.type` is `motorhome_spot`. SQL that filters `resources.type` must use `motorhome_spot`. This caused a 500 on `/admin/financials/reservations-by-month` until [DIL-369 hotfix `ae46117`].

### `booking_status` — `bookings.status`

```
pending   confirmed   cancelled   completed   no_show
```

### `event_tag` — `events.tag`

```
regatta   volunteer   social   agm   other
```

### `document_visibility` — `documents.visibility`

```
member   board
```

### `task_status` — `tasks.status`

```
todo   in_progress   done
```

### `task_priority` — `tasks.priority`

```
low   medium   high
```

### `feature_request_status` — `feature_requests.status`

```
proposed   reviewing   accepted   rejected   done
```

### `order_status` — `orders.status` (commerce)

```
pending   paid   failed   refunded
```

### `slip_assignment_type` — `slip_assignments.type` (migration 000012)

```
permanent   seasonal
```

### `journal_status` — `journal_entries.status` (accounting, migration 000006)

```
draft   posted   voided
```

**Invariant**: `posted` entries cannot be edited — only voided via `VoidJournalEntry`, which creates a balanced reversal entry. `voided` is terminal.

### `account_type` — `accounts.account_type`

```
asset   liability   revenue   expense
```

### `fiscal_period_status` — `fiscal_periods.status`

```
open   closed   locked
```

**Invariant**: `closed` periods are immutable per bokføringsloven §13. `resolvePeriod` enforces this; never bypass it.

### `transaction_source` — `journal_entries.source`

```
manual   bank_import   payment_sync   invoice_sync   vipps   invoice_resplit   opening_balance
```

The `source` field tags how a journal entry was created. `invoice_resplit` (migration 000101, BRY-364) marks a revenue reclassification posted against an invoice's `invoice_sync` entry; `source_id` is the invoice id. `opening_balance` (000107, BRY-370) marks the cutover entry carrying balances brought over from the previous system; a posted one debiting 1500 is what lets a booked credit note settle an imported faktura. Filters in cash-flow / reconciliation logic depend on this (e.g. Vipps resync targets `source = 'vipps' AND status = 'draft'`).

### `mva_eligibility` — `accounts.mva_eligible`, `journal_lines.mva_eligible`

```
eligible   ineligible   partial   not_applicable
```

---

## String "enums" (TEXT columns with informal vocabularies)

These are not Postgres enums but the codebase treats them as fixed sets. New values need synchronized updates across multiple files.

### `price_items.category`

```
membership   harbor_membership   slip_fee   seasonal_rental   guest   motorhome   room_hire   service   other
```

Synchronized vocabularies:

- `revenueAccountMap` (`backend/internal/accounting/revenue_accounts.go`) — maps category → revenue GL code for every payment path
- `PriceItemCategories` (`backend/internal/accounting/price_hints.go`) — the vocabulary the price-item handlers accept
- `PRICE_CATEGORIES` and its label keys (`frontend/src/lib/priceCategories.ts`) — the SPA vocabulary and its `admin.pricing.category*` i18n keys

Adding a new category requires touching **all three** plus this doc. Otherwise a faktura or Vipps charge for that category routes to the fallback revenue code (3900), Vipps auto-assign leaves it for review, and the SPA shows the raw enum string.

### `price_exemptions.role` and `invoice_lines.exemption_role` (migration 000108)

```
chair   vice_chair   treasurer   secretary   harbor_master   deputy   board
```

The board roles, defined once in `backend/internal/roles` (`roles.Board`) and validated there; a subset of `user_role`. Adding a board role to `user_role` means adding it to `roles.Board` (with its Norwegian faktura label) and to the `admin.users.role.*` i18n keys. `price_exemptions.category` uses the `price_items.category` vocabulary. Board-only documents (visibility `board`) are **not** gated on `roles.Board`: `roles.CanReadBoardDocuments` keeps the narrower set board, admin, harbor_master, treasurer until the owner decides whether the other board roles should read them.

### `invoices.status` (CHECK since migration 000102)

```
open   voided
```

Enforced by `invoices_status_check` (BRY-325). This column is **not** the payment state: whether an invoice is paid lives in `payment_id` (reconciled) and `online_paid_at` (captured via the pay link by the provider in `online_paid_provider`, awaiting settlement; `vipps_paid_at` is its legacy Vipps-only copy, still written until BRY-450 drops it). Ask "is it settled?" through `finance.SettledForMemberSQL` / `SettledForGLSQL`, never with ad-hoc checks. See the invariant "Settled means one thing, defined once".

### `invoices.recipient_kind`

```
private   organization
```

Drives the org-recipient block on the faktura PDF (`finance.OrgRecipient` is non-nil only for `organization`).

### `bank_imports.format`

```
sparebank-norge-v1   …
```

Bank format parsers live in `backend/internal/accounting/bankformats/`. Each format is a YAML file declaring CSV column positions, delimiter, date format, etc.

### `club_bank_accounts.role` (migration 000048)

```
drift   hoyrente   other
```

Semantic only — used by the operator to label accounts and (future) to pick the Vipps merchant settlement account. The faktura PDF picks an account by `is_default_for_invoices`, not by role.

### `developer_tokens.scope` (DIL-365, pending)

```
db_query   mcp
```

### `orders.provider` and `payment_attempts.provider` / `payment_refunds.provider` / `payment_webhook_events.provider` / `invoices.online_paid_provider`

```
vipps   stripe
```

The `payments.Provider` name (`vipps.ProviderName`, `stripe.ProviderName`). No CHECK: the vocabulary grows with the providers registered in `cmd/api/main.go`. `orders.provider` is the provider of the order's latest attempt, and of the attempt that paid it once paid.

### `payment_attempts.status` (CHECK since migration 000115)

```
created   authorized   captured   failed   cancelled
```

One payment created at a provider for an order (`handlers/order_pay.go`, `handlers/order_settle.go`). `created` on mint, before the provider is called → `authorized` when the provider reports an authorization Brygge will capture (back to `created` if the capture call fails) → `captured` when the money is taken (from any state: a capture means it was taken). `failed` when the provider refused to create it, or it was aborted, expired or terminated. `cancelled` when Brygge voided an authorization because the order was already paid or another attempt was already authorized or captured. Migration 000115 backfilled one attempt per order with a `vipps_reference`: paid or refunded → `captured`, failed → `failed`, else `created`.

### `payment_refunds.status` (CHECK since migration 000115)

```
requested   succeeded   failed
```

Nothing writes it yet; refunds land with BRY-446.

### `broadcasts.status` (migration 000069)

```
pending   complete
```

Lifecycle of a bulk send. Set to `pending` on enqueue; the delivery worker flips it to `complete` once no delivery remains `pending`/`sending`. Per-recipient failures stay visible on the delivery rows — the parent still completes. Constants in `backend/internal/broadcast/store.go` (`BroadcastPending`, `BroadcastComplete`).

### `broadcast_deliveries.status` (migration 000069)

```
pending   sending   sent   failed
```

Per-recipient delivery state. `pending` → claimed by the worker (`sending`) → `sent` or, after the retry cap, `failed`. A transient failure goes back to `pending` for reclaim. Constants in `backend/internal/broadcast/store.go` (`DeliveryPending`, `DeliverySending`, `DeliverySent`, `DeliveryFailed`).

---

## Mapping tables

### Category → revenue GL code

Source: `revenueAccountMap` in `backend/internal/accounting/revenue_accounts.go`. One map serves invoice sync, Vipps per-charge, Vipps auto-assign, the order split and the price hints.

| Key (payment_type or category) | GL code | Name (NS 4102) |
|---|---|---|
| `dues` (payment_type) | 3100 | Medlemskontingent |
| `membership` (price_items.category) | 3100 | Medlemskontingent — same account, different vocab |
| `harbor_membership` | 3110 | Havneavgift |
| `slip_fee` | 3120 | Plassleie |
| `electricity` | 3130 | Strøm |
| `booking` (payment_type) | 3200 | Gjestehavninntekter |
| `seasonal_rental` | 3120 | Plassleie (seasonal slip rent) |
| `guest` | 3200 | Gjestehavninntekter |
| `motorhome` | 3210 | Bobil/campingplass, kept apart from 3200 because it may be taxed differently under MVA |
| `room_hire` | 3220 | Lokalleie |
| `merchandise`, `service` | 3300 | Salgs- og tjenesteinntekter (seeded name; clubs seeded earlier keep "Salgsinntekter" until a treasurer renames it in Kontoplan) |
| `other` | 3900 | Andre inntekter |

Fallback when no entry matches: `fallbackRevenueCode = "3900"` (Andre inntekter), through `RevenueAccountOrFallback` on the paths that must book the money (invoice sync, including the residual, no-line and uncategorised-line cases; Vipps per-charge; order split). Invoice sync reads the category stored on each `invoice_lines` row at issue (`finance.LineCategorySQL`), falling back to the price item's current category only for a line that never stored one; every line with a price item stores it (migration 000111). `RevenueAccount` returns the code and whether the category is known; Vipps auto-assign and the price hints use it so an unknown category is never auto-booked or hinted. `TestRevenueAccountResolver` lists every key.

`PriceCategoryAccount(category)` resolves a price-item category (the ten keys in `PriceItemCategories`, the same set the price-item handlers accept) to its default account through `RevenueAccount`. Payment-type-only keys (`dues`, `booking`, `merchandise`) are not price-item categories and resolve to nothing. The accounts list (`GET /admin/accounting/accounts`) uses it to show, per account, the price items and categories that book there by default.

Every code in this table must exist in `DefaultKontoplan()`. `TestCategoryMapsResolveToDefaultKontoplan` enforces that. A code missing from the chart makes `CreateJournalEntry` fail with "account not found", and sync silently counts the invoice as skipped. That happened to 3130, 3210 and 3220 until migration `000099` (BRY-361).

### Fixed GL account constants

Source: `backend/internal/accounting/sync.go`, `revenue_accounts.go` and `vippsreconcile.go`.

| Constant | Code | Name |
|---|---|---|
| `bankAccountCode` | 1920 | Bankkonto drift |
| `receivablesAccountCode` | 1500 | Kundefordringer |
| `fallbackRevenueCode` | 3900 | Andre inntekter (fallback for every payment path; **not 2900**, see `invariants.md`) |
| `vippsFeeAccountCode` | 7700 | Bankgebyrer — default of `clubs.payment_fee_account`, which books fees for every provider |
| `vippsClearingAccountCode` | 2900 | Annen kortsiktig gjeld (legacy — kept as a constant for report compatibility, but **no new lines route here**) |

`clubs.stripe_clearing_account` defaults to **1580** Til gode betalingsformidler (Stripe), an asset in `DefaultKontoplan()` that migration 000114 seeded into every existing chart that lacked it. It holds card payments until Stripe pays them out; nothing posts to it until BRY-445.

### Price-item unit → i18n key

Source: `unitKeys` in `frontend/src/composables/usePricing.ts`.

| Unit | i18n key |
|---|---|
| `once` | `admin.pricing.unitOnce` |
| `year` | `admin.pricing.unitYear` |
| `season` | `admin.pricing.unitSeason` |
| `day` | `admin.pricing.unitDay` |
| `night` | `admin.pricing.unitNight` |
| `hour` | `admin.pricing.unitHour` |

Unknown units fall back to literal `/${unit}` so a new unit-string surfaces visibly in the UI.

### Vipps row types (CSV column `row_type`)

Source: `VippsRowType` constants in `backend/internal/accounting/vippsimport.go`.

```
payout      The "Utbetaling planlagt" row — carries settlement_number
belastning  Customer charge — amount > 0, customer_name + message set
fee         Vipps gebyr — amount < 0
other       Anything else (refunds, reversals) — left for manual review
```

`vipps_import_rows.auto_assign_rule` (CHECK, migration 000103, BRY-340) records which auto-assign rule booked a charge: `member_invoice` or `price_item`. NULL means a human booked it, or it is unbooked.

The reconciliation cascade groups `belastning` and `fee` rows by `booking_date` matching the payout row's date.

Report API rows (`vipps_imports.filename` "Vipps Report API …") map onto the same vocabulary through `classifyReportFunds`. A `capture` becomes `belastning`, the settlement payout (reference `Utb. <n> Vippsnr <msn>`) becomes `payout` with its settlement number, and the fees retained from the settlement become the per-settlement `fee` row (negative amount). The fees report's per-charge lines are also `fee` rows, but they carry their value in `fee` with `amount` 0, so they don't double-count. Report rows imported as `other` before BRY-337 are reclassified by `RepairVippsReportRows`. `message` holds the entry type for Report rows only, and the payer's free text for CSV rows, which is why the repair never touches CSV rows.

---

## What to do when

- **Adding a new `payment_type` enum value**: migration adds the value to the Postgres enum, update `revenueAccountMap`, update this doc, update `internal/handlers/invoices.go` if the new type changes invoice flow.
- **Adding a new `price_items.category` string**: no migration needed, but you must update `revenueAccountMap`, `PriceItemCategories`, the handlers' `validCategories`, `frontend/src/lib/priceCategories.ts`, `TestRevenueAccountResolver`, and this doc. Otherwise the new category routes to 3900 and the SPA shows the raw string.
- **Adding a new `resource_type`**: migration adds the value, then audit `frontend/src/views/BookView.vue` and any reservations chart filter for vocabulary mismatches (see the `bobil_spot` cautionary tale).
- **Adding a new `transaction_source`**: only audit-relevant filters in cash-flow and reconciliation depend on this; mostly safe.

When in doubt: `grep -r "<new-value>" backend/ frontend/` after the migration lands — every usage site should be intentional, not accidental.
