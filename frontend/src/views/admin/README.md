# `views/admin/` — admin SPA layout and nav conventions

`AdminLayout.vue` is the parent route for everything under `/admin/*`. It owns the sidebar, the per-route TOTP gating, and the responsive mobile menu. Every admin view mounts as `<RouterView>` inside it.

## Sidebar groups

Navigation lives in `navGroups` inside `AdminLayout.vue`. Each group is `{ titleKey, items[] }`; each item is `{ to, icon, label, roles?, feature? }`.

```ts
{
  titleKey: 'admin.groupEconomy',
  items: [
    { to: '/admin/accounting', icon: Calculator, label: t('admin.sidebar.accountingOverview'),
      feature: 'accounting' },
    { to: '/admin/accounting/faktura', icon: Receipt, label: t('admin.sidebar.faktura'),
      feature: 'accounting' },
    // …
  ],
},
```

"Payments" (`/admin/harbor/recent-payments`) and "QR posters" (`/admin/harbor/payment-targets`) sit first in the Economy group although their URLs keep the `harbor` prefix, so bookmarks and printed QR codes still resolve. They carry no `roles` and no `feature`, so every admin viewer, harbor master included, sees them even with the accounting flag off; do not give them the `accounting` flag of the items below. They are outside the `/admin/accounting` and `/admin/economy` prefixes, so `requiresFreshTotp` does not gate their sidebar clicks.

Filter logic at the bottom of the `<script setup>` block removes items whose `roles` the user lacks AND items whose `feature` flag is off. Empty groups disappear from the sidebar.

The bank-imports item's `badge` is the Tildel count plus the pending-refund count, the same two numbers `BankImportsView` puts on its Tildel and Refunds tabs. Adding them is right only because the server keeps the two disjoint (Tildel lists undismissed rows, a pending refund is a dismissed one); see "Tildel has one row set" in [`accounting/README.md`](../../../../backend/internal/accounting/README.md).

## Who sees what (BRY-372)

Every board role (`chair`, `vice_chair`, `treasurer`, `secretary`, `harbor_master`, `deputy`, `board`) and `admin` may **view** every admin page except Developer and Site. The rules live in [`../../lib/adminAccess.ts`](../../lib/adminAccess.ts) and mirror the server:

- **Router.** `/admin` carries `meta.requiresAdmin`; the guard (`guardRoute`) admits any admin viewer (`canViewAdmin`). Developer and Site pages — the whole "Nettsted" group: general, security, site and FAQ settings, sea-chart markers, notifications, GDPR, dev query, dev tokens — carry `meta.adminOnly` and open for `admin` only. A board member who follows a link there lands on `/admin`.
- **Sidebar.** Items outside "Nettsted" have no `roles`: anyone the router lets in sees them. "Nettsted" items carry `roles: ['admin']`. The inbox keeps its mailbox role list.
- **Write controls.** Viewing is not acting. A page a board member can open hides the controls they cannot use with `useCan()`:

  ```ts
  const { can } = useCan()
  const canEdit = can('money') // 'money' | 'ledger' | 'members' | 'harbor'
  ```

  Each area's roles (`WRITE_ROLES`) are the server's write roles for it: `money` is treasurer/admin (faktura, Tildel, imports, syncs, pricing, economy and bank settings, Vipps aliases), `ledger` is treasurer/board/admin (accounts, periods, Vipps message rules). Hide the button (`v-if`), or for a settings form wrap it in `<fieldset :disabled="!canEdit">`. The step-up prompt (`ensureFreshTotp()`) stays on the click for writers.
- **Covered so far:** faktura (create tab, row and bulk actions), bank imports (sync, uploads, Tildel and refund tabs, reassign/reimport), Vipps queue, economy settings, pricing, journal (periods, sync, new entry, post/void), chart of accounts. Other pages (users, slips, events, products, …) still show their write buttons to board members who lack the role; the server answers 403.

### TOTP-gated actions

An action button whose endpoint sits behind `RequireFreshTOTP` calls `ensureFreshTotp()` at click time, before the dialog opens (the server is the hard gate; the prompt is so the person re-verifies before filling in a form, not after).

- **GDPR deletion requests (BRY-509).** `GDPRAdminView`'s Process button (erases or anonymises the member, irreversible) calls `ensureFreshTotp()` on the click, then opens the confirm dialog, then posts through `totpAwareFetch` (`useProcessDeletion`), which re-prompts and replays if the window lapses in between. A cancelled step-up or a declined dialog sends nothing. The request list and the legal-document editor need only the 12-hour step-up.
- **Waiting list (BRY-477).** `WaitingListAdminView` talks to `/api/v1/admin/waiting-list` (list behind the 12-hour step-up, enroll / offer / reorder behind a fresh TOTP). "Add member" calls `ensureFreshTotp()` before it opens the search modal; a drag-reorder cannot pause mid-drag, so the drop is the click-time gate. Both writes go through `totpAwareFetch`, which re-prompts and replays if the window lapses while the modal is open.
- **Refund a card payment (BRY-497).** `RefundOrderButton` (payment overview row in `RecentPaymentsView`, faktura row in `FakturaList`) asks for the step-up on click and then builds `RefundOrderDialog`, which reads `GET /admin/orders/{orderID}/refunds` through `useOrderRefunds` and sends `POST /admin/orders/{orderID}/refund` through `useRefundOrder` (typed client, `composables/useOrderRefund.ts`). The server marks the rows to show it on (`can_refund` on a payment, `refund_order_id` on a faktura, only for a treasurer or admin); the view also hides it behind `can('money')`. The dialog makes its `request_key` when it opens and sends the same one on every retry, so a lost answer cannot refund twice; only a refund Stripe refused (`refund_failed`) makes a new key. See [`docs/developer/payments.md`](../../../../docs/developer/payments.md#refunds).
- **Refund and dispute chips, "Ny faktura" (BRY-498).** `RefundStateChip` (with `lib/refundState.ts`) is the one chip for a card payment's refund and dispute state, used by `RecentPaymentsView` rows and `FakturaList` rows so the two read alike (*Refundert*, *Delvis refundert 90 kr*, *Omstridt*, *Tvist tapt*, *Tvist vunnet*; sky, yellow, red, solid red, green, always with an icon and a label). Given an `href` (a Stripe Dashboard page the server sent a treasurer or admin) it is a link with a 44px tap area; the payment overview also shows a link to Stripe's list of disputes waiting for an answer while `open_disputes` > 0. `FakturaList` lists each refund credit note (*Kreditnota #N*, a PDF link) under the chips, and `ReissueAfterRefundButton` (shown when the server sets `can_reissue`) asks for the step-up on click, then a confirm, then `POST …/reissue-after-refund` through `useReissueAfterRefund` (typed client); the result is said in the list's notice (sent, left in Utkast, or PDF failed). The member portal (`InvoiceList`) uses its own status vocabulary (`refunded`, `refund_credit_note`) and a line under the faktura, never the staff chips. The faktura list is still a table that scrolls sideways below `lg`; the actions column is off-screen there, so "Ny faktura" also sits under the row's chips below `lg` (BRY-464).
- **Payment overview filters and dates (BRY-518, BRY-522).** The chips of `RecentPaymentsView` are the club's filter groups, as the server sends them in `groups` with the payments (enabled groups, display order, the fallback last); the view holds them as `filterGroups` (`groups` and `PaymentGroup` are the date sections, a different thing) and finds a group with `find`/`Map` by slug, never by indexing an object with it. The chips are additive: each pressed chip adds its group, `all` (no `kind` filter) is what is left when none is pressed and is the default. The ids are group slugs, kept as `?kind=a,b` in the URL and `recentPayments.kinds` on the device, so the old links and stored choices (seeded slugs equal the old kind ids) keep working; a stored `harbor` expands to `guest,motorhome`. After each answer the view drops the requested slugs that are no group (a deleted or disabled group, the old `merch`) and rewrites the URL and storage, falling back to `all`. Before the first answer only `Alle` shows, with placeholder chips of chip height so the layout does not jump; chips are `max-w-full truncate`, 44px. A row's icon and label come from its group (`PaymentGroupIcon`, `lib/paymentFilterIcons.ts`: `groupLabel` is the name as typed, or the translation of the seeded `label_key`); a slug the list does not have reads as the club's fallback group (its current name and icon, even renamed). The header has a `Filtre` link (every admin viewer) to the settings page, and those who may assign prices (`can('paymentFilters')`) get a line linking there when `unassigned_items` is above zero. Without a date range the list is the last 7 days (`since`) shown as `I dag` and `Siste 7 dager`; filling Fra or Til shows that range as one list with a clear button. Both ask for `limit=200`, the server maximum; when the server answers `has_more` the view says it shows only the newest payments. `until` includes its whole day (Fra = Til is one day). A failed load shows an error with a retry button instead of the empty-list message, above any rows kept from an earlier load.
- **Payment filter groups (BRY-522).** `PaymentFiltersView` (`/admin/harbor/recent-payments/filters`, route `admin-payment-filters`) is where the groups are set up. It has no sidebar item: it is reached from the `Filtre` link on the Payments page, which it links back to, and it sits outside the `/admin/accounting` and `/admin/economy` prefixes, so no navigation fresh-TOTP gate applies (the writes have none either, PAY-15). Every admin viewer opens it; `WRITE_ROLES.paymentFilters` (board, harbor_master, treasurer, admin, mirroring `handlers.PaymentFilterWriteRoles`) may change it, and for the others the list sits in a disabled `<fieldset>` with a note and no New, Edit or move controls. One card per group: icon, label, `N priser` (or what the built-in is), an enabled switch (not on the fallback), up and down buttons (44px, `aria-label`ed, disabled at the ends and while a reorder is in flight; a failed order is put back and the list re-read, and `order_mismatch` says someone else changed it), and Edit. "Ny gruppe" and Edit open `Modal` full screen below `md`: name (40 characters, "use the default name" for a seeded row), the icon grid (radios, 44px cells, names from `paymentFilters.icons.*`) and, for an item group, `MultiSelectList` of the club's prices (hint: category, Arkivert, "Kun faktura" for the categories no order line can reach, QR poster labels; meta: the group an item is in now). Ticking an item that is in another group moves it; "Alle" in the picker skips such items and the faktura-only ones, and save asks through `useConfirm()` with the moves listed. A name that another chip already shows in this language (seeded translations included) is refused before the request. Delete (item groups only) confirms with the number of prices that become unassigned. Below the list, the panel "Ikke i noen gruppe" names the active prices in no group that have been sold (`sold`: an active QR poster has them or a payment does), whose payments show under Annet; the Payments page's count is the same set, so a club that leaves other prices ungrouped on purpose is not nagged. The price count, the move confirm and the delete confirm are vue-i18n plurals (`t(key, {count}, count)`). `save()` and delete set `saving` before they open their confirm, so a second submit cannot start a second save. All of it uses `usePaymentFilterGroups` (typed client, plain state; the Payments page reloads its own copy on mount). Mobile: one column, controls wrap, nothing hover-only.
- **Provider links (Stripe, Vipps).** `RecentPaymentsView` shows a link per configured provider above the filters (`provider_links` in the `GET /harbor/recent-payments` answer, treasurer or admin only; `target="_blank" rel="noopener noreferrer"`, 44px tall, `recentPayments.providerLinks.*`). The URLs come from the server (`stripe.DashboardURL`; the Vipps one is the club's `clubs.vipps_portal_url`, set in `EconomySettingsView`) and are absent for an unconfigured provider, for an unset Vipps address, for the Vipps mock and in Vipps test mode; the view carries no URL. The settings field (own fieldset and Save button, `data-testid="vipps-portal-url"`) words the typed 400 `invalid_vipps_portal_url` next to the field and stacks below `md`. Local to the view for now (one consumer); extract it if another page needs the same pair. See [`docs/developer/payments.md`](../../../../docs/developer/payments.md#provider-links-on-the-payment-overview).

### Group titleKeys

Stable enums. Adding a new group means adding a new key here:

- `admin.groupHarbor`
- `admin.groupEconomy`
- `admin.groupShop`
- `admin.groupArchive`
- `admin.groupSite`

If you need a new group (say `admin.groupCommunity`), add the i18n key in `frontend/src/locales/{en,nb,nn}.json` first, then the group definition.

### Item shape

| Field | Required | Notes |
|---|---|---|
| `to` | yes | Router path. Used both as the link and as the key for active-route highlighting (`isActive(to)`). |
| `icon` | yes | Lucide-vue-next component. Import at the top of the file. |
| `label` | yes | Localized string (`t(...)`). |
| `roles` | no | Array of role names. If present, user needs at least one. Omitted = visible to anyone who can hit `/admin/*` at all. |
| `feature` | no | Feature flag key (e.g. `'accounting'`). Hidden when the flag is off. |

## TOTP gating at click time

Two layers, both via `useNavGate()`:

### Fresh-TOTP (10-min window) — economy nav items

Any path under `/admin/accounting/*` or `/admin/economy/*` intercepts the click via `handleNavClick`:

```vue
<RouterLink :to="item.to" @click="(e: MouseEvent) => handleNavClick(e, item.to)">
```

```ts
function requiresFreshTotp(path: string): boolean {
  return path.startsWith('/admin/accounting') || path.startsWith('/admin/economy')
}

async function handleNavClick(e: MouseEvent, to: string) {
  if (!requiresFreshTotp(to)) {
    closeSidebar()
    return
  }
  e.preventDefault()
  const ok = await gateToFresh(to)
  closeSidebar()
  if (ok) router.push(to)
}
```

When the path requires fresh TOTP, the click is prevented, `gateToFresh(path)` runs (modal prompt, redirect to `/portal/security?next=…` if unenrolled), and `router.push` only fires on success.

### Admin step-up (12-hour window) — NavBar "Admin" button

`NavBar.vue`'s `handleAdminClick` does the same shape but with `gateToAdmin('/admin')` instead. The 12h step-up modal opens if the user has TOTP enrolled but the window has lapsed; unenrolled users land on `/portal/security?next=/admin`.

## `useNavGate` composable

`frontend/src/composables/useNavGate.ts`. Exports two functions:

```ts
gateToFresh(path: string): Promise<boolean>  // 10-min fresh TOTP
gateToAdmin(path: string): Promise<boolean>  // 12-hour admin step-up
```

Both:

- Return `true` when the caller should proceed with the navigation
- Return `false` when the user cancelled OR was redirected for enrollment
- Redirect unenrolled users to `/portal/security?next={path}` and return `false`

Don't reinvent the gate in component-level `@click` handlers — that bypasses the enrollment-redirect path and produces inconsistent UX. Always use `useNavGate`.

## Adding a new TOTP-gated path prefix

If a new admin section needs fresh-TOTP at click time:

1. Extend `requiresFreshTotp(path)` in `AdminLayout.vue` with the new prefix
2. No other changes needed — sidebar items under the new prefix automatically gate

If a new top-level button (outside the sidebar, like the NavBar Admin entry) needs gating:

1. Add a `handleXClick` function calling `gateToFresh` or `gateToAdmin`
2. Wire it up: `@click="handleXClick"`

## Per-row action TOTP (separate from nav)

For sensitive in-page actions (send faktura, regen PDF, void, and every button that can book a posted GL entry: bank and Vipps upload, Synkroniser, Synk Vipps, Synk Stripe, Vipps payout confirm, Avstem mot Stripe-utbetaling (before its picker opens, BRY-465), the Stripe review card's Bokfør mot faktura / Bokfør mot konto / Avvis (before each dialog opens) and Angre avvisning (`StripeTransactionCard`, BRY-466), its Angre on a booked row (before the confirm dialog opens) and Bokfør fra ordren igjen on a held row (BRY-467), Avstem periode, journal post/void; see BRY-370 in the auth matrix; every Economy-settings save that changes what is billed or booked, such as role exemptions, BRY-351; and teaching or removing a Vipps-queue payer alias, BRY-383), use `useFreshTotp()`'s `ensureFreshTotp()` at click time:

```ts
import { useFreshTotp } from '@/composables/useFreshTotp'
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

async function sendFakturas() {
  if (!(await ensureFreshTotp())) return
  // proceed with the API call
}
```

For fetch calls that may 403 with `totp_fresh_required`, wrap with `totpAwareFetch` — it transparently re-prompts and retries:

```ts
const res = await totpAwareFetch('/api/v1/admin/...', { method: 'POST', body: ... })
```

This is orthogonal to nav gating. A single user flow can fire both — nav gate (enter the section) and per-row gate (perform the action) — without redundancy because each freshens the same window.

## Adding a new admin view

Per [`../../../../docs/developer/checklists/add-route.md`](../../../../docs/developer/checklists/add-route.md) for the backend route. For the frontend view:

1. Create `frontend/src/views/admin/XyzView.vue`
2. Register the route in `frontend/src/router/index.ts` under the admin layout's children
3. Add the sidebar item to `AdminLayout.vue` `navGroups` with appropriate `roles` + `feature`
4. If the path matches `/admin/accounting/*` or `/admin/economy/*`, fresh-TOTP gating is automatic. Otherwise, no gate at click time — backend role + TOTP middleware enforce at request time.
5. i18n: add `admin.sidebar.xyz` for the label, plus view-specific keys under `admin.xyz.*`

## Print views (`bare` routes)

A page meant to be printed, like the harbor QR card sheet (`/admin/harbor/payment-targets/print`, `HarborQrPrintView.vue`), cannot live under the admin layout: the sidebar would print, and the layout's padding would push fixed-size pages onto extra sheets. Register it as a **top-level** route next to `/admin/verify-totp`, with literal meta:

```ts
meta: { requiresAuth: true, requiresAdmin: true, bare: true }
```

- `requiresAdmin` must be spelled out: a top-level route doesn't inherit it from `/admin`.
- `bare: true` makes `App.vue` skip the navbar and footer.
- The page must load whatever NavBar normally loads for it, e.g. `useClubStore().ensureLoaded()` for the club name and logo.
- Inject any `@page` rule on mount and remove it on unmount. A plain global style from a lazy chunk stays in the document after you navigate away and changes how every other page prints.
- Hide screen-only controls with a scoped `@media print { display: none }`. A `print:hidden` toolbar still showed up in Chrome's print-to-PDF output for the QR sheet.

## Common misses

- **Sidebar item without `roles`** — visible to every board role, by design. A page that only site admins may open needs `roles: ['admin']` **and** `meta: { adminOnly: true }` on its route; one without the other leaves either a dead link or a reachable page.
- **Write button without `useCan()`** — on a page the board can view, a write control without a `useCan()` check is shown to members who will only get a 403. Pick the area whose roles match the route's server gate.
- **TOTP gate omitted for a new economy sub-path** — backend enforces it, but the SPA navigates first and bounces, which is confusing. Extend `requiresFreshTotp` prefix list.
- **`feature` flag forgotten** — the route shows up even when the module is disabled. Cross-reference [`add-feature-flag.md`](../../../../docs/developer/checklists/add-feature-flag.md).
- **A journal void error swallowed** — the server refuses to void an entry booked from a Stripe row (409 `stripe_entry`) or one a bank row or Stripe payout points at (409 `bank_entry`); `JournalEntriesView` shows the refusal with a deep link: `/admin/accounting/bank-imports?stripe=booked` (the Stripe list opens on Bokført and scrolls into view) or `?tab=reconcile` (Tildel). A new place that voids entries must handle the code the same way, not fail silently.
- **A refused charge undo or void with an undo of the clearing (BRY-555).** A Vipps charge whose settlement is already cleared is refused with 409 `charge_in_cleared_settlement` (body: `settlement_number`, `msn`, `bank_row_id`). `asClearedSettlementRefusal` (`useClearedSettlement.ts`) reads it and `ClearedSettlementRefusal.vue` renders the localized note with **Angre innbetalingen**: `ensureFreshTotp()` at click time, a confirmation listing what it undoes, then `unassignBankRow(bank_row_id)` (the deposit's Angre) and `undone`, on which the parent repeats what was refused. `AutoAssignSummary` (charge Angre) and `JournalEntriesView` (ledger void) use it; any new place that unbooks a Vipps charge or voids its bilag must too. The backlog release only handles drafts, which are never refused.
- **Component-level @click bypassing useNavGate** — works in isolation, breaks when the next change touches the gating policy. Always route through the composable.

## Related

- [`../../composables/useNavGate.ts`](../../composables/useNavGate.ts) — gate composable
- [`../../composables/useFreshTotp.ts`](../../composables/useFreshTotp.ts) — per-action TOTP helpers
- [`../../../../docs/developer/security/2fa.md`](../../../../docs/developer/security/2fa.md) — TOTP windows and the recovery flow
- [`../../../../docs/developer/reference/invariants.md`](../../../../docs/developer/reference/invariants.md) — TOTP middleware ordering
