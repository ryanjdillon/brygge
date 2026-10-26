# Contributing to Brygge

Thank you for your interest in contributing to Brygge. This guide covers the development environment, project structure, and workflow for making changes.

---

## 1. Prerequisites

### Recommended: Nix

The repository includes a Nix flake that provides all required tooling at pinned versions. This is the easiest way to get started and ensures your environment matches CI exactly.

Install Nix with flake support:

```bash
curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix | sh -s -- install
```

### Manual install

If you prefer not to use Nix, install the following tools manually:

| Tool              | Version | Purpose                          |
|-------------------|---------|----------------------------------|
| Go                | 1.25+   | Backend                          |
| Node.js           | 22+     | Frontend                         |
| sqlc              | latest  | SQL-to-Go code generation        |
| golang-migrate    | latest  | Database migration CLI           |
| golangci-lint     | latest  | Go linting                       |
| Docker + Compose  | latest  | Local services and deployment    |
| just              | latest  | Task runner                      |
| psql              | 16      | PostgreSQL client (optional)     |
| redis-cli         | 7       | Redis client (optional)          |

Atlas is not yet in nixpkgs. Install it separately if you need migration linting:

```bash
curl -sSf https://atlasgo.sh | sh
```

---

## 2. Getting started

```bash
git clone https://github.com/ryanjdillon/brygge.git
cd brygge

# Enter the dev shell (provides all tools)
nix develop

# Install frontend dependencies
cd frontend && npm install && cd ..

# Start the full local stack
just dev
```

`just dev` starts PostgreSQL, Redis, the Go API, and the Vue dev server using Docker Compose with the development overlay (`deploy/docker-compose.dev.yml`).

The API is available at `http://localhost:8080` and the Vue dev server at `http://localhost:5173` (with hot module replacement).

---

## 3. Project structure

```
/
├── frontend/                   Vue 3 + TypeScript SPA
│   ├── src/
│   │   ├── components/ui/      Shadcn-vue components (source-owned)
│   │   ├── locales/            i18n translation files (nb.json, en.json)
│   │   ├── views/              Route-level page components
│   │   ├── stores/             Pinia stores
│   │   └── router/             Vue Router configuration
│   ├── package.json
│   └── vite.config.ts
│
├── backend/                    Go API server
│   ├── cmd/api/                Application entrypoint
│   ├── cmd/seed/               Seed data loader
│   ├── internal/               Private application packages
│   │   ├── handlers/           HTTP handlers
│   │   └── web/dist/           Placeholder for embedded Vue build
│   ├── migrations/             golang-migrate SQL files
│   ├── queries/                sqlc SQL query definitions
│   ├── gen/                    sqlc-generated Go code (committed)
│   ├── embed.go                go:embed directive (production)
│   ├── embed_noembed.go        Empty FS for separate frontend deploys
│   └── sqlc.yaml               sqlc configuration
│
├── deploy/
│   ├── docker-compose.yml      Production Compose file
│   ├── docker-compose.dev.yml  Development overlay
│   ├── .env.example            Documented configuration template
│   └── traefik/                Traefik config templates
│
├── scripts/
│   └── brygge.sh               Deployment CLI wrapper
│
├── docs/
│   ├── setup.md                Non-developer deployment guide
│   ├── contributing.md         This file
│   ├── deploy.md               Deployment guide with provider instructions
│   └── k8s.md                  Kubernetes portability notes
│
├── flake.nix                   Nix dev shell definition
├── flake.lock                  Pinned Nix inputs
├── justfile                    Task runner recipes
├── Dockerfile                  Production container image
└── .github/workflows/ci.yml   GitHub Actions CI pipeline
```

---

## 4. Development workflow

### Making changes

1. Create a feature branch from `main`:
   ```bash
   git checkout -b feat/your-feature
   ```

2. Make your changes. The Vue dev server hot-reloads. For Go changes, restart the API container or re-run `just dev`.

3. Run linting and tests before committing:
   ```bash
   just lint
   just test
   ```

### Available just recipes

| Recipe            | Description                                  |
|-------------------|----------------------------------------------|
| `just dev`        | Start the full local development stack       |
| `just down`       | Stop the local development stack             |
| `just test`       | Run all tests (Go + Vitest + Playwright)     |
| `just test-go`    | Run Go tests only                            |
| `just test-vue`   | Run Vue component tests only                 |
| `just test-e2e`   | Run Playwright end-to-end tests              |
| `just lint`       | Lint Go and Vue code                         |
| `just fmt`        | Format all code                              |
| `just migrate`    | Run pending database migrations              |
| `just generate`   | Run sqlc code generation                     |
| `just seed`       | Load demo/seed data                          |
| `just build`      | Build Go binary with embedded Vue dist       |

---

## 5. Code style

### Go

- Format with `gofmt` (enforced by CI).
- Lint with `golangci-lint` (`just lint-go`), configured in `backend/.golangci.yml`: the default linters except `errcheck`, plus `gocognit` (> 30), `cyclop` (> 20) and `nolintlint`. CI fails on any finding.
- Suppress a finding only with `//nolint:<linter> // <reason>`. Name the linter and give the reason; `nolintlint` rejects a bare `//nolint`. Raise neither threshold to make a function pass. Existing complexity suppressions end with "split tracked in BRY-424"; split a function when you are already changing it.
- golangci-lint shows at most three findings of the same kind per run. Run `golangci-lint run --max-same-issues=0 --max-issues-per-linter=0 ./...` to see them all before assuming a fix is complete.
- `gosec` runs in CI (the `security-go` job, pinned release) and fails on any finding. Fix the finding; suppress a false positive only on the flagged line with `// #nosec <rule> -- <reason>`, e.g. `// #nosec G120 -- body capped by MaxBytesReader above`. Name one rule and say why the input cannot be hostile; never a bare `#nosec`. Run it locally from the repo root with `go run github.com/securego/gosec/v2/cmd/gosec@<tag> -exclude=G104 -exclude-dir=gen -exclude-dir=vendor -exclude-dir=.go ./backend/...`, using the tag from `.github/workflows/ci.yml`.
- gosec's G104 (unchecked error) is excluded, as `errcheck` is: most hits are response, stdout and `Close` writes with no recovery. That makes unchecked errors a review concern — a dropped `Exec`, `Scan` or `Decode` error that changes stored data, a document, or a response must be checked and logged or returned.
- Follow standard Go conventions: short variable names in small scopes, exported names documented with comments, errors wrapped with `fmt.Errorf("context: %w", err)`.
- Destructure imports into standard library, external, and internal groups.

### Vue / TypeScript

- Lint with ESLint and format with Prettier (`npm run lint` and `npm run format` in `frontend/`).
- Use the Composition API with `<script setup lang="ts">`.
- Prefer Shadcn-vue components from `src/components/ui/`. These are source-owned files you can edit directly.
- All user-facing strings must use vue-i18n (`$t('key')` in templates, `useI18n()` in script). Norwegian (`nb.json`) is the primary locale.

### SQL

- Migrations and queries use plain SQL (no ORM).
- sqlc query definitions live in `backend/queries/`. Follow existing naming conventions: one file per domain (e.g. `health.sql`, `users.sql`, `slips.sql`).
- Use `-- name: GetUser :one` style annotations. See [sqlc documentation](https://docs.sqlc.dev/) for the full syntax.

---

## 6. Testing

### Go tests

```bash
just test-go
```

Go tests use `testcontainers-go` to spin up real PostgreSQL and Redis instances. No mocking of database calls. Tests run against the actual schema after applying all migrations.

For finance tests (invoices, orders, bank and Vipps rows, GL), build fixtures with `internal/testutil/fin`: `fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))` gives a club with an open fiscal period, a treasurer and a 1920 bank account. `club.NewInvoice`, `NewOrderForInvoice`, `NewBankRow` and `NewVippsRow` take amounts in integer øre. Use synthetic data only, never real members or account numbers.

### Vue component tests

```bash
just test-vue
```

Component tests use Vitest. Test files live alongside the components they test (e.g. `MyComponent.test.ts`).

### End-to-end tests

```bash
just test-e2e
```

Playwright tests run against the full stack. Install browsers first:

```bash
npx playwright install
```

### CI

GitHub Actions runs all of the above on every push and pull request to `main`. The CI pipeline uses the Nix flake for linting and `nix flake check` for flake integrity. Go tests run with real PostgreSQL and Redis service containers.

---

## 7. Database migrations

Brygge uses [golang-migrate](https://github.com/golang-migrate/migrate) with plain SQL files.

### Creating a new migration

```bash
migrate create -ext sql -dir backend/migrations -seq <descriptive_name>
```

This creates two files:

- `NNNNNN_descriptive_name.up.sql` -- the forward migration
- `NNNNNN_descriptive_name.down.sql` -- the rollback

Write your SQL in the up file. Always write a corresponding down migration that reverses the change.

### Applying migrations

```bash
just migrate
```

### Updating generated Go code

After changing queries or schema:

```bash
just generate
```

This runs `sqlc generate` to regenerate type-safe Go code from your SQL queries. The generated code in `backend/gen/` is committed to the repository so that contributors without sqlc installed can still build the project.

### Migration best practices

- Each migration should be a single logical change.
- The schema starts from a single consolidated baseline (`000001_init`). New migrations start at `000002`.
- Never modify an existing migration that has been merged to `main`. Create a new migration instead.
- Test both up and down migrations locally.
- Avoid destructive changes (dropping columns, renaming tables) without a multi-step migration plan.
- Atlas is used in CI to lint migrations and catch dangerous patterns before merge.

---

## 8. Submitting pull requests

### Branch naming

Use a prefix that describes the type of change:

| Prefix      | Use for                                  |
|-------------|------------------------------------------|
| `feat/`     | New features                             |
| `fix/`      | Bug fixes                                |
| `refactor/` | Code restructuring without behavior change |
| `docs/`     | Documentation only                       |
| `chore/`    | Build, CI, dependency updates            |

Examples: `feat/booking-calendar`, `fix/vipps-callback-timeout`, `docs/setup-guide`.

### Commit messages

Write clear, concise commit messages. Use imperative mood ("add", "fix", "update", not "added", "fixed", "updated").

```
feat: add guest slip booking availability check

The availability endpoint now returns conflicting bookings
so the frontend can show which dates are taken.
```

Keep the first line under 72 characters. Add a blank line and further explanation in the body if the change warrants it.

### CI checks

All of the following must pass before a PR can be merged:

- `lint` -- golangci-lint and ESLint/Prettier
- `test-go` -- Go tests with real database
- `test-vue` -- Vitest component tests
- `build` -- Full production build (frontend + Go binary with embedded dist)
- `nix-check` -- Flake integrity

If a check fails, look at the CI output, fix the issue locally, and push again.

### Review process

- Keep PRs focused on a single concern.
- Include a brief description of what changed and why.
- If the PR includes a migration, note it explicitly so reviewers can inspect the SQL.
- Screenshots or recordings are appreciated for UI changes.
