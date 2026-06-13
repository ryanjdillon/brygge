# Mail docs — self-hosted Stalwart + Bulwark

Cross-audience navigation: see [`../../index.md`](../../index.md).

| Document | Audience | Description |
|----------|----------|-------------|
| [setup.md](setup.md) | Operators | Initial deploy, DKIM provisioning, role mailboxes, deliverability, day-2 ops |
| [inbox.md](inbox.md) | Operators + Developers | Role-gated shared inbox at `/admin/inbox`: spec format, reconciler, per-user provisioning, send path, verification recipes |
| [stalwart-internals.md](stalwart-internals.md) | Developers | Stalwart 0.15 protocol quirks (admin REST, JMAP, password hashing). Reference for when something at the protocol layer breaks. |
| [bimi.md](bimi.md) | Operators | BIMI: publishing the club logo so it renders next to outbound mail |

Related:

- [`../../../backend/internal/email/README.md`](../../../backend/internal/email/README.md) — outbound senders + Norwegian-localized templates
