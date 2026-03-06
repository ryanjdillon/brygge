# Brygge Backend — Multi-stage Dockerfile
# Produces a single static binary with the SPA embedded.
# Target platform: linux/arm64 (Hetzner CAX11)

# ── Stage 1: Build frontend ─────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ── Stage 2: Build Go binary (with embedded frontend) ───────
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY backend/go.* ./
RUN go mod download
COPY backend/ .
COPY --from=frontend /frontend/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -ldflags="-s -w" -o /brygge ./cmd/api

# ── Stage 3: Minimal runtime ────────────────────────────────
FROM gcr.io/distroless/static:nonroot AS production
COPY --from=builder /brygge /brygge
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/brygge"]

# ── Dev target: Go with live reload ─────────────────────────
FROM golang:1.25-alpine AS dev
RUN go install github.com/air-verse/air@latest
WORKDIR /app
COPY backend/go.* ./
RUN go mod download
COPY backend/ .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
