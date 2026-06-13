# Change checklists

Short, explicit lists of "when you change X, you must also change Y." Skipping a step rarely fails fast — it bites in the next session or the next release.

Each checklist is small enough to load at the top of an agent's context for the relevant task. Cross-references between checklists keep composition cheap.

## Available checklists

- [`add-route.md`](add-route.md) — register a new HTTP route end-to-end
- [`add-bulk-action.md`](add-bulk-action.md) — `POST {ids: []}` style operations with skipped/failures tracking
- [`add-migration.md`](add-migration.md) — Postgres schema change with docs sync
- [`add-audit-action.md`](add-audit-action.md) — new entry in the audit log
- [`add-feature-flag.md`](add-feature-flag.md) — env var + DB column + UI toggle for a new module

## Companion docs

- [`../enums.md`](../enums.md) — when the change touches an enum value
- [`../invariants.md`](../invariants.md) — when the change brushes against an invariant
- [`../audit-actions.md`](../audit-actions.md) — when adding an action to the audit log
- [`../database.md`](../database.md) — when adding a table or column

## When in doubt

Run the checklist even if it feels like overkill. The 30 seconds it takes to verify each step is cheaper than the next session realizing the i18n key was missing or the migration broke staging.
