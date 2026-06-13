# Security docs

Cross-audience navigation: see [`../index.md`](../index.md).

| Document | Audience | Description |
|----------|----------|-------------|
| [2fa.md](2fa.md) | Board members + Operators | Two-factor authentication: enrollment, recovery codes, admin reset, all-admins-lost recovery, configurable step-up window |

Related:

- [`../developer/invariants.md`](../developer/invariants.md) — TOTP middleware ordering and gating invariants
- [`../../backend/internal/middleware/README.md`](../../backend/internal/middleware/README.md) — `RequireAdminTOTP`, `RequireFreshTOTP`, `IsFreshTOTP`
- [`../../frontend/src/views/admin/README.md`](../../frontend/src/views/admin/README.md) — click-time gating via `useNavGate`
