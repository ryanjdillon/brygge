# paymentfilters

The Payments page's filter groups (BRY-522, [PAY-15](../../../docs/adr/payments.md)) and the one rule that says which group an order is in. The overview query and the SMS alerts (BRY-521) both classify through this package; nothing else re-implements the rule.

## The rule

An order's group is the **enabled** group with the lowest `(sort_order, slug)` among

- the **item groups** (`builtin IS NULL`) that hold the price item of any of the order's **order lines**, and
- the `faktura` built-in, when the order has an `order_sources` row.

When nothing matches, the order is in the `other` built-in (Annet), whatever its `sort_order`. `other` is the fallback and is never matched.

| Case | Behaviour |
|---|---|
| Group disabled | Not a chip and skipped by the classifier; its orders fall to the next match or Annet. Its items stay assigned, so enabling it again restores it |
| Group deleted | Its items become unassigned; their orders fall to the next match or Annet. Past payments reclassify, because the group is decided when the list is read |
| Price item archived | Keeps its group, so past payments keep classifying |
| Price item hard-deleted | Possible only when nothing references it, so classification is unaffected; the assignment cascades. The price-item delete's reference count ignores groups on purpose |
| Order line with no price item | No item match: Faktura if it pays a faktura, else Annet |
| `faktura` built-in disabled | Orders that pay a faktura and match no item group go to Annet |
| Order that pays a faktura | Faktura, **unless one of its order lines holds an item of a group that ranks above Faktura** (a faktura checkout with an add-on). The classifier sees only `order_lines`; what a faktura bills (`invoice_lines` behind `order_sources`) never counts, so an item that can never be on an order line can never put an order in a group |
| Item that can be on an order line (`Reachable`) | Read from data: the category is offered by the checkout (`checkoutCatalogExcludedCategories` in the handlers lists the exceptions: `harbor_membership`, `slip_fee`, `broadcast`), **or** an active QR poster sells it (a poster may use any price item), **or** an order line already has it. A `membership` item can be a checkout add-on, so it is reachable. Only an item that is none of these is hinted "Bare faktura" |
| Item that is sold (`Sold`) | On an active QR poster or on an order line. Drives the nudge (`CountUnassigned`) and the settings page's unassigned panel: only active, sold items in no group count, so ungrouped items nobody sells do not nag |
| Club with no group rows | Reads synthesise the two built-ins (enabled, default label and icon); the classifier treats a missing faktura row as enabled and the fallback is the slug `other`. The first **write** creates the rows (`EnsureBuiltins`). A GET never writes |

A price item is in at most one group (`payment_filter_group_items.price_item_id` is the primary key). An order with items in several groups takes the one with the lowest position, which is why the settings page orders the groups. `Create` places a new group just before the faktura built-in and renumbers the positions 10, 20, ... in the locked transaction (`setOrder`, shared with `Reorder`), so it ranks above Faktura like the groups the migration seeds.

## Interface

| | |
|---|---|
| `OrderGroupLateralSQL` | A constant `LEFT JOIN LATERAL` fragment; needs an `orders o` alias in scope, yields `pfg.slug` and `pfg.group_id`. Select `COALESCE(pfg.slug, 'other')`. No group data is concatenated into it, so a caller binds only its own arguments (the overview binds `kind` as a `text[]`) |
| `ClassifyOrder(ctx, q, orderID)` | The group of one order, for the paid hook of BRY-521. **Call it; do not copy the rule.** Rules there should reference `payment_filter_groups.id` and need server-side labels for `label_key` rows (the labels are translations the SPA owns today) |
| `ListGroups`, `ListGroupItems`, `ListPriceItems(…, checkoutExcluded)`, `CountUnassigned` | Reads, synthesising missing built-ins. `ListPriceItems` takes the checkout-excluded categories from the caller (the handlers package owns the list) and derives `Reachable` and `Sold` |
| `Lock`, `EnsureBuiltins`, `Create`, `Update`, `SetItems`, `Reorder`, `Delete` | Writes, each on a `pgx.Tx`. They return `*Error` with a `Code` (`CodeOf`) for a refusal the handler maps to HTTP |
| `Slugify`, `IconAllowList`, `ValidIcon`, `NormalizeName` | Helpers. `NormalizeName` stores NFC, trimmed, and refuses control and Unicode format characters (zero-width spaces and joiners, bidi marks); `all`/`alle`/`tous`/`tutti`/`wszystkie` (the no-filter chip's names) and the slugs `all`, `alle`, `harbor`, `faktura`, `other` are reserved |

## Common misses

- **A write without the lock.** Every write function takes the club's advisory lock (`Lock`) and then `EnsureBuiltins`. A new write must too, or two admins interleave and a club without rows gets half its built-ins.
- **A move as delete then insert.** Use `ON CONFLICT (price_item_id) DO UPDATE`; with the lock held it cannot race, but the statement is also what keeps the primary key honest.
- **A new icon.** Add it to `iconAllowList` (`icons.go`) and to the `enum` tags of the OpenAPI types (`internal/openapi/payment_filters.go`; a test compares them), run `just api-types`, and map it in `frontend/src/lib/paymentFilterIcons.ts`: until you do, `vue-tsc` fails because the map is typed against the generated enum. The column needs no migration.
- **A new label key.** `label_key` is CHECKed (`pfg_label_key`), so a key needs a migration, a `recentPayments.kind.<key>` string in all 8 locales, an entry in `defaultLabels` (`store.go`, so a new group cannot be named like it) and the OpenAPI enum.
- **Defaults for a new club.** A club created after migration 000123 gets no item groups. Do not seed any: defaults come from the club's own price items (PLAT-1), as the migration did once, or from the board.
- **Naming a payments variable `groups`.** In `RecentPaymentsView` `groups` already means the date sections ("I dag", "Siste 7 dager"); the filter groups are `filterGroups`.

## Tests

`migration_test.go` re-executes migration 000123 over fixtures (the schema is migrated before any club exists) and checks the backfill, a second run and the down. The handler tests (`handlers/payment_filters_test.go`, `harbor_payments_test.go`) cover the classification equivalence with the old rule, the acceptance flow, validation, concurrency and the route gates.
