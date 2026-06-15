set dotenv-load

default:
    @just --list

compose := "docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"

# Start compose services + frontend dev server
up:
    #!/usr/bin/env bash
    set -euo pipefail
    {{compose}} up -d --build
    rm -rf frontend/node_modules/.vite
    cd frontend && nohup npx vite > /tmp/brygge-vite.log 2>&1 &
    echo $! > /tmp/brygge-vite.pid
    echo "compose services started, vite dev server running (pid $(cat /tmp/brygge-vite.pid), log: /tmp/brygge-vite.log)"

# Stop compose services + frontend dev server
down:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -f /tmp/brygge-vite.pid ]; then
        kill "$(cat /tmp/brygge-vite.pid)" 2>/dev/null || true
        rm -f /tmp/brygge-vite.pid
        echo "vite dev server stopped"
    fi
    {{compose}} down

# First-time setup: start services, run migrations, seed data
setup:
    {{compose}} up -d db redis
    @echo "waiting for postgres to be ready..."
    @sleep 3
    just dev-role-bootstrap
    just migrate
    just seed
    @echo "\nsetup complete! run 'just up' to start the full stack"

# Provision the brygge_dev_ro Postgres role used by /admin/dev/query (DIL-365).
# Idempotent. In prod this is handled by the brygge-dev-query-role systemd unit;
# this recipe mirrors it for local dev against the docker-compose postgres.
dev-role-bootstrap:
    {{compose}} exec -T db psql -U postgres -d brygge -v ON_ERROR_STOP=1 -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN CREATE ROLE brygge_dev_ro NOLOGIN; END IF; END \$\$;"
    {{compose}} exec -T db psql -U postgres -d brygge -v ON_ERROR_STOP=1 -c "GRANT USAGE ON SCHEMA public TO brygge_dev_ro; GRANT brygge_dev_ro TO brygge; ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public GRANT SELECT ON TABLES TO brygge_dev_ro; GRANT SELECT ON ALL TABLES IN SCHEMA public TO brygge_dev_ro; REVOKE ALL ON TABLE audit_log FROM brygge_dev_ro;"

# Pre-deploy dry-run: spins up a fresh test DB, applies every migration
# end-to-end as the brygge role (matching prod), drops the DB. Catches
# enum casts, IF NOT EXISTS gaps, role/privilege mismatches, and any
# migration that fails to roll forward on a clean slate.
#
# Run BEFORE `nix run .#deploy`. If this passes, the actual deploy
# migration step should also pass — short of legitimately prod-state-
# specific issues, which idempotent migrations should still survive.
#
# Requires `just up` (docker compose db + redis) to be running.
migrate-check:
    @echo "▶ Setting up clean test DB brygge_migrate_check..."
    {{compose}} exec -T db psql -U postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS brygge_migrate_check;" >/dev/null
    {{compose}} exec -T db psql -U postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE brygge_migrate_check OWNER brygge;" >/dev/null
    {{compose}} exec -T db psql -U postgres -d brygge_migrate_check -v ON_ERROR_STOP=1 -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN CREATE ROLE brygge_dev_ro NOLOGIN; END IF; END \$\$; GRANT USAGE ON SCHEMA public TO brygge_dev_ro; GRANT brygge_dev_ro TO brygge; ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public GRANT SELECT ON TABLES TO brygge_dev_ro;" >/dev/null
    @echo "▶ Applying all migrations to brygge_migrate_check..."
    nix develop --command bash -c 'migrate -path backend/migrations -database "postgres://brygge:brygge@localhost:5432/brygge_migrate_check?sslmode=disable" up'
    @echo "▶ Migrations applied cleanly. Cleaning up..."
    {{compose}} exec -T db psql -U postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE brygge_migrate_check;" >/dev/null
    @echo "✓ migrate-check passed."

# Run all tests (Go unit + Vue + Playwright)
test: test-go test-vue test-e2e

# Run Go unit tests (no database required)
test-go:
    cd backend && go test ./...

# Run Go tests with integration tests (requires DATABASE_URL and REDIS_URL)
test-go-integration:
    cd backend && go test -count=1 ./...

# Run Vue component tests
test-vue:
    cd frontend && npm run test

# Run Playwright E2E tests
test-e2e:
    cd frontend && npx playwright test

# Start only db and redis for running integration tests locally
test-services:
    {{compose}} up -d db redis

# Stop test services
test-services-down:
    {{compose}} down db redis

# Run Go coverage report
coverage-go:
    cd backend && go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Run Vue coverage report
coverage-vue:
    cd frontend && npx vitest run --coverage

# Run pending database migrations
migrate:
    {{compose}} run --rm migrate -path=/migrations -database 'postgres://brygge:brygge@db:5432/brygge?sslmode=disable' up

# Run sqlc code generation
generate:
    cd backend && sqlc generate

# Load demo/seed data
seed:
    cd backend && go run ./cmd/seed

# Print the current TOTP code for the seeded admin (paste into /admin/verify-totp)
totp:
    cd backend && go run ./cmd/totp

# Build Go binary with embedded Vue dist
build: build-frontend
    cp -r frontend/dist/* backend/internal/web/dist/
    cd backend && CGO_ENABLED=0 go build -o ../brygge ./cmd/api

# Build Vue frontend
build-frontend:
    cd frontend && npm run build

# Lint Go code
lint-go:
    cd backend && golangci-lint run ./...

# Lint Vue code
lint-vue:
    cd frontend && npm run lint

# Lint all
lint: lint-go lint-vue

# Find unused exports, dependencies, and dead code in frontend
knip:
    cd frontend && npx knip --reporter compact

# Run Go security scanner (gosec)
security-go:
    cd backend && gosec -exclude-dir=gen ./...

# Run Go vulnerability check
vuln-go:
    cd backend && govulncheck ./...

# Run npm dependency audit
audit-npm:
    cd frontend && npm audit --audit-level=high

# Run all security checks
security: security-go vuln-go audit-npm

# Format all code
fmt:
    cd backend && gofmt -w .
    cd frontend && npm run format

# ── Deployment (NixOS on Hetzner) ─────────────────────────────

# One-time bootstrap: install NixOS onto a fresh Hetzner VM via nixos-anywhere.
# Requires the target in rescue mode (hcloud server enable-rescue <name> --type linux64 && reset <name>).
install host:
    nix run .#install -- {{host}}

# Deploy the current flake state to the server (builds locally, activates remotely).
deploy host="brygge":
    nix run .#deploy -- {{host}}

# Roll back to the previous system generation on the server.
rollback host="brygge":
    nix run .#deploy -- {{host}} --rollback

# Build the brygge package locally (frontend + Go binary with embedded SPA).
build-nix:
    nix build .#brygge

# Run smoke tests against a URL
smoke url="http://localhost:8080":
    ./scripts/smoke-test.sh {{url}}

# ── API Documentation ─────────────────────────────────────────

# Generate OpenAPI spec (JSON to stdout)
openapi-spec:
    cd backend && go run ./cmd/openapi/

# Generate TypeScript API types from OpenAPI spec
api-types:
    cd backend && go run ./cmd/openapi/ > /tmp/brygge-openapi.json
    cd frontend && npx openapi-typescript /tmp/brygge-openapi.json -o src/types/api.d.ts
    @echo "generated frontend/src/types/api.d.ts"

# ── Infrastructure (Terranix + OpenTofu) ───────────────────────

# Plan infrastructure changes
tf-plan:
    nix run .#tf-plan

# Apply infrastructure changes
tf-apply:
    nix run .#tf-apply

# Show current infrastructure outputs
tf-output:
    cd terraform && tofu output

# ── Database ──────────────────────────────────────────────────

# Run EXPLAIN ANALYZE on a query
explain query:
    psql "$DATABASE_URL" -c "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) {{query}}"
