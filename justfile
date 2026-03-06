set dotenv-load

default:
    @just --list

# Start full local dev stack
dev:
    docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up --build

# Stop local dev stack
down:
    docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml down

# Run all tests (Go + Vitest + Playwright)
test: test-go test-vue test-e2e

# Run Go tests
test-go:
    cd backend && go test ./...

# Run Vue component tests
test-vue:
    cd frontend && npm run test

# Run Playwright E2E tests
test-e2e:
    cd frontend && npx playwright test

# Run pending database migrations
migrate:
    cd backend && go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
        -path migrations -database "$DATABASE_URL" up

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

# Format all code
fmt:
    cd backend && gofmt -w .
    cd frontend && npm run format
