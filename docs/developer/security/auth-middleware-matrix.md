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

Notes:
- `RequireAdminTOTP` wraps the entire `/api/v1/admin` group, so **every**
  admin route inherits the 12 h step-up. The matrix below only calls out
  the *additional* `RequireFreshTOTP` gates layered on top.
- The `/admin/totp/*` enrollment/verification endpoints are deliberately
  **outside** `RequireAdminTOTP` (chicken-and-egg: they are how you open
  the step-up window). Only `regenerate-codes` adds a fresh-TOTP gate.
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
| `POST /orders`, `GET/POST /orders/{id}*` | none (commerce checkout) |

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
| `POST /journal`, `/{id}/post`, `/{id}/void` | ✅ (DIL-245) | the double-entry ledger itself |
| `/sync/*`, `/bank-sync`, `/vipps-resync` | — | idempotent re-derivations |
| `/bank-import` create/match/auto-match | — | |
| `PATCH /bank-import/{id}/account` | ✅ | reassigns an import to another account |
| `/vipps-imports` create/list | — | |
| `bank-rows/{id}` suggestions/potential-invoices | — | reads |
| `assign-invoice[-multi]`, assign-account, dismiss, unassign | ✅ (treasurer, admin) | manual reconcile (Tildel) |
| `/rules/*` | — | reconciliation rules |
| `/reports/*` (incl. momskomp save/status) | — | reporting |

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
