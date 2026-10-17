# Auth middleware coverage matrix

Authoritative map of every API surface registered in
[`backend/cmd/api/main.go`](../../../backend/cmd/api/main.go) against the
authentication, authorization, and TOTP-gating middleware applied to it.
Keep this in sync when adding or moving routes — a mismatch here is the
signal that a route is under- or over-protected.

Tracking issue: **DIL-245**.

## Middleware vocabulary

| Middleware | Effect | Reject shape |
|---|---|---|
| `AuthenticateSession` | Requires a valid session cookie; loads `SessionInfo` into context. | `401 authentication required` / `401 invalid session` |
| `OptionalSessionAuth` | Loads `SessionInfo` if present; never rejects. Used where a route serves both anon and authed callers. | — |
| `RequireRole(…)` | Caller's roles must intersect the list. | `403` (structured `error`) |
| `RequireAdminTOTP` | TOTP enrolled **and** verified within the 12 h step-up window. Applied once to the whole `/admin` subtree. | `403 totp_required` / `403 totp_not_enrolled`, or `302` for top-level browser navigations |
| `RequireFreshTOTP(window)` / `RequireFreshTOTPDefault()` | TOTP verified within a short per-action window (default 10 min, `AUTH_FRESH_TOTP_WINDOW`). For high-blast-radius mutations. | `403 totp_fresh_required` (with `window_seconds`), or `302` for browser navigations |
| `RequireFreshTOTPIfEnrolled()` | Same fresh-window check, but only for users who already have a second factor (TOTP or passkey); users with none pass. Guards TOTP `setup`/`confirm` so an existing factor can't be replaced from a stale session. Fails closed without session info. | `403 totp_fresh_required` (with `window_seconds`), or `302` for browser navigations |
| `RequireDevToken(svc, scope)` | Developer bearer token (`Authorization: Bearer bry_dev_…`) that is live and carries `scope`. Ignores cookies entirely; injects claims for the token's creator and club with **no roles**, plus the token id. Used only on `/api/v1/dev/query` (BRY-115). | `401 developer bearer token required` / `401 invalid or expired developer token` |

Notes:
- `RequireAdminTOTP` wraps the entire `/api/v1/admin` group, so **every**
  admin route inherits the 12 h step-up. The matrix below only calls out
  the *additional* `RequireFreshTOTP` gates layered on top.
- The `/admin/totp/*` enrollment/verification endpoints are deliberately
  **outside** `RequireAdminTOTP` (chicken-and-egg: they are how you open
  the step-up window). `regenerate-codes` adds a fresh-TOTP gate.
  `setup` and `confirm` sit behind `RequireFreshTOTPIfEnrolled`: open for a
  user's first factor, but a user who already has TOTP or a passkey must
  step up fresh before replacing it.
- Reject bodies are SPA-interpretable (`totp_required` →
  redirect/enroll, `totp_fresh_required` → in-context modal). Top-level
  browser navigations get a `302` to `/admin/verify-totp?next=…` instead
  of raw JSON. See `internal/middleware/session.go`.

## Public / unauthenticated

| Route | Auth |
|---|---|
| `GET /health`, `GET /features` | none |
| `GET /pricing`, `GET /products`, `GET /boat-models`, `GET /weather`, `GET /club`, `GET /club/logo[.svg]` | none (rate-limited) |
| `POST /contact` | none (rate-limited) |
| `GET /legal/{docType}` | none |
| `GET /map/*` | none |
| `POST /auth/magic-link`, `GET /auth/verify` | none (strict rate-limit) |
| `GET /harbor/layout` | `OptionalSessionAuth` |
| `POST /bookings` | `OptionalSessionAuth` (guest booking) |
| `GET /betal/{token}`, `GET /checkout/{token}*`, `GET /betal/bruker/{token}` | none — the durable token is the capability (per-invoice and per-member payables links) |
| `POST /dev/query` | `RequireDevToken(db_query)` + per-IP and per-token (60/min) rate limits. Only mounted when `DEV_QUERY_TOKENS_ENABLED`. No session, no TOTP — the short-lived token is the credential, and the handler is bounded by the read-only, PII-masked `brygge_dev_ro` role (BRY-115) |

## Authenticated member surfaces (`AuthenticateSession`, no role gate)

`/auth/me`, `/auth/session/*`, `/members/me/*`, `/portal/slip-shares/*`,
`/waiting-list/*` (member actions), `/bookings/me*`, `/push/*`,
`/forum/*`, `/projects` + `/tasks` (read + self-join),
`/feature-requests` (read + vote), `/documents` (read + comment),
`/shopping-lists/*`. These are correctly member-scoped; ownership checks
live in the handlers.

## Role-gated, non-admin (`AuthenticateSession` + `RequireRole`)

| Route | Roles |
|---|---|
| `PUT /harbor/layout` | board, admin, harbor_master |
| `POST /bookings/{id}/confirm` | board, harbor_master |
| `POST/PUT/DELETE /calendar/*` | board |
| `GET /waiting-list/`, offer, reorder | board |
| `POST /projects`, project tasks; `PUT/DELETE /tasks/{id}`, assign, hours | board |
| `PUT /feature-requests/{id}/status`, promote | board |

## Admin subtree (`AuthenticateSession` + `RequireAdminTOTP` + `RequireRole`)

Everything below also carries the inherited 12 h `RequireAdminTOTP`.
The **Fresh TOTP** column marks the extra per-action gate.

| Route | Roles | Fresh TOTP |
|---|---|---|
| `GET /admin/audit` | board, admin | — |
| `POST /admin/dev/query` | admin | ✅ |
| `GET /admin/dev/tokens` | admin | — |
| `POST /admin/dev/tokens` | admin | ✅ |
| `DELETE /admin/dev/tokens/{tokenID}` | admin | ✅ |
| `/admin/volunteer/*` | board | — |
| `/admin/map/markers` (POST/PUT/DELETE) | board | — |
| `/admin/boats/*` | board, harbor_master | — |
| `/admin/broadcast*` | board, admin | — |
| **Financials** (`/admin/financials`) | treasurer, board, admin | — for reads |
| `POST /financials/invoices/full`, `/bulk`, send, resend, void, **delete**, bulk-reminder, bulk-regenerate-pdf, import/uni24, delivery-log | treasurer, **admin** | ✅ |
| **Users** (`/admin/users`) | board, admin | — for reads |
| `export.csv` | admin | — |
| `POST import` (CSV) | admin | ✅ |
| create, update, roles, slips, **delete**, boats CRUD | admin (boats) / board+admin | ✅ |
| `POST /users/{id}/totp/disable` | admin | ✅ |
| **Inbox** (`/admin/inbox`) | board-mailbox roles | — for reads |
| `POST /inbox/{address}/send` | (same) | ✅ |
| **Slips** (`/admin/slips`) | board, harbor_master, admin | — for reads |
| create, update, **delete**, assign, assignment-type, release | (same) | ✅ |
| `/admin/settings/general` | board, admin | — |
| `GET /admin/settings/site` | treasurer, admin | — |
| `PATCH /admin/settings/site` | treasurer, admin | ✅ |
| `/admin/settings/economy/faktura-logo` (POST/DELETE) | treasurer, admin | ✅ |
| `/admin/settings/bank-accounts` (POST/PUT/DELETE) | treasurer, admin | ✅ |
| `/admin/settings/site-logo` (POST/DELETE) | treasurer, admin | ✅ |
| `/admin/slip-shares/*` | board, harbor_master | — |
| `/admin/pricing` (CUD) | admin, treasurer | — |
| `/admin/products` (CUD) | board, admin | — |
| `/admin/documents` (upload/delete/AI) | board | — |
| `/admin/notifications/config` | board, admin | — |
| `/admin/gdpr/*` | board, admin | — |

### Accounting (`/admin/accounting`) — roles: treasurer, board, admin

| Route | Fresh TOTP | Notes |
|---|---|---|
| `/accounts` list/create/update/seed | — | |
| `DELETE /accounts/{id}` | ✅ (DIL-245) | destroys a GL account |
| `/periods` list/create | — | |
| `POST /periods/{id}/close`, `/reopen` | ✅ (DIL-245) | locks/unlocks the books |
| `/journal` list/get | — | |
| `POST /journal`, `/{id}/post`, `/{id}/void`, `DELETE /journal/{id}` | ✅ (treasurer, admin; BRY-370) | the double-entry ledger itself |
| `POST /sync/payments`, `/sync/invoices` (Avstem periode), `/sync/invoices/resplit`, `/sync/vipps-drafts` | ✅ (treasurer, admin; BRY-370) | book posted entries |
| `POST /sync/invoices/resplit/preview`, `/sync/vipps-drafts/preview` | — | read-only previews |
| `POST /bank-sync`, `/vipps-resync` | ✅ (treasurer, admin; BRY-370) | auto-match and post (KID, Vipps order and auto-assign) |
| `POST /bank-import` | ✅ (treasurer, admin; BRY-370) | posts import-time KID matches |
| `/bank-import/{id}/rows/{row}/match`, `/auto-match` | — | create drafts only |
| `PATCH /bank-import/{id}/account` | ✅ (treasurer, admin; BRY-370) | reassigns an import to another account; rewrites posted journal_lines |
| `POST /bank-import/account-reimport`, `/bank-import/{id}/reimport` | ✅ (treasurer, admin; BRY-370) | rerun the import cascade, which posts KID matches |
| `POST /vipps-imports` | ✅ (treasurer, admin; BRY-370) | posts order-linked and auto-assigned charges |
| `POST /bank-rows/{id}/reconcile-vipps/confirm` | ✅ (treasurer, admin; BRY-370) | posts the payout reconciliation |
| `/vipps-imports` list | — | |
| `bank-rows/{id}` suggestions/potential-invoices | — | reads |
| `assign-invoice[-multi]`, assign-account, dismiss, unassign | ✅ (treasurer, admin) | manual reconcile (Tildel) |
| `/rules/*` | — | reconciliation rules |
| `/reports/*` (incl. momskomp save/status) | — | reporting |

**Rule (BRY-370):** every route that can create or change a *posted* GL
entry, whether directly or by auto-matching during an import or sync,
requires the treasurer or admin role **and** a fresh step-up (`moneyGate` in
`cmd/api/main.go`). Board members keep read access to the books. The SPA asks
for the step-up at click time (`ensureFreshTotp()`). The journal, sync,
bank-import and bank-reconciliation routes are mounted by
`AccountingHandler.MountLedgerRoutes` (`internal/handlers/accounting_routes.go`);
`TestLedgerRoutesGateMoneyMovesForBoard` walks them and fails on any non-GET
route that answers a board session with anything but 403, outside a short
allowlist of previews, attachments and draft-only matches.

## Findings (DIL-245)

**Fixed in this change** — ledger mutations that were missing the
per-action fresh-TOTP gate while structurally identical mutations
elsewhere in the same file (invoice void/delete, bank-row assign) already
required it:

- `DELETE /admin/accounting/accounts/{id}`
- `POST /admin/accounting/periods/{id}/close` and `/reopen`
- `POST /admin/accounting/journal`, `/{id}/post`, `/{id}/void`

These are now `RequireFreshTOTPDefault()`. The change is purely additive
(re-prompts; removes no one's access) and matches the established pattern.

**Open recommendations (need a maintainer decision — deliberately not
changed here):**

1. **Role drift in the accounting block.** The manual reconcile endpoints
   (`bank-rows/.../assign-*`, dismiss, unassign) are narrowed to
   `treasurer, admin`, but the surrounding ledger mutations
   (journal/period/account/sync) still admit `board`. Either `board`
   should operate the ledger or it shouldn't — pick one and make the
   block consistent. Narrowing removes access, so it's a product call,
   not an autonomous one.
2. **`harbor_master` on financial surfaces.** `harbor_master` reaches
   `/admin/slips` and `/admin/slip-shares` (incl. rebate status, which has
   a financial effect) but not `/admin/financials`. Confirm that's
   intentional.
3. **`momskomp` save/status** (`POST /reports/momskomp`,
   `PUT /reports/momskomp/{id}/status`) is a VAT-compensation *filing*.
   Consider a fresh-TOTP gate if the saved report feeds an external
   submission.
4. **Audit-log column-shape mismatch (DIL-240 recurrence).** The issue
   notes silent 500s in `admin_pricing.go`, `admin_documents.go`, and
   `admin_slips.go` that read as auth failures from the SPA. That's an
   audit-write bug, not a middleware gap — tracked under **DIL-240**,
   verify and fix there.
5. **Frontend coverage (spec #5).** All admin mutations are reached via
   the openapi-fetch client, so the `totp_fresh_required` modal flow
   fires automatically. New direct `fetch()` calls bypassing that client
   would skip the modal — keep mutations on the typed client.
