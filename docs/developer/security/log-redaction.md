# Log redaction

Some URLs carry a bearer secret. A log line that records the URL records the secret, and anyone who can read the log can use it. This page says what is redacted, where, and how to keep it that way. The rule is the invariant "No bearer token in a URL is written to any log" in [invariants.md](../reference/invariants.md).

## What carries a secret

| URL | Secret | Lifetime |
|-----|--------|----------|
| `GET /api/v1/auth/verify?token=` | magic-link login token (stored hashed since BRY-652) | 15 minutes, one use |
| `/join/confirm?token=`, `/api/v1/join/confirm/{token}` | join confirmation token | per application |
| `/api/v1/unsubscribe?token=` | unsubscribe token | long-lived |
| `/betal/{token}`, `/betal/{token}/kvittering`, `/betal/bruker/{token}` | pay and payables tokens | until paid; payables are durable |
| `/checkout/{token}`, `/qr/{token}`, `/qr/{token}/pay` | checkout and QR order tokens | until the order settles |
| `?cid=` on the passkey endpoints | one-shot challenge id | minutes |
| `?access_token=` on Matrix client API calls (`/_matrix/...` on `matrix.<domain>`) | Matrix session token | until logout |

Each path form exists with and without the `/api/v1` prefix (the SPA page and the API call share the token). Dev tokens and session cookies travel in `Authorization` and `Cookie` headers, not in URLs.

## Where it is redacted

- **Caddy access logs** (`/var/log/caddy/access-<host>.log`, one per vhost). `nix/caddy-log.nix` wraps the JSON encoder in a `filter` with a `multi_regexp` on `request>uri`, `request>headers>Referer` and `resp_headers>Location`. The Referer matters: a page at `/betal/{token}` sends its own URL with every request it makes. The Location matters for redirects: `GET /api/v1/betal/{token}` sends the payer on to `/betal/{token}` or `/kvittering`, and `/q/{code}` to `/qr/{token}`. Matches become `REDACTED`; the rest of the URI stays, so the log is still useful for security work.
- **Caddy global log** (`services.caddy.logFormat`). Caddy writes an error entry, request URI included, to stderr, which is journald, which the OTel host collector ships off-host (`journald` receiver, unit `caddy.service`; see [local-collector.md](../otel/local-collector.md)). The same filter applies.
- **Caddy credentials.** `Authorization`, `Cookie`, `Proxy-Authorization` and `Set-Cookie` are redacted by Caddy unless `log_credentials` is set. Never set it. The flake check sends `Authorization` and `Cookie` and fails if either leaks.
- **App logs.** The request logger (`requestLogger` in `backend/cmd/api/main.go`) and the rate-limit middleware (`internal/middleware/ratelimit.go`) log the path through `logredact.Path` (`backend/internal/logredact`). Neither ever logged the query string, so magic-link tokens never reached the app log or the journal. These logs go to journald and off-host. The inbound server is not otelhttp-instrumented, so spans do not carry request URLs.

Nothing else ships off-host: the collector reads journald only and does not tail `/var/log/caddy`, so the Caddy access files stay on the host.

## Adding a token-carrying route

Prefer a POST body or an `Authorization` header. If the token has to be in the URL, add its path prefix to `pathTokenRoutes` (or its parameter to `queryParams`) in `nix/caddy-log.nix` and to `pathTokenRoute` in `backend/internal/logredact/logredact.go`, add a case to its test and a request to `nix/tests/caddy-log.nix`, and update the table above.

## Checks

- `nix flake check` runs `checks.<system>.caddy-log`: it starts the pinned Caddy with the rendered log block, sends requests with secrets in the URI, Referer, Cookie and Authorization, triggers an error entry, and fails if any secret appears in either log.
- `nix/host.nix` asserts that the global log equals `defaultLogFormat` and every virtualHost's `logFormat` equals the generated one, so a vhost added without it fails the build. A new vhost also goes in `caddyHosts`. The same check runs `caddy adapt` on the host's rendered Caddyfile.
- `go test ./internal/logredact ./internal/middleware` (path redaction, rate-limit log).

## Clearing what was logged before the filter

Caddy access logs written before the filter shipped hold URL tokens in clear; so do Caddy's error entries in the journal. Login tokens have expired. Pay, checkout and QR tokens last until their order settles. Payables and unsubscribe links are long-lived. The app log never held query strings, so magic-link tokens never reached the journal.

Owner steps, once, after the first deploy that carries the filter:

1. **Clear the access files** (best effort; rotated copies may exist elsewhere, such as backups):

   ```bash
   ssh root@<host> 'systemctl stop caddy && rm -f /var/log/caddy/access-*.log* && systemctl start caddy'
   ```

2. **Count what the journal exposed** (count only, nothing is printed):

   ```bash
   ssh root@<host> "journalctl -u brygge -u caddy --since -1month -g '/(betal|checkout|qr|join/confirm)/[^/ ]' | wc -l"
   ```

3. **Re-mint long-lived tokens if the count or the access files show them.**
   - Payables (`/betal/bruker/{token}`): no admin action exists. The token is `users.payables_token`, minted on first use and kept. Setting it to NULL for the affected members (a manual production SQL step) makes the next payables link mint a new one; links already sent stop working.
   - Unsubscribe (`/api/v1/unsubscribe?token=`): a stateless HMAC of user and category with no expiry and no per-user rotation; only changing the signing secret invalidates it. A leaked one can only unsubscribe that member from one category, so this is normally not worth a rotation.
   - Order and QR tokens end when the order settles; nothing to do.

4. **Let the journal age out.** `MaxRetentionSec=1month` removes the rest, and the upstream collector's owner can drop retained `caddy.service` logs from before the deploy.

5. **Optional, destructive:** `journalctl --rotate && journalctl --vacuum-time=1s` removes the exposed entries at once. It wipes the logs of every service on the host, not just Caddy's.

## Open follow-up

A magic link is still a GET that logs the user in, so a mail scanner that follows the link can consume it, and the token is in a URL for the browser history and any proxy in between. Moving verification to an SPA landing page that POSTs the token (as `POST /join/confirm` already does) would keep it out of every URL. Not done; tracked as a follow-up.
