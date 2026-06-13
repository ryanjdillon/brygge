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
manual   bank_import   payment_sync   invoice_sync   vipps
```

The `source` field tags how a journal entry was created. Filters in cash-flow / reconciliation logic depend on this (e.g. Vipps resync targets `source = 'vipps' AND status = 'draft'`).

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

- `paymentTypeAccountMap` (`backend/internal/accounting/sync.go`) — maps category → revenue GL code
- `vippsCategoryAccountMap` (`backend/internal/accounting/vippsreconcile.go`) — same shape, kept separate so Vipps-specific routing can evolve independently
- `categoryKeys` (`frontend/src/composables/usePricing.ts`) — maps category → `admin.pricing.category*` i18n key

Adding a new category requires touching **all three** plus this doc. Otherwise a faktura for that category routes to the default revenue code (3100) and the SPA shows the raw enum string.

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

---

## Mapping tables

### Category → revenue GL code

Source: `paymentTypeAccountMap` in `backend/internal/accounting/sync.go`.

| Key (payment_type or category) | GL code | Name (NS 4102) |
|---|---|---|
| `dues` (payment_type) | 3100 | Medlemskontingent |
| `membership` (price_items.category) | 3100 | Medlemskontingent — same account, different vocab |
| `harbor_membership` | 3110 | Havneavgift |
| `slip_fee` | 3120 | Plassleie |
| `booking` (payment_type) | 3200 | Gjestehavninntekter |
| `merchandise` | 3300 | Salgsinntekter |

Fallback when no entry matches: `defaultRevenueCode = "3100"` (Medlemskontingent).

### Vipps category → revenue GL code

Source: `vippsCategoryAccountMap` in `backend/internal/accounting/vippsreconcile.go`. Diverges from the table above because Vipps walk-up payments are mostly guest/motorhome, and we want them routed even when the category vocabulary differs slightly.

| `price_items.category` | GL code |
|---|---|
| `membership` | 3100 |
| `harbor_membership` | 3110 |
| `slip_fee` | 3120 |
| `seasonal_rental` | 3120 |
| `guest` | 3200 |
| `motorhome` | 3200 |
| `room_hire` | 3200 |
| `merchandise` | 3300 |

Fallback: `vippsFallbackRevenueCode = "3900"` (Andre inntekter). **Not 2900** — see `developer/invariants.md`.

### Fixed GL account constants

Source: `backend/internal/accounting/sync.go` + `vippsreconcile.go`.

| Constant | Code | Name |
|---|---|---|
| `bankAccountCode` | 1920 | Bankkonto drift |
| `receivablesAccountCode` | 1500 | Kundefordringer |
| `defaultRevenueCode` | 3100 | Medlemskontingent (fallback for invoice sync) |
| `vippsFallbackRevenueCode` | 3900 | Andre inntekter (fallback for Vipps cascade) |
| `vippsFeeAccountCode` | 7700 | Bankgebyrer |
| `vippsClearingAccountCode` | 2900 | Annen kortsiktig gjeld (legacy — kept as a constant for report compatibility, but **no new lines route here**) |

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
```

The reconciliation cascade groups `belastning` and `fee` rows by `booking_date` matching the payout row's date.

---

## What to do when

- **Adding a new `payment_type` enum value**: migration adds the value to the Postgres enum, update `paymentTypeAccountMap`, update this doc, update `internal/handlers/invoices.go` if the new type changes invoice flow.
- **Adding a new `price_items.category` string**: no migration needed, but you must update `paymentTypeAccountMap`, `vippsCategoryAccountMap`, `categoryKeys` (frontend), and this doc. Otherwise the new category routes to 3100 and the SPA shows the raw string.
- **Adding a new `resource_type`**: migration adds the value, then audit `frontend/src/views/BookView.vue` and any reservations chart filter for vocabulary mismatches (see the `bobil_spot` cautionary tale).
- **Adding a new `transaction_source`**: only audit-relevant filters in cash-flow and reconciliation depend on this; mostly safe.

When in doubt: `grep -r "<new-value>" backend/ frontend/` after the migration lands — every usage site should be intentional, not accidental.
