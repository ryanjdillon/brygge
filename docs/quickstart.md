# Quickstart

Spin up a local Brygge stack for development.

## Prerequisites

- Docker (with the Compose plugin)
- One of:
  - [Nix](https://nixos.org/) with flakes enabled (provides every other tool at the right version)
  - Or, manually: Go 1.25+, Node.js 22+, [`just`](https://just.systems/)

## First run

```bash
git clone https://github.com/ryanjdillon/brygge.git
cd brygge

# All tools at pinned versions
nix develop

cd frontend && npm install && cd ..

# Postgres + Redis + Go API (hot reload) + Vite dev server
just up
```

When it settles you'll have:

- `http://localhost:5173` — Vue dev server
- `http://localhost:8080` — Go API
- `http://localhost:8025` — MailPit (catches every magic link, faktura PDF, broadcast — nothing leaves the host in dev)

## Useful commands

```bash
just test              # everything
just test-go           # Go unit tests
just test-go-integration  # against real Postgres/Redis
just test-vue          # Vitest
just test-e2e          # Playwright

just lint              # golangci-lint + ESLint
just fmt               # format both stacks

just migrate           # apply pending migrations
just generate          # regenerate sqlc Go from queries
just api-types         # regenerate frontend types from OpenAPI

just build             # production binary with embedded SPA
just docker-build      # ARM64 image
```

## Where things live

- `backend/` — Go API, migrations, sqlc queries
- `frontend/` — Vue 3 SPA
- `deploy/` — Compose files + Traefik config
- `scripts/brygge.sh` — deploy CLI used in production
- `docs/` — everything you're reading

For the deeper tour see [architecture.md](architecture.md) and [contributing](../CONTRIBUTING.md).
