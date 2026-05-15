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

## Two-factor authentication

### Admin link disappeared from the nav

**Symptom**: A user with admin/board role no longer sees "Admin" in the nav after a deploy.

**Cause**: Step-up 2FA is enforced (`RequireAdminTOTP`). The SPA hides the Admin link until the user has TOTP enrolled and a fresh verification.

**Fix**: Click the amber "Enable 2FA" prompt in the nav (or go to `/portal/security`) and enroll. Save the recovery codes. After enrollment + initial verify, the Admin link reappears.

### Magic link succeeded but TOTP still asked for

**Symptom**: User logs in successfully via magic link but is immediately redirected to `/admin/verify-totp` and asked for a code.

**Cause**: This is expected. Magic-link login establishes the session, but the 12-hour step-up window for `/admin/*` requires a fresh TOTP verification on top.

**Fix**: Enter the 6-digit code from the authenticator app. The user lands on the page they were trying to reach (read from `?next=`).

### Recovery code rejected as "invalid or already-used"

**Symptom**: User types a recovery code in `/admin/verify-totp` and it's rejected.

**Causes**:
1. Code was already redeemed (single-use)
2. Code was typed wrong (codes use only `A-Z` minus `O/I/L` and `2-9` minus `0/1` to avoid ambiguity — check for confused glyphs)
3. Codes were regenerated since the user saved this batch

**Fix**: Try another code from the saved batch. If none work, see "Lost authenticator AND recovery codes" below.

### Lost authenticator AND recovery codes

**Symptom**: User can sign in via magic link but can't pass the 2FA gate at all.

**Fix**: Another admin (with `admin` role specifically, not just `board`) hits `POST /api/v1/admin/users/{userID}/totp/disable` from a fresh-TOTP-verified session. Full procedure in [../security/2fa.md → Admin reset](../security/2fa.md#admin-reset-lost-authenticator-and-recovery-codes).

### All admins lost their devices simultaneously

**Symptom**: No admin can pass the 2FA gate, so no admin can run the admin-reset endpoint.

**Cause**: The chicken-and-egg case the admin-reset endpoint can't solve.

**Fix**: Manual DB intervention on the production VM. Full SQL in [../security/2fa.md → What if all admins lose their devices](../security/2fa.md#what-if-all-admins-lose-their-devices). After unlocking yourself, use the admin-reset endpoint for any other locked-out admins.

### `503 TOTP not configured` errors after a deploy

**Symptom**: Enrollment fails with `503 TOTP not configured (missing encryption key)`.

**Cause**: `TOTP_ENCRYPTION_KEY` is missing or invalid in `/etc/brygge/env`. Must be 64 hex characters (32 bytes).

**Fix**: Generate a key with `openssl rand -hex 32` and set it in the env file. Restart brygge. **Do not rotate this key once enrollments exist** — every enrolled user becomes unrecoverable.

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

See also: [deploy.md](deploy.md) | [CONTRIBUTING.md](../../CONTRIBUTING.md) | [setup.md](setup.md)
