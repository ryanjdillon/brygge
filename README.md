<h1 align="center">Brygge</h1>

<p align="center">
  Open-source harbour club management platform
</p>

<p align="center">
  <a href="#features">Features</a> &middot;
  <a href="#quick-start">Quick Start</a> &middot;
  <a href="docs/index.md">Documentation</a> &middot;
  <a href="docs/deploy.md">Deploy</a> &middot;
  <a href="docs/contributing.md">Contributing</a>
</p>

<p align="center">
  <a href="https://github.com/brygge-klubb/brygge/actions/workflows/ci.yml">
    <img src="https://github.com/brygge-klubb/brygge/actions/workflows/ci.yml/badge.svg" alt="CI" />
  </a>
</p>

---

Brygge (Norwegian for *dock*) is a self-hosted management platform for harbour clubs, boat associations, and marina cooperatives. It ships as a single Docker container with an embedded SPA frontend, backed by PostgreSQL and Redis.

## Features

| Area | Highlights |
|------|-----------|
| **Member portal** | Profile, boat registry, slip management, waiting list, document archive, directory, GDPR data export & deletion |
| **Bookings** | Guest slips, motorhome spots, club rooms, hoist scheduling with calendar availability |
| **Finances** | Dues & invoices, Vipps payments, merchandise shop, overdue tracking, financial reports (CSV export) |
| **Communication** | Broadcast email, web push notifications, integrated forum (Matrix/Dendrite) |
| **Projects** | Dugnad (working day) tracking, task boards with kanban view, shopping lists |
| **Calendar** | Events with iCal export, RSVP, regatta & social event management |
| **Admin** | Role-based access (7 roles), audit log, slip & waiting list management, feature flags |
| **Harbour map** | Interactive MapLibre GL map with configurable markers and harbour chart overlay |
| **i18n** | 7 languages: Norwegian, English, German, French, Italian, Dutch, Polish |

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25, chi/v5, pgx/v5, Redis 7, Huma (OpenAPI 3.1) |
| Frontend | Vue 3.5, TanStack Query, Pinia, Shadcn-vue, TailwindCSS 4, MapLibre GL |
| API client | openapi-fetch with generated TypeScript types |
| Auth | JWT + Vipps Login (Norwegian payment provider) |
| Database | PostgreSQL 16 (raw SQL + sqlc code generation) |
| Infrastructure | Docker Compose, Traefik v3.3 (TLS via Let's Encrypt), Distroless runtime |
| CI | GitHub Actions (lint, test, security scan, build, deploy) |

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) with Compose plugin
- [Nix](https://nixos.org/) (recommended) or Go 1.25+, Node.js 22+, and [just](https://just.systems/)

### Development

```bash
git clone https://github.com/brygge-klubb/brygge.git
cd brygge

# Enter the Nix dev shell (provides all tools at pinned versions)
nix develop

# Install frontend dependencies
cd frontend && npm install && cd ..

# Start PostgreSQL, Redis, Go API (with hot reload), and Vite dev server
just up
```

The app is available at `http://localhost:5173` (frontend) and `http://localhost:8080` (API).

### Running Tests

```bash
just test-go            # Go unit tests
just test-go-integration # Go tests with real DB/Redis
just test-vue           # Vitest component tests
just test-e2e           # Playwright E2E tests
just lint               # golangci-lint + ESLint
```

### Production Build

```bash
just build              # Go binary with embedded SPA
just docker-build       # Docker image (ARM64)
```

## Architecture

```
                    ┌─────────────┐
                    │   Traefik   │  TLS termination, routing
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
        ┌─────┴─────┐ ┌───┴───┐ ┌─────┴─────┐
        │  Go API   │ │Element│ │  Dendrite  │
        │+ embedded │ │  Web  │ │  (Matrix)  │
        │   SPA     │ └───────┘ └────────────┘
        └─────┬─────┘
              │
      ┌───────┼───────┐
      │               │
┌─────┴─────┐  ┌──────┴──────┐
│ PostgreSQL │  │    Redis    │
│     16     │  │      7      │
└────────────┘  └─────────────┘
```

The Go binary embeds the Vue production build via `go:embed` and serves both the API (`/api/v1/*`) and SPA from a single process. For Kubernetes deployments, a `noembed` build tag separates the frontend. See [docs/k8s.md](docs/k8s.md).

## Configuration

Copy `deploy/.env.example` to `deploy/.env` and configure:

| Variable | Purpose |
|----------|---------|
| `DOMAIN` | Your club's domain |
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `JWT_SECRET` | Token signing secret |
| `VIPPS_*` | Vipps payment & login credentials |
| `S3_*` | Object storage for documents |
| `RESEND_API_KEY` | Transactional email |
| `FEATURE_*` | Toggle feature modules (bookings, projects, calendar, commerce, communications) |

See [deploy/.env.example](deploy/.env.example) for the full list with inline documentation.

## Deployment

Brygge runs on a single VPS with Docker Compose. The recommended target is a **Hetzner CAX11** (ARM64, 2 vCPU, 4 GB RAM).

See the [deployment guide](docs/deploy.md) for step-by-step instructions, including provider-specific guides for Hetzner.

```bash
# Deploy to production
./scripts/brygge.sh setup    # First time
./scripts/brygge.sh update   # Subsequent updates
```

## Documentation

Full documentation is in the [`docs/`](docs/index.md) directory:

- [Documentation index](docs/index.md)
- [Deployment guide](docs/deploy.md) with provider-specific instructions
- [Setup guide](docs/setup.md) for non-developer club administrators
- [Contributing guide](docs/contributing.md) for developers
- [Kubernetes migration](docs/k8s.md) for scaling beyond a single server

## Project Status

Brygge is in active development and used in production. See [TODO.md](TODO.md) for the roadmap.
