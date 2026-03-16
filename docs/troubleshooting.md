# Troubleshooting

Common issues and solutions for Brygge development and deployment.

---

## Development

### Go tests fail with "connection refused"

**Symptom**: `dial tcp 127.0.0.1:5432: connect: connection refused`

**Cause**: Integration tests need real PostgreSQL and Redis containers.

**Fix**: Run `just up` first to start Docker services, then `just test-go-integration`. Unit tests (`just test-go`) use nil db/redis and don't need containers.

### `just generate` fails after schema changes

**Symptom**: sqlc reports errors about missing columns or types.

**Fix**: Ensure migrations are applied first (`just migrate`), then run `just generate`. The sqlc config reads the schema from the migration files, not the live database.

### API types out of sync

**Symptom**: TypeScript errors on API client calls, or `api-types` CI job fails.

**Fix**: Run `just api-types` to regenerate `frontend/src/types/api.d.ts` from the OpenAPI spec. If you added new endpoints, register them in `backend/internal/openapi/register.go` first.

### Vue i18n missing keys

**Symptom**: `[intlify] Not found key` warnings in the browser console.

**Fix**: When adding or modifying locale keys, update all 7 JSON files in `frontend/src/locales/`. Norwegian (`nb.json`) is the primary locale. Use `jq` or Python for safe editing — `nb.json` contains unicode characters that can be corrupted by naive string replacement.

---

## Deployment

### Dirty migration state

**Symptom**: `error: Dirty database version X. Fix and force version.`

**Cause**: A migration failed partway through, leaving the schema_migrations table in a dirty state.

**Fix**:
```bash
docker compose -f deploy/docker-compose.yml run --rm migrate \
  -path=/migrations -database "$DATABASE_URL" force <VERSION>
```
Replace `<VERSION>` with the version number shown in the error. Then re-run migrations.

### Traefik not issuing TLS certificates

**Symptom**: Browser shows "connection not secure" or certificate errors.

**Causes**:
- DNS A records not pointing to the server yet
- Port 80 blocked by firewall (needed for ACME HTTP challenge)
- Rate limit hit on Let's Encrypt (5 duplicate certs per week)

**Fix**: Check DNS propagation, ensure ports 80 and 443 are open, and inspect Traefik logs:
```bash
docker compose -f deploy/docker-compose.yml logs traefik
```

### CSP errors in browser console

**Symptom**: `Refused to load ... because it violates the Content Security Policy`

**Cause**: The Go binary serves CSP headers that restrict external resources. MapLibre, Kartverket, OSM, and Yr.no are already allowed.

**Fix**: If you need to allow a new external domain, update the CSP header in `backend/cmd/api/main.go` (search for `Content-Security-Policy`).

### Vipps mock not receiving callbacks

**Symptom**: Login works on the mock but the API never gets the callback.

**Cause**: The vipps-mock container sends webhooks to `http://api:8080/api/v1/webhooks/vipps` using Docker networking. If the API container isn't on the same Docker network, the callback fails.

**Fix**: Ensure both `api` and `vipps-mock` are on the `brygge` network in `deploy/docker-compose.yml`. Check logs:
```bash
docker compose -f deploy/docker-compose.yml logs vipps-mock
docker compose -f deploy/docker-compose.yml logs api | grep -i vipps
```

### SPA routes return 404

**Symptom**: Direct navigation to `/portal/bookings` or similar returns a blank page or 404.

**Cause**: The Go binary needs to serve `index.html` for unmatched routes (SPA fallback).

**Fix**: This is handled by the `handleSPAFallback` middleware in `cmd/api/main.go`. If you see this issue, ensure the Vue dist was properly embedded during build (`just build`).

### Container won't start after update

**Symptom**: API container exits immediately after `docker compose up -d`.

**Fix**: Check logs for the specific error:
```bash
docker compose -f deploy/docker-compose.yml logs api --tail 50
```
Common causes:
- Missing environment variables in `deploy/.env`
- Database not ready yet (check `db` container health)
- Migration needed (run `docker compose -f deploy/docker-compose.yml run --rm migrate`)

---

## Nuke and rebuild

If all else fails, you can reset the entire local or production environment:

```bash
# Local dev
just down
docker volume rm $(docker volume ls -q | grep brygge) 2>/dev/null
just setup

# Production (destructive — loses all data)
docker compose -f deploy/docker-compose.yml down -v
docker compose -f deploy/docker-compose.yml up -d db redis
docker compose -f deploy/docker-compose.yml run --rm migrate
docker compose -f deploy/docker-compose.yml run --rm --entrypoint /brygge-seed api
docker compose -f deploy/docker-compose.yml up -d
```

---

See also: [deploy.md](deploy.md) | [contributing.md](contributing.md) | [setup.md](setup.md)
