<p align="center">
  <img src="docs/assets/logo.svg" alt="Brygge" width="120" />
</p>

<h1 align="center">Brygge</h1>

<p align="center">
  Self-hosted management software for harbour clubs, boat associations, and small marinas.
</p>

<p align="center">
  <a href="https://github.com/ryanjdillon/brygge/actions/workflows/ci.yml">
    <img src="https://github.com/ryanjdillon/brygge/actions/workflows/ci.yml/badge.svg" alt="CI" />
  </a>
</p>

---

Brygge runs the administrative side of a club — the membership roll, the waiting list, slip assignments, invoicing, the volunteer-day sign-up, the harbour map, the calendar, and an integrated members' forum. It replaces the spreadsheets, the shared mailbox, and the manual bank reconciliation that volunteer boards juggle every season.

Three goals shape the project:

- **Low running cost.** The whole platform — app, database, mail server, forum — runs on one small VPS for roughly the price of a coffee per month. There is no per-member pricing and no SaaS subscription.
- **Fully integrated operations.** Membership, billing, payments, mail, and the forum are one system, not five tools stitched together. An invoice knows who it is for; a bank payment reconciles itself against it; a board mailbox is readable by whoever currently holds the role.
- **An intuitive interface.** Board members are volunteers, not operators. Day-to-day tasks — sending invoices, approving a slip, posting an announcement — are meant to be obvious without training.

It is one Go binary with an embedded web app, backed by PostgreSQL and Redis. Every feature is behind a flag, so a club runs only what it needs.

## Screenshots

<p align="center">
  <img src="docs/assets/screenshots/dashboard.png" alt="Admin dashboard" width="48%" />
  <img src="docs/assets/screenshots/faktura.png" alt="Invoicing" width="48%" />
</p>
<p align="center">
  <img src="docs/assets/screenshots/harbor-map.png" alt="Harbour map" width="48%" />
  <img src="docs/assets/screenshots/inbox.png" alt="Shared inbox" width="48%" />
</p>

## What it does

- **Members & slips** — member roll, boat register, slip assignments, waiting list, GDPR export and deletion.
- **Billing** — Norwegian faktura with KID, bulk invoicing, automatic bank and Vipps reconciliation, accounting reports.
- **Payments** — Vipps for dues and bookings, overdue tracking.
- **Mail** — a self-hosted mail server; role mailboxes (treasurer, harbour master, …) are readable from the admin portal by whoever holds the role.
- **Communications** — member broadcasts, web push, and an integrated Matrix-powered forum.
- **Operations** — bookings for guest slips and the hoist, volunteer-day tracking, an interactive harbour map, and a club calendar.

## Documentation

### Users

For club admins, board members, and members operating the running system.

- [Invoicing guide](docs/user/faktura.md)

### Developers

For deploying, contributing, and low-level operations.

- [Quickstart](docs/developer/quickstart.md)
- [Deployment](docs/developer/deploy.md)
- [Server setup](docs/developer/setup.md)
- [Configuration](docs/developer/configuration.md)
- [Database operations](docs/developer/database.md)
- [Troubleshooting](docs/developer/troubleshooting.md)
- [SSH recovery](docs/developer/rescue-recover-ssh-access.md)
- [Kubernetes notes](docs/developer/k8s.md)
- [Architecture](docs/architecture.md)
- [Technology stack](docs/tech-stack.md)
- [Mail server](docs/mail/setup.md)
- [Shared inbox](docs/mail/inbox.md)
- [Observability](docs/otel/index.md)
- [Two-factor authentication](docs/security/2fa.md)
- [Contributing](CONTRIBUTING.md)

## Contributing

Help is welcome — translations, bug reports, fixes, or whole new modules. Clubs have specific needs, and the project improves as more of them are represented in the code. If you run Brygge for your own club, sharing what you changed is the most useful contribution of all. See the contributing guide above for the development workflow.

## Status

Alpha. Brygge runs in production at a live club but is under active development; interfaces and data shapes can still change between releases. Bug reports and input are encouraged.
