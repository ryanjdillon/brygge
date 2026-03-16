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
    just migrate
    just seed
    @echo "\nsetup complete! run 'just up' to start the full stack"

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

# ── Deployment ────────────────────────────────────────────────

prod-compose := "docker compose -f deploy/docker-compose.yml"

# Deploy latest code to production (builds from source on server)
deploy host="brygge":
    ssh {{host}} 'cd /opt/brygge && git pull --ff-only origin main && docker compose -f deploy/docker-compose.yml build api && docker compose -f deploy/docker-compose.yml run --rm migrate && docker compose -f deploy/docker-compose.yml up -d api && docker image prune -f'

# Run smoke tests against a URL
smoke url="http://localhost:8080":
    ./scripts/smoke-test.sh {{url}}

# Rollback to a specific git commit
rollback sha host="brygge":
    ssh {{host}} 'cd /opt/brygge && git checkout {{sha}} && docker compose -f deploy/docker-compose.yml up -d --build api'

# Build Docker image locally
docker-build:
    docker build -t brygge:local --target production .

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
