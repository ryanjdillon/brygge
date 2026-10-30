# Testing Vipps payment flows locally

Exercise the full ePayment path — create → approve → capture → **webhook** →
order marked `paid` — without a Vipps account and without spending real kroner.
Every payment surface (faktura pay links, harbor QR checkout, merch) uses the
same mint → capture → webhook path, so testing one exercises them all.

There is only one deployed instance (prod), so all local testing happens against
the mock or the Vipps test environment described below — never prod.

## 1. Local mock (default, recommended)

The dev overlay runs a `vipps-mock` service (`dev/vipps-mock`) that simulates the
ePayment API and fires **HMAC-signed** webhooks back to the API. No internet, no
Vipps account, no money, no tunnel.

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up
```

Wiring (already in `deploy/docker-compose.dev.yml`):

- **api**: `VIPPS_TEST_MODE=true`, `VIPPS_MOCK_URL=http://vipps-mock:8090`, a
  dummy `VIPPS_MSN=mock-msn` (the client is only built when `VIPPS_MSN` is set),
  and `VIPPS_WEBHOOK_SECRET=dev-webhook-secret`.
- **vipps-mock**: `WEBHOOK_TARGET=http://api:8080/api/v1/webhooks/vipps` and
  `WEBHOOK_SECRET=dev-webhook-secret`.

The two secrets **must match**: the mock signs each webhook and the API runs its
real HMAC-SHA256 verification (BRY-200), so the local loop exercises the same
verification path as prod. `VIPPS_MOCK_URL` wins over `apitest.vipps.no` whenever
`VIPPS_TEST_MODE=true`, which is what lets a dummy MSN coexist with the mock.

To test a payment:

1. Trigger one — e.g. open a `/qr/{token}` harbor page and pay, or click a
   faktura pay link.
2. The browser is redirected to the mock approve page at
   `http://localhost:8090/mock/approve`.
3. Click **Approve** → mock fires `authorized` → API captures → mock fires
   `captured` → the order flips to `paid`.
4. It appears under **Economy → Payments** (recent-payments) within seconds.

Watch the mock: `docker compose ... logs -f vipps-mock` (it logs every call and
webhook, including the send status code — a non-2xx means the signature or
secret is wrong).

## 2. Vipps test environment (MT) — higher fidelity

Use the real `apitest.vipps.no` with the Vipps **test app** when you need to
validate genuine Vipps behaviour rather than the mock's approximation.

- Set `VIPPS_TEST_MODE=true`, a real **test** MSN + test client credentials, and
  **leave `VIPPS_MOCK_URL` unset** so `VippsBaseURL()` resolves to
  `apitest.vipps.no`.
- Webhooks must reach your local server, so expose it with a tunnel and register
  that public URL:

  ```bash
  cloudflared tunnel --url http://localhost:8080     # or: ngrok http 8080
  ```

  Register the tunnel's `…/api/v1/webhooks/vipps` URL via the Webhooks API
  (`internal/vipps/webhooks_api.go`) and set the returned secret as
  `VIPPS_WEBHOOK_SECRET`.
- Approve payments with the Vipps MT app / test cards.

Prefer the mock for day-to-day work. Note that **settlement / Report API data is
prod-only** — it cannot be exercised on MT or the mock.
