# `views/admin/` — admin SPA layout and nav conventions

`AdminLayout.vue` is the parent route for everything under `/admin/*`. It owns the sidebar, the per-route TOTP gating, and the responsive mobile menu. Every admin view mounts as `<RouterView>` inside it.

## Sidebar groups

Navigation lives in `navGroups` inside `AdminLayout.vue`. Each group is `{ titleKey, items[] }`; each item is `{ to, icon, label, roles?, feature? }`.

```ts
{
  titleKey: 'admin.groupEconomy',
  items: [
    { to: '/admin/accounting', icon: Calculator, label: t('admin.sidebar.accountingOverview'),
      roles: ['treasurer', 'board', 'admin'], feature: 'accounting' },
    { to: '/admin/accounting/faktura', icon: Receipt, label: t('admin.sidebar.faktura'),
      roles: ['treasurer', 'admin'], feature: 'accounting' },
    // …
  ],
},
```

Filter logic at the bottom of the `<script setup>` block removes items whose `roles` the user lacks AND items whose `feature` flag is off. Empty groups disappear from the sidebar.

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

For sensitive in-page actions (send faktura, regen PDF, void), use `useFreshTotp()`'s `ensureFreshTotp()` at click time:

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

## Common misses

- **Sidebar item without `roles`** — visible to everyone who reaches `/admin/*`, including bare members if they have any admin-adjacent role. Almost always wrong; add the explicit role list.
- **TOTP gate omitted for a new economy sub-path** — backend enforces it, but the SPA navigates first and bounces, which is confusing. Extend `requiresFreshTotp` prefix list.
- **`feature` flag forgotten** — the route shows up even when the module is disabled. Cross-reference [`add-feature-flag.md`](../../../../docs/developer/checklists/add-feature-flag.md).
- **Component-level @click bypassing useNavGate** — works in isolation, breaks when the next change touches the gating policy. Always route through the composable.

## Related

- [`../../composables/useNavGate.ts`](../../composables/useNavGate.ts) — gate composable
- [`../../composables/useFreshTotp.ts`](../../composables/useFreshTotp.ts) — per-action TOTP helpers
- [`../../../../docs/developer/security/2fa.md`](../../../../docs/developer/security/2fa.md) — TOTP windows and the recovery flow
- [`../../../../docs/developer/reference/invariants.md`](../../../../docs/developer/reference/invariants.md) — TOTP middleware ordering
