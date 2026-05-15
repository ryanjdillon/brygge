# Role-gated shared inbox

A role-gated shared inbox lets the club's board read and reply to the role mailboxes (`leiar@`, `kasserar@`, `styre@`, …) from the admin portal at `/admin/inbox`, without each member needing personal webmail credentials. Access converges automatically from the `user_roles` table — when a Brygge user is granted `treasurer`, they gain access to `kasserar@`; when the role is revoked, access drops within ~5 minutes.

Parent issue: [DIL-275](https://linear.app/dillonteknisk/issue/DIL-275). Sub-phases: DIL-276 (reconciler), DIL-277 (read UI), DIL-278 (send), DIL-321 (user provisioning), DIL-322 (secret hashing).

This doc is the **operational + design reference** for the feature. For day-to-day Stalwart admin (DKIM, role-mailbox primitives, deliverability) see [setup.md](setup.md). For Stalwart 0.15's protocol quirks see [stalwart-internals.md](stalwart-internals.md).

---

## Components

```
                ┌───────────────────────────────────────────────────────┐
                │                       brygge                          │
                │                                                       │
  user logs in  │  ┌──────────────────────┐                             │
   (magic link) │  │ UserProvisioner      │ ── creates Stalwart         │
        ─────────▶ │ (DIL-321)            │    principal `bu<12hex>@    │
                │  │                      │    <club_domain>` + bcrypt  │
                │  └──────────────────────┘    secret in DB             │
                │                                                       │
  role granted  │  ┌──────────────────────┐                             │
   on user      │  │ Reconciler (DIL-276) │ ── JMAP Mailbox/set         │
        ─────────▶ │ event + 5-min cron   │    shareWith on the role    │
                │  └──────────────────────┘    principal's Inbox        │
                │                                                       │
  user opens    │  ┌──────────────────────┐                             │
   /admin/inbox │  │ InboxHandler         │ ── authenticates JMAP as    │
        ─────────▶ │ (DIL-277)            │    the user; cross-account  │
                │  │  read + mark/archive │    Mailbox/get on the role  │
                │  └──────────────────────┘    account (Stalwart honors │
                │                              shareWith)               │
                │                                                       │
  user clicks   │  ┌──────────────────────┐                             │
   reply        │  │ InboxHandler.Send    │ ── authenticates JMAP as    │
        ─────────▶ │ (DIL-278)            │    the SHARED principal     │
                │  │  TOTP-fresh gate     │    (kasserar) using its     │
                │  └──────────────────────┘    service password         │
                │                                                       │
                └───────────────────────────────────────────────────────┘
```

**Two parallel auth tracks:**

- **User authenticates as themselves** for *reading* — Stalwart's `shareWith` grants cross-account access, so the user's JMAP session includes the shared accounts they have rights to.
- **Backend authenticates as the shared principal** for *sending* — outbound mail must be `From: kasserar@…` and JMAP submission requires acting as the account that owns the message. The `X-Brygge-Actor: <user-id>` header preserves accountability.

Why two tracks? Because Stalwart 0.15 doesn't support an OAuth grant that lets admin mint tokens for other principals (verified — both `password` and `token-exchange` grants return `invalid_grant`). The simplest path that actually works is per-principal Basic auth, with service passwords for the shared accounts kept in a root-owned file outside `/nix/store`. See [stalwart-internals.md § auth model](stalwart-internals.md#auth-model) for the full backstory.

---

## Spec format

The role → mailbox mapping lives in `terraform/terraform.tfvars.json` under `board_mailboxes`:

```json
{
  "board_mailboxes": [
    {
      "address": "kasserar@klokkarvikbaatlag.no",
      "role": "treasurer",
      "display_name": "Kasserar",
      "type": "shared",
      "send_as": true,
      "bcc_members": false,
      "managed": true
    },
    ...
  ]
}
```

Fields:

| Field | Notes |
|---|---|
| `address` | Full email at the club's mail domain. Must match a Stalwart principal (provisioned by `stalwart-mailbox-config.service`). |
| `role` | A value of the `user_role` Postgres enum. Holders of this role get reader/contributor `shareWith` on the address's Inbox. |
| `display_name` | Used as the From-name when sending — prefixed with the uppercased club slug (`"KBL Kasserar"`). |
| `type` | `"shared"` for portal-readable mailboxes. `"list"` for pure forwarding aliases (not touched by the reconciler). |
| `send_as` | Reserved for DIL-278 future polish — account-level delegation, not currently honored at JMAP layer. |
| `bcc_members` | When `true`, outgoing sends include every role-holder as Bcc (each gets a personal copy). |
| `managed` | When `false`, the reconciler skips the address entirely — useful for legacy mailboxes you don't want auto-managed. |

The flake reads this file directly via `lib.fakeHash` style `tfvars = builtins.fromJSON …` and threads `boardMailboxes` through `clubConfig` into `nix/host.nix`. Editing the file and `nix run .#deploy` is the deploy flow; no `tf-apply` needed for this side of things (DNS records aren't affected).

---

## Reconciler

**File**: `backend/internal/mail/reconciler.go`.

Goal: the `shareWith` set on each shared mailbox's Inbox should equal `{ user_id ∈ users with the mapped role AND a provisioned Stalwart principal }`, with reader+contributor rights.

Triggers:

- **Role change hook** — fires from `HandleUpdateUserRoles` and `createUserCommon` (admin user CRUD). Detached goroutine; failures don't block the HTTP response.
- **5-minute cron** — `time.NewTicker` in `cmd/api/main.go`. Catches any drift from missed hooks, manual Stalwart edits, or Stalwart restarts mid-apply.
- **Boot-time pass** — runs once 30 s after `brygge.service` starts.

Each cycle:

1. Build an index of `name → JMAP id` from admin's `Principal/get` (admin can enumerate principals even though it can't act on other accounts).
2. For each managed `type=shared` mailbox in the spec:
   1. Query `user_roles` for users holding the mapped role; JOIN `user_mail_credentials` so only users with a Stalwart principal participate.
   2. Build `shareWith: { jmap_id: ShareRights{...} }`.
   3. Compute a canonical SHA-256 hash. If it matches `mailbox_sync_state.applied_hash`, skip (no-op).
   4. Authenticate JMAP as the shared principal using the password from `/etc/stalwart/board-mailbox-passwords.json`.
   5. Resolve the shared account's Inbox folder id.
   6. `Mailbox/set { shareWith }` to apply.
   7. Update `mailbox_sync_state` with new hash + timestamp; write `inbox.acl_changed` audit row.

Failures land in `mailbox_sync_state.last_error` and re-attempt on the next cycle.

`BRYGGE_RECONCILER_DRY_RUN=1` in `/etc/brygge/env` short-circuits the JMAP call and logs `dry-run: would apply ACLs address=… desired_hash=… members=N`. Useful during cutover; flip off once the desired hash looks right.

### shareWith rights (RFC 8621 §2.5)

Each grant carries:

```go
ShareRights{
    MayReadItems:   true,
    MaySetSeen:     true,
    MaySetKeywords: true,
    MayAddItems:    true,
    MayRemoveItems: true,
}
```

Field names match RFC 8621 exactly. `mayRead` (no `Items`) is invalid; Stalwart rejects it. See [stalwart-internals.md § Mailbox/set shareWith](stalwart-internals.md#mailboxset-sharewith).

---

## Per-user Stalwart provisioning

**File**: `backend/internal/mail/user_provisioner.go`. **Migration**: `000046_user_mail_credentials.up.sql`.

For the reconciler to share a mailbox with a user, the user needs a Stalwart principal — Brygge's user-row alone isn't enough. Provisioning happens in two paths:

1. **Lazy (preferred)**: on first successful magic-link verify, a detached goroutine calls `EnsureUserPrincipal`. Login latency unaffected.
2. **Eager**: when admin assigns any role in `boardMailboxRoles` (chair, vice_chair, treasurer, harbor_master, secretary, board), synchronous. So an admin can add a treasurer who's never logged in and the reconciler's next pass populates `shareWith` immediately.

Both call `EnsureUserPrincipal(ctx, userID, email)`:

1. Look up `user_mail_credentials` — if a row exists, return.
2. Build slug `bu<first-12-hex-of-userID>` (Stalwart's principal-name validator rejects dashes; alphanumeric only — see [stalwart-internals.md](stalwart-internals.md#principal-name-constraints)).
3. Generate 32-char URL-safe password via `crypto/rand`.
4. **bcrypt-hash** the password before sending (admin REST POST persists `secrets` verbatim — see [stalwart-internals.md § secret hashing](stalwart-internals.md#secret-hashing-on-create-vs-patch)).
5. Create the Stalwart principal with `type=individual`, `name=slug`, `emails=[slug@<club_domain>]`. The user's real email (e.g. `@gmail.com`) is NOT used for Stalwart — only on the Brygge side. See [stalwart-internals.md § email domain validation](stalwart-internals.md#email-domain-validation).
6. Encrypt the plaintext password with `auth.Encrypt(TOTP_ENCRYPTION_KEY)` and INSERT into `user_mail_credentials`.
7. Audit `user.mail_provisioned`.

`Credentials(ctx, userID)` is the read-back: decrypt and return for use in the user-facing JMAP read path.

Deletion (`DeleteUserPrincipal`) fires from `HandleDeleteUser` (both soft-delete and erasure paths) — Stalwart principal removed, `user_mail_credentials` row deleted (CASCADE handles the latter when the user row is erased).

---

## Read path (DIL-277)

**File**: `backend/internal/handlers/inbox.go`. **Routes**: under `/api/v1/admin/inbox/...`, role-gated to `{chair, vice_chair, treasurer, harbor_master, secretary, board, admin}` + the standard admin TOTP 12 h step-up.

- `GET /mailboxes` — lists shared mailboxes the caller has access to, with unread counts. The handler iterates the spec, filters by `auth.hasRole(spec.role)`, fetches counts via JMAP `Mailbox/get` on each shared account using the user's session.
- `GET /:address/threads?cursor=&q=` — paginated thread list. `Email/query` with `inMailbox` filter; reduces to one row per `threadId`.
- `GET /:address/threads/:thread_id` — full message list. `Thread/get` → `Email/get` with `bodyValues`. Body HTML is sent raw; the SPA sanitises with DOMPurify on render.
- `POST /:address/threads/:thread_id/mark_read?read=true|false` — toggles `$seen` on the *latest* email only (Gmail-style thread semantics).
- `POST /:address/threads/:thread_id/archive` — moves every email in the thread to the Archive folder via `Mailbox/set`.

Per-address auth re-check: `authorize()` reads `claims.Roles` from the session and matches against `spec.role` on every request. URL-decodes `:address` via `url.PathUnescape` (chi v5 doesn't auto-decode percent-encoded `@`).

---

## Send path (DIL-278)

**File**: same handler. **Route**: `POST /:address/send`, gated additionally with `RequireFreshTOTP(10 * time.Minute)` — same posture as void-invoice / delete-user, since outbound mail is irreversible.

Payload:

```json
{
  "to": [{"name": "...", "email": "..."}],
  "cc": [...],
  "subject": "Re: …",
  "body_text": "...",
  "body_html": "...",
  "in_reply_to": "<Message-ID>"
}
```

Flow:

1. `authorize()` — role check + spec lookup.
2. Look up the shared principal's service password in `passwords` map.
3. Build a JMAP client authenticated as the shared principal.
4. `resolveSendMailboxes`: get accountId + Drafts/Sent ids. Stalwart 0.15 only auto-creates Inbox on principal init — Drafts/Sent are created on-demand via `Mailbox/set { create }` first time we send from a mailbox.
5. `resolveIdentity`: `Identity/get` and pick the one whose email matches the shared address (or first available). `EmailSubmission/set` requires `identityId`.
6. Build From name as `"KBL Kasserar"` (uppercased `cfg.ClubSlug` + spec display name). Gmail's contact-card override surfaces just the club name in inbox listings; the message header carries the role-specific form.
7. If `spec.bcc_members` is true, query for every user with the role and add to Bcc.
8. `Email/set { create: {tmp: ...} }` + `EmailSubmission/set { create: {sub: {emailId: "#tmp", identityId, envelope}}, onSuccessUpdateEmail: {Drafts→null, Sent→true} }` — moves the message from Drafts to Sent atomically on success.
9. Audit `inbox.message_sent` with `target_address`, `recipient_count`, `in_reply_to`, `message_id`, `bcc_members`, `actor_id`.

The Email/set body shape has several gotchas — see [stalwart-internals.md § Email/set](stalwart-internals.md#emailset-create-body-shape).

---

## Deploy gate

**File**: `nix/host.nix` — `systemd.services.brygge-inbox-validate`.

Runs as the `brygge` user *between* `stalwart-mailbox-config.service` and `brygge.service`. Validates:

- `/etc/stalwart/board-mailbox-passwords.json` is readable by `brygge` (catches `/etc/stalwart` traversal-perm regressions).
- Every managed `type=shared` mailbox in the spec has a password entry.

A failure here propagates through the systemd dependency chain → `brygge.service` doesn't start → `deploy-rs` activation exits non-zero → magic-rollback reverts. **No silent half-working state.** This was a deliberate design choice — a `WRN` line in journald is too easy to miss.

If the validate unit fails, `systemctl status brygge-inbox-validate` carries the exact error (which mailbox is missing a password, which file isn't readable as which user, etc.).

---

## Verification recipes

After a deploy, re-trigger user provisioning to pick up the latest provisioner behaviour:

```bash
sudo -u postgres psql brygge -c "TRUNCATE user_mail_credentials;"

TOKEN="probe-$(cat /proc/sys/kernel/random/uuid)"
sudo -u postgres psql brygge -c \
  "INSERT INTO magic_links (token, email, club_id, expires_at, used)
     SELECT '$TOKEN', u.email, u.club_id, now() + interval '15 min', false
       FROM users u WHERE u.email = '<your-email>';"

curl -sS -o /dev/null -w 'verify=%{http_code}\n' \
  "https://<domain>/api/v1/auth/verify?token=$TOKEN"
```

Inspect the resulting Stalwart principal (should be `bu<12hex>` with `bu…@<club_domain>` synthetic email, bcrypt-hashed secret, description marker `managed_by=brygge-user`):

```bash
ADMIN=$(cat /etc/stalwart/admin-password)
SLUG=$(sudo -u postgres psql -tA brygge -c "SELECT jmap_user FROM user_mail_credentials LIMIT 1;")
curl -fsS -u "admin:$ADMIN" "http://127.0.0.1:8088/api/principal/$SLUG" | jq
```

Confirm a shared mailbox's `shareWith` after assigning a role:

```bash
PW=$(sudo -u brygge jq -r '."kasserar@klokkarvikbaatlag.no"' /etc/stalwart/board-mailbox-passwords.json)
curl -fsS -u "kasserar:$PW" -X POST -H 'Content-Type: application/json' \
  http://127.0.0.1:8088/jmap/ \
  -d '{"using":["urn:ietf:params:jmap:core","urn:ietf:params:jmap:mail"],
       "methodCalls":[["Mailbox/get",{"accountId":"<kasserar-jmap-id>","properties":["id","name","role","shareWith"]},"0"]]}' \
  | jq '.methodResponses[0][1].list[] | select(.role=="inbox")'
```

Check sync state in Postgres:

```bash
sudo -u postgres psql brygge -c \
  "SELECT address, applied_hash IS NOT NULL AS applied, last_error
     FROM mailbox_sync_state;"
```

Tail logs during reconcile / send:

```bash
journalctl -u brygge -f | grep -E 'inbox|jmap|reconciler|provisioner'
```

---

## Operational gotchas

- **First send from a mailbox** triggers Drafts + Sent folder creation. Watch for the `created Drafts folder on demand` / `created Sent folder on demand` INFO lines on the first reply — subsequent sends are silent.
- **Service-password regeneration**: `stalwart-mailbox-config.service` generates passwords on first run only (presence-check in the JSON map). Force a rotation by removing the address's entry from the file before redeploy.
- **JMAP IDs are NOT the admin REST IDs**. `Principal/get` returns alphabetic ids (`f`, `i`); admin REST returns numeric (`5`, `7`). The reconciler uses JMAP ids for `shareWith`; do not mix them. Cross-reference with `jmap_user` in `user_mail_credentials` for users.
- **Re-provisioning** (`TRUNCATE user_mail_credentials`): only DROPS the Brygge-side credential row. The Stalwart principal stays; `EnsureUserPrincipal` detects it (`principalExists`) and rotates the secret in place via PATCH. The user's JMAP id stays the same, so existing `shareWith` grants remain valid.
- **bcc_members fan-out**: each role member gets a personal copy of every outbound message. Off by default; enable per-mailbox in tfvars when the board wants the audit trail.

---

## Related

- [setup.md](setup.md) — base Stalwart setup, role mailboxes, deliverability foundations.
- [stalwart-internals.md](stalwart-internals.md) — Stalwart 0.15 protocol quirks (admin REST, JMAP, password hashing).
- [bimi.md](bimi.md) — BIMI logo rendering (depends on DKIM/DMARC from setup.md).
- Parent [DIL-275](https://linear.app/dillonteknisk/issue/DIL-275) — comprehensive session-context comment at the bottom captures everything discovered during DIL-276 → DIL-278.
