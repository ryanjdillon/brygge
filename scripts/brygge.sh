#!/usr/bin/env bash
set -euo pipefail

# Brygge CLI — deployment wrapper for Docker Compose operations.
# Make executable: chmod +x scripts/brygge.sh
# Symlink for convenience: ln -s "$(pwd)/scripts/brygge.sh" /usr/local/bin/brygge

# ── Colors ────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

info()    { printf "${GREEN}[brygge]${NC} %s\n" "$*"; }
warn()    { printf "${YELLOW}[brygge]${NC} %s\n" "$*"; }
error()   { printf "${RED}[brygge]${NC} %s\n" "$*" >&2; }
heading() { printf "\n${BOLD}%s${NC}\n" "$*"; }

# ── Resolve project root ──────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$PROJECT_ROOT/deploy/docker-compose.yml"
ENV_FILE="$PROJECT_ROOT/deploy/.env"
BACKUP_DIR="$PROJECT_ROOT/backups"

# ── Preflight checks ─────────────────────────────────────────────

check_requirements() {
    local missing=0

    if ! command -v docker &>/dev/null; then
        error "docker is not installed. See https://docs.docker.com/engine/install/"
        missing=1
    fi

    if ! docker compose version &>/dev/null; then
        error "docker compose (v2 plugin) is not available."
        error "Install it: https://docs.docker.com/compose/install/"
        missing=1
    fi

    if [[ $missing -ne 0 ]]; then
        exit 1
    fi
}

check_env() {
    if [[ ! -f "$ENV_FILE" ]]; then
        error ".env file not found at $ENV_FILE"
        error "Copy the example and fill in your values:"
        error "  cp deploy/.env.example deploy/.env"
        exit 1
    fi
}

compose() {
    docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

# ── Load env for variables we need in this script ─────────────────

load_env() {
    check_env
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
}

# ── Commands ──────────────────────────────────────────────────────

cmd_setup() {
    heading "Brygge Setup"

    check_requirements
    check_env
    load_env

    info "Pulling Docker images..."
    compose pull

    info "Starting database and Redis..."
    compose up -d db redis
    info "Waiting for database to be ready..."
    sleep 5

    info "Running database migrations..."
    cmd_migrate

    info "Starting all services..."
    compose up -d

    info "Waiting for services to start..."
    sleep 10

    heading "Setup complete"
    cmd_status
}

cmd_update() {
    heading "Brygge Update"

    check_requirements
    load_env

    info "Pulling latest Docker images..."
    compose pull

    info "Running pending database migrations..."
    cmd_migrate

    info "Restarting services..."
    compose up -d --remove-orphans

    info "Update complete."
    cmd_status
}

cmd_backup() {
    heading "Brygge Backup"

    check_requirements
    load_env

    mkdir -p "$BACKUP_DIR"

    local timestamp
    timestamp="$(date +%Y%m%d_%H%M%S)"
    local dump_file="$BACKUP_DIR/brygge_${timestamp}.sql.gz"

    info "Dumping database to $dump_file..."

    docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" \
        exec -T db pg_dump \
        -U "${POSTGRES_USER}" \
        "${POSTGRES_DB}" | gzip > "$dump_file"

    local size
    size="$(du -h "$dump_file" | cut -f1)"
    info "Backup complete: $dump_file ($size)"

    # Clean up backups older than 30 days
    local removed
    removed=$(find "$BACKUP_DIR" -name "brygge_*.sql.gz" -mtime +30 -delete -print | wc -l)
    if [[ $removed -gt 0 ]]; then
        info "Removed $removed backup(s) older than 30 days."
    fi
}

cmd_logs() {
    check_requirements
    check_env

    local service="${1:-}"
    if [[ -n "$service" ]]; then
        compose logs -f "$service"
    else
        compose logs -f
    fi
}

cmd_status() {
    heading "Service Status"

    check_requirements
    load_env

    compose ps

    # Check API health endpoint
    local domain="${DOMAIN:-}"
    if [[ -n "$domain" ]]; then
        printf "\n"
        info "Checking API health endpoint..."
        local health_url="https://${domain}/api/health"
        local http_code
        http_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "$health_url" 2>/dev/null || true)

        if [[ "$http_code" == "200" ]]; then
            info "API health: ${GREEN}OK${NC} ($health_url)"
        elif [[ -n "$http_code" && "$http_code" != "000" ]]; then
            warn "API health: HTTP $http_code ($health_url)"
        else
            warn "API health: unreachable ($health_url)"
            warn "This is expected if DNS is not yet configured or TLS is still provisioning."
        fi
    fi
}

cmd_migrate() {
    check_requirements
    load_env

    info "Running pending database migrations..."

    compose run --rm -T api \
        /brygge migrate -path /migrations -database "$DATABASE_URL" up \
        2>&1 || {
            warn "Migration via container failed. Trying with local migrate tool..."
            if command -v migrate &>/dev/null; then
                migrate -path "$PROJECT_ROOT/backend/migrations" -database "$DATABASE_URL" up
            else
                error "Could not run migrations. Ensure the API container or golang-migrate is available."
                exit 1
            fi
        }

    info "Migrations complete."
}

cmd_stop() {
    heading "Stopping Brygge"
    check_requirements
    check_env
    compose down
    info "All services stopped."
}

cmd_restart() {
    heading "Restarting Brygge"
    check_requirements
    check_env
    compose restart
    info "All services restarted."
}

cmd_help() {
    cat <<EOF
${BOLD}brygge${NC} — Brygge deployment CLI

${BOLD}Usage:${NC}
    brygge <command> [args]

${BOLD}Commands:${NC}
    setup             Validate config, pull images, init DB, run migrations, start services
    update            Pull latest images, run new migrations, restart services
    backup            Dump database to timestamped gzipped file
    logs [service]    Tail logs (optionally for a single service)
    status            Show health of all services
    migrate           Run pending database migrations
    stop              Stop all services
    restart           Restart all services
    help              Show this help message

${BOLD}Examples:${NC}
    brygge setup          # First-time deployment
    brygge logs api       # Tail API logs
    brygge backup         # Create database backup

${BOLD}Environment:${NC}
    Configuration is read from deploy/.env
    Docker Compose file:  deploy/docker-compose.yml
    Backups stored in:    backups/
EOF
}

# ── Main ──────────────────────────────────────────────────────────

command="${1:-help}"
shift || true

case "$command" in
    setup)    cmd_setup "$@"   ;;
    update)   cmd_update "$@"  ;;
    backup)   cmd_backup "$@"  ;;
    logs)     cmd_logs "$@"    ;;
    status)   cmd_status "$@"  ;;
    migrate)  cmd_migrate "$@" ;;
    stop)     cmd_stop "$@"    ;;
    restart)  cmd_restart "$@" ;;
    help|-h|--help) cmd_help   ;;
    *)
        error "Unknown command: $command"
        cmd_help
        exit 1
        ;;
esac
