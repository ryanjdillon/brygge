# Developer docs

Audience: deployers, contributors, and anyone troubleshooting at the OS / protocol / database layer. Cross-audience navigation: see [`../index.md`](../index.md).

## Getting up and running

| Document | Description |
|----------|-------------|
| [quickstart.md](quickstart.md) | Local dev environment in five minutes |
| [setup.md](setup.md) | Single-VPS deployment walkthrough (DNS, SSH, Hetzner) |
| [deploy.md](deploy.md) | Production deploy: Nix flake, deploy-rs, magic-rollback |
| [k8s.md](k8s.md) | Kubernetes migration notes for scaling beyond a single VPS |

## Configuration & data

| Document | Description |
|----------|-------------|
| [configuration.md](configuration.md) | Environment variables, feature flags, env-layer rules |
| [database.md](database.md) | Postgres ops: connecting, scripts, migrations, backups |
| [enums.md](enums.md) | Postgres enums, TEXT vocabularies, payment / Vipps category → GL maps |

## Cross-cutting invariants & audit

| Document | Description |
|----------|-------------|
| [invariants.md](invariants.md) | Append-only list of rules other code must respect (auth, accounting, invoicing, sessions, config) |
| [audit-actions.md](audit-actions.md) | Every `audit.Action*` constant: string, resource, when, `extra` fields |

## Change checklists

[checklists/](checklists/) — per-change-type recipes. Start here when adding a route, migration, bulk action, audit action, or feature flag. See [checklists/README.md](checklists/README.md) for the full index.

## Troubleshooting & recovery

| Document | Description |
|----------|-------------|
| [troubleshooting.md](troubleshooting.md) | Common dev + deploy issues |
| [rescue-recover-ssh-access.md](rescue-recover-ssh-access.md) | Recovering SSH access when locked out |

## Subsystem READMEs (colocated with code)

Backend internals carry their own `README.md` next to the source — load when you're working in that package:

- [`backend/internal/accounting/README.md`](../../backend/internal/accounting/README.md) — Vipps cascade, KID matching, GL constants
- [`backend/internal/handlers/README.md`](../../backend/internal/handlers/README.md) — handler struct convention, response shapes, audit pattern, TOTP gating
- [`backend/internal/email/README.md`](../../backend/internal/email/README.md) — template pattern, `formatNOK`, locale detection
- [`backend/internal/middleware/README.md`](../../backend/internal/middleware/README.md) — middleware ordering, TOTP gating flavors, browser-navigation detection
- [`backend/internal/finance/README.md`](../../backend/internal/finance/README.md) — invoice data contract, KID generation, Norwegian formatting

Frontend:

- [`frontend/src/views/admin/README.md`](../../frontend/src/views/admin/README.md) — admin sidebar groups, click-time TOTP gating, `useNavGate`
