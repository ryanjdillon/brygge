<p align="center">
  <img src="docs/assets/logo.svg" alt="Brygge" width="120" />
</p>

<h1 align="center">Brygge</h1>

<p align="center">
  Self-hosted management for harbour clubs, boat associations, and small marinas.
</p>

<p align="center">
  <a href="https://github.com/ryanjdillon/brygge/actions/workflows/ci.yml">
    <img src="https://github.com/ryanjdillon/brygge/actions/workflows/ci.yml/badge.svg" alt="CI" />
  </a>
</p>

---

*Brygge* is Norwegian for "dock". It runs the unglamorous side of a club — the membership roll, the waiting list, the slip assignments, the invoices, the dugnad sign-up sheet — so volunteer boards can spend less time in spreadsheets and more time on the water.

It's one Go binary with an embedded Vue SPA, backed by Postgres and Redis. A single Hetzner CAX11 (ARM64, 2 vCPU, 4 GB) runs the whole thing comfortably for a few hundred members.

## Get started

- **Run it locally** → [docs/quickstart.md](docs/quickstart.md)
- **Deploy it** → [docs/deploy.md](docs/deploy.md)
- **Set it up as a club admin** → [docs/setup.md](docs/setup.md)

## What's inside

A guided tour of the architecture lives in [docs/architecture.md](docs/architecture.md). The short version: member portal, slip + waiting list management, faktura/KID invoicing, bookings (guest slips, hoist, rooms), an integrated Matrix-powered forum, dugnad tracking, harbour map, and a calendar — all gated by feature flags so you only run what you use.

The pieces that hold it together are listed in [docs/tech-stack.md](docs/tech-stack.md).

## Contributing

I'd genuinely love help. Whether it's a translation, a bug report, a small fix, or a whole new module — clubs everywhere have weirdly specific needs and the only way Brygge gets better is if more of them get represented in the code. See [CONTRIBUTING.md](CONTRIBUTING.md) for how the dev loop works.

If you're running Brygge for your own club and want to share what you've changed, that's the most welcome contribution of all.

## Documentation

Everything else is in [docs/](docs/index.md) — deployment, configuration, mail, observability, 2FA, troubleshooting, and the Kubernetes notes for when one VPS isn't enough.

## Status

Used in production. The roadmap and known gaps live in [TODO.md](TODO.md).

## License

MIT.
