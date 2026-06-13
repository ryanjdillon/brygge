# Adding a feature flag

Five places to touch. The env-var → DB-column → frontend-toggle chain is what makes the flag actually toggleable at runtime.

Read [`../configuration.md`](../configuration.md) Feature Flags section first — there's an asymmetry with the accounting flag that's important to understand.

## 1. Go config

`backend/internal/config/config.go`:

```go
type Features struct {
    Bookings       bool
    // … existing …
    NewModule      bool
}

// In Load():
NewModule: envBool("FEATURE_NEW_MODULE", true),
```

Default `true` matches existing convention — clubs opt out, not in. If the new module is risky (e.g. handles payments, touches PII at new boundaries), default `false` is reasonable.

## 2. Migration: per-club DB column

New migration adds `feature_<name>` to the `clubs` table:

```sql
ALTER TABLE clubs
  ADD COLUMN feature_new_module BOOLEAN NOT NULL DEFAULT TRUE;
```

The DEFAULT matches your env-var default so existing clubs don't suddenly lose access on migration apply.

## 3. Features handler

`backend/internal/handlers/features.go` reads the per-club row with env-var fallback:

```go
case "new_module":
    if dbVal, ok := dbFeatures["new_module"]; ok {
        features["new_module"] = dbVal
    } else {
        features["new_module"] = h.config.Features.NewModule
    }
```

The DB value beats the env value when present. Lookup fallback handles fresh clubs that haven't been backfilled.

## 4. Route gating in `main.go`

Most flags only gate UI visibility (the frontend hides the entry, but the routes still exist server-side). For most modules, do nothing in `main.go` — the SPA hides what it shouldn't show, and direct API access returns empty data when the module is off.

⚠️ **The accounting flag is asymmetric** — it gates **route registration** in `main.go`:

```go
if cfg.Features.Accounting {
    r.Route("/accounting", ...)
}
```

This means a deploy with `FEATURE_ACCOUNTING=false` makes the routes literally not exist, and the DB toggle can't bring them back. If your new flag has the same property, document it in [`../configuration.md`](../configuration.md).

For most new flags, prefer UI-only gating — it makes the DB toggle fully bidirectional.

## 5. Frontend toggle

Add to `frontend/src/views/admin/SiteSettingsView.vue` Modules section:

```vue
<Switch
  v-model="features.new_module"
  :label="t('admin.siteSettings.modules.newModule')"
/>
```

Add the i18n key in `frontend/src/locales/{en,nb,nn}.json` under `admin.siteSettings.modules.*`.

The `features` reactive object is populated from `/api/v1/features` on mount and pushed back via `/api/v1/admin/settings/site` PATCH on save. The wire is already there — you just consume it.

## 6. Sidebar visibility

If the new module adds nav items, gate them by the flag in `frontend/src/views/admin/AdminLayout.vue`:

```ts
{ to: '/admin/new-module', icon: SomeIcon, label: t('admin.sidebar.newModule'),
  roles: ['admin'], feature: 'new_module' },
```

The filter at the bottom of the script setup automatically removes items whose `feature` is off.

## 7. Docs

Update [`../configuration.md`](../configuration.md) Feature Flags table with the new env var. If the flag has the route-registration asymmetry, document it in the same callout that mentions the accounting flag.

Update [`../database.md`](../database.md) Schema overview to mention the new `clubs.feature_<name>` column.

## Common misses

- **Forgot the DB column migration** — the env var still works, but the per-club override doesn't. Operators can't toggle from the UI.
- **Forgot the SPA wire-up** — the toggle saves to the DB but doesn't actually do anything because the frontend doesn't gate on the flag.
- **Inverted default** — env default `true`, migration default `false`. Existing clubs lose access on apply.
- **Route registration when you didn't mean to** — accidentally copied the accounting `if cfg.Features.X` block. Now your flag has the asymmetry and you didn't document it.

## Testing

The features handler test (`features_test.go`) needs a row added for any new flag. The test signature constructs a `Features{}` literal — add your field there.

Manual smoke test: toggle the flag from `Admin → Site → Site content → Modules`, save, refresh. The nav entries should appear/disappear immediately (the `/features` query is invalidated after save).
