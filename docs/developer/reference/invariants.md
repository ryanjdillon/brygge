# Invariants

The things that must never be violated. Agents respect what's explicit and invent what's not — these are the implicit rules that get silently broken otherwise.

Each invariant lists the **rule**, then the **failure mode** it prevents. When proposing code that touches the relevant subsystem, this is the first doc to consult.

---

## Accounting

### Closed fiscal periods are immutable

**Rule:** No journal entry can be created against, or voided into, a period whose `fiscal_period_status` is `closed` or `locked`. `accounting.Service.resolvePeriod()` enforces this — never bypass it by querying the period yourself.

**Failure mode:** silent corruption of a balance sheet that's already been reported to the styre or filed with skatteetaten. Bokføringsloven §13 violation.

### Posted journal entries cannot be edited

**Rule:** Once `journal_status` is `posted`, the only legal write is `VoidJournalEntry`, which creates a balanced reversal entry rather than mutating lines. `draft` entries can be edited or deleted freely.

**Failure mode:** historical reports start disagreeing with themselves. Auditor finds the GL doesn't tie back to the printed annual report.

### Bank rows with booked journals require cascade on reassign

**Rule:** A `bank_import_rows.journal_entry_id` that is not NULL means the row has been booked. Reassigning the parent `bank_imports.bank_account_code` must cascade through `journal_lines` to update the booked account. `HandleReassignBankImport` does this in a single transaction; closed/locked/voided journals refuse the cascade.

**Failure mode:** ledger says money is in account A, bank statement says it's in account B. Reconciliation breaks silently.

### Invoice PDFs are retained for 5 years

**Rule:** Bokføringsloven §13 requires invoice documents to be retained for 5 years in the form the recipient was shown. `regenerateOnePDF` archives `invoices.pdf_data` into `invoice_pdf_archive` *before* overwriting, inside the same transaction. If the archive INSERT fails, the transaction rolls back and the original PDF stays in place.

**Failure mode:** legal exposure on a member dispute. No way to prove what the recipient was actually shown.

### Invoices snapshot the member's identity at issue

**Rule:** Every invoice stores `member_name` / `member_email` captured when it is created (historical rows are backfilled). Display and PDF-regeneration paths read the snapshot first — `COALESCE(NULLIF(recipient_org_name,''), NULLIF(member_name,''), users.full_name, …)` — so anonymizing or deleting the member never rewrites the name/email on their past invoices.

**Failure mode:** a member is anonymized and all their historical invoices suddenly read "Deleted User", breaking the bokføringsloven §13 requirement that the record show who was billed.

### A member with in-window financial records is deactivated, never erased

**Rule:** "Removing" a member sets `users.deactivated_at` (reversible; hides them from active lists and member counts, blocks login, drops sessions) — it does *not* delete the row or scrub PII. Their name/email/address are erased only after their most recent invoice clears the retention window (1 January of the record's year + 6), by the daily sweep `accounting.PurgeExpiredMemberPII` or a manual erase. `HandleDeleteUser(?erasure=true)` refuses while records are in-window and downgrades to anonymize-in-place once cleared (the row must survive — invoices reference it with no `ON DELETE` cascade). `accounting.MemberFinancialRetention` is the single source of truth for eligibility — never hand-roll the date math or the `deactivated_at IS NULL` list filter.

**Failure mode:** either an erase/GDPR action destroys records the club must keep for 5 years (FK violation or lost history), or a member's PII lingers indefinitely past the point erasure is legally required.

### Sent fakturas keep their issue-time kontonummer

**Rule:** A sent faktura's PDF is whatever bytes are stored in `invoices.pdf_data` at delivery time. Switching the club's `is_default_for_invoices` bank account does NOT retroactively change the kontonummer on already-sent PDFs. To correct a batch, the operator runs `Regenerer PDF` from the Sent tab, which archives the original and rebuilds with the current default.

**Failure mode:** member pays the wrong account. Or worse, an auditor finds two versions of the same invoice with different kontonr and the audit trail can't explain why.

### Vipps cascade lands on 3900, never 2900

**Rule:** Unmatched Vipps `belastning` lines route to `vippsFallbackRevenueCode = "3900"` (Andre inntekter). The legacy `vippsClearingAccountCode = "2900"` is kept as a Go constant for backwards-compatible reports but no new lines route there.

**Failure mode:** 2900 (Annen kortsiktig gjeld) implies "we owe the payer money," which is wrong for revenue we just haven't classified yet. Treasurer's balance sheet lies.

### Vipps row amount signs

**Rule:** `vipps_import_rows.amount` is positive for `belastning` (customer paid us) and negative for `fee` (Vipps took their cut). The fee branch flips the sign for the DR line.

**Failure mode:** bilag doesn't balance, reconciliation 500s.

---

## Auth

### Recovery codes are bcrypt-hashed and single-use

**Rule:** `totp_recovery_codes.code_hash` is bcrypt; `used_at` is monotonic. Consumption is irreversible — there's no "un-use" path. Re-issuing recovery codes wipes the unused ones.

**Failure mode:** a leaked code that's already been used grants admin access. Or a "convenience" code-reuse feature creates a permanent bypass.

### TOTP enrollment requires QR scan + confirm

**Rule:** The TOTP secret is persisted only after the user confirms a code derived from it. The QR-scan step shows the secret; the confirm step proves the secret was registered with an authenticator. Both steps are required — never persist a secret without confirmation.

**Failure mode:** user thinks they enrolled, can't verify, is locked out. Or worse: secret leaked from logs while the user never actually scanned it.

### Replacing a second factor requires a fresh step-up

**Rule:** a user who already has a second factor (TOTP or passkey) can only add, replace or remove a factor after a fresh step-up. TOTP `setup`/`confirm` sit behind `RequireFreshTOTPIfEnrolled`; passkey register/delete sit behind `RequireFreshTOTPDefault`. Only a user with **no** factor may enroll on a plain session.

**Failure mode:** enrollment stamps the session as stepped-up. If replacing a factor needed only a session cookie, a stolen cookie could enroll the attacker's own secret and then pass every admin and fresh gate (BRY-298).

### `totp_verified_at` is the source of truth for step-up freshness

**Rule:** Both `RequireFreshTOTP` and the SPA's countdown logic compute "is this fresh?" from `sessions.totp_verified_at`. Client-side timers are advisory only. The backend always reverifies on every gated request.

**Failure mode:** client clock skew lets a stale session bypass step-up. Or a tampered client gets to claim freshness it didn't earn.

### TOTP middleware runs after AuthenticateSession

**Rule:** `RequireFreshTOTP` and `RequireAdminTOTP` both read `SessionInfo` from the request context, which `AuthenticateSession` populates. Reversing the order produces nil-pointer dereferences in dev and silent admin-bypass in prod (since the gate runs against an empty session).

**Failure mode:** a confused middleware chain admits a request that shouldn't have been admitted.

---

## Invoicing

### `invoices.kid_number` is unique per club

**Rule:** `idx_invoices_kid` enforces `UNIQUE (club_id, kid_number)`. KID generation reserves the next sequence number atomically before issuing. The KID encodes club + sequence + check digit.

**Failure mode:** two invoices with the same KID — bank can't tell them apart, reconciliation pairs the wrong one.

### `invoices.payment_id` is set only when a payment row exists

**Rule:** Either both `invoices.payment_id IS NOT NULL` and the referenced `payments.id` exists, or both are NULL. Never write `payment_id` pointing at a non-existent UUID, and never delete a `payments` row that any `invoices.payment_id` references.

**Failure mode:** dangling foreign key. Dashboard says paid; member says they didn't pay; nobody can explain the discrepancy.

### Voided invoices stay in the table

**Rule:** `invoices.status = 'voided'` is terminal. Voided rows are excluded from receivables totals but remain in the table for audit. Never DELETE a voided invoice.

**Failure mode:** lost paper trail when an auditor asks why invoice #87 was voided.

### Invoice line snapshots are immutable

**Rule:** `invoice_lines.description`, `unit_price`, `line_total`, `category` are denormalized from `price_items` at issue time. Updating `price_items.amount` does NOT rewrite existing `invoice_lines`. Historical fakturas reflect the prices that were in effect when they were issued.

**Failure mode:** a price-list correction silently changes amounts on already-issued invoices. Treasurer's totals start drifting.

### A referenced price item is archived, never deleted

**Rule (BRY-360):** `HandleDelete` on `/admin/pricing/{id}` hard-deletes a price item only when no `invoice_lines`, `invoices`, `order_lines` or `harbor_payment_targets` row references it. Otherwise it returns 409 `price_item_in_use`, and the SPA offers to archive it instead (`is_active = false`). Archived items are excluded from the faktura pickers, bulk invoicing, the public price list, the QR checkout catalogue, and the public QR target lookup (`loadQRTarget`). A printed poster for an archived item therefore stops taking payments.

**Failure mode:** `order_lines.price_item_id` and `invoices.price_item_id` are `ON DELETE SET NULL`. A delete that succeeded would silently strip the category from historical orders and invoices, and with it the revenue-account mapping used by GL sync and reconciliation. The other two FKs fail the delete, which used to surface only as a generic 500.

### Bulk-reminder queue is durable, throttled, and retried

**Rule:** `HandleBulkSendReminder` validates rows synchronously and enqueues each eligible reminder as an `invoice_reminder` row in the shared `deliveries` table (the same durable queue broadcasts use). The delivery worker drains it on a background context at `cfg.BulkSendThrottle` (default 1s) per send, retries transient SMTP failures up to a cap, and survives restarts (a boot sweep re-claims rows orphaned mid-send). Enqueue is idempotent per invoice: an invoice that already has a non-failed reminder queued is skipped (surfaced to the operator as "already queued"), so a re-run or double click can't double-send.

**Failure mode:** a deploy or crash during a 100-row send no longer loses the un-drained tail — the worker resumes on restart. Per-row `invoice.reminded` audit rows still reflect progress on the Sent tab's "Sist purring" column. Reminders whose invoice was paid or deleted between enqueue and send are treated as terminal no-ops (marked sent, not retried).

### Vipps is offered per the tri-state rule (override, else amount cap)

**Rule (BRY-283):** whether a "Betal med Vipps" link/payment is offered for an invoice is the single expression `vippsAllowedExpr` (in `order_pay.go`):
- `invoices.vipps_payment_link_opt_in IS TRUE` (On) → **always** offered, even above the cap;
- `... IS FALSE` (Off) → **never**;
- `... IS NULL` (Auto) → offered iff `clubs.vipps_max_amount IS NULL` (no cap) or `total_amount <= vipps_max_amount`.

So the amount cap is a **default**, not a hard ceiling — the per-invoice override wins either way. It is enforced at **both** points, because `orders.token`s are permanent and one exists for every sent faktura: at **issuance** in `ensureFakturaOrderToken` (returns `""` when not allowed, before the token-reuse lookup — so faktura/reminder emails, the admin copy-link, and the payables page all go KID-only) and at **mint** in `mintOrderPayment` (returns `errVippsBlocked` via `orderHasVippsBlockedSource` — so a link already in an inbox, or issued before the cap/override changed, can't be charged). The reminder batch's `vipps_payment_link` persists On/Off onto the reminded invoices (it sets the same column), so it too is honored at the member's eventual click.

**Failure mode:** enforcing at issuance alone would leave every large invoice already emailed fully Vipps-payable. Not-allowed invoices simply never get an `orders`/`order_sources` row minted, which is safe — reconciliation and the webhook key off `orders.vipps_reference`, created only on mint.

### Only the payment provider can mark an order paid

**Rule:** an order (or the invoice behind it) becomes paid only from evidence that money moved: the signed Vipps webhook (`onCaptured`), or reconciliation against a settlement or bank row. No HTTP route, public or admin, sets `orders.status = 'paid'` or `invoices.vipps_paid_at`/`payment_id` from a caller's say-so.

**Failure mode:** a "confirm" endpoint that flips status on request lets anyone holding an order id mark it paid with no money received. That is what the pre-BRY-259 `POST /orders/{id}/confirm` stub did; it was removed in BRY-306.

---

## Sessions

### Sliding idle window vs absolute cap

**Rule:** Sessions have a 12-hour sliding idle window and a 7-day absolute cap (`sessionIdleWindow`, `sessionAbsoluteCap` in `internal/auth/session.go`). The cookie MaxAge tracks the absolute cap. `ValidateSession` enforces both bounds; the idle extension is throttled (`sessionExtendThreshold`).

**Failure mode:** sessions that live forever, or sessions that get killed without warning before the user's done.

---

## Deploy

### No migration runs without a pre-deploy snapshot

**Rule:** `nix run .#deploy` (and so `just deploy`) takes a fresh `pg_dump` on the host via `brygge-pre-deploy-backup.service` before deploy-rs activates the new system, which is when migrations run. If the snapshot fails, or no dump newer than this deploy appears, the deploy aborts. `SKIP_BACKUP=1` is the only bypass and is announced. The snapshot directory is created by tmpfiles on the host, and pre-created by the deploy script for hosts whose config predates that rule. The unit's `ProtectSystem=strict` + `ReadWritePaths` fails with exit 226 when the path is missing.

**Failure mode:** a deploy script that swallows the backup error ("skipped (service not found on host)") ran every migration with no rollback point while the operator believed one existed (BRY-304). A system rollback does not undo a migration.

## Config

### Env vars are trimmed at the boundary

**Rule:** `cleanBaseURL` and friends in `internal/config` strip surrounding quotes, whitespace, and trailing slashes from string env vars before storing them. Magic-link URL generation depends on this — a trailing quote in `FRONTEND_URL` produced broken links in production until a regression test got added.

**Failure mode:** silent breakage of any feature that concatenates URLs.

### `TOTP_ENCRYPTION_KEY` rotation is destructive

**Rule:** The 32-byte hex-encoded key encrypts every TOTP secret in the DB. Rotating it without a re-encrypt migration makes every existing enrollment unrecoverable.

**Failure mode:** every admin gets locked out of 2FA. Manual DB intervention required to recover.

### `audit_log.resource_id` is TEXT, not UUID

**Rule:** `audit_log.resource_id` is a `TEXT` column even though every callsite passes a UUID. When joining `audit_log` against a UUID column (e.g. `invoices.id`), explicitly cast — `resource_id = invoices.id::text` — or Postgres returns `ERROR: operator does not exist: text = uuid`.

**Failure mode:** the query throws 500 the first time it runs against a non-empty audit log. The `last_notified_at` subquery on `HandleListInvoices` got bitten by this in commit `b8573c4`; fix in the follow-up.

---

## Developer DB query (DIL-365)

### Query role is default-deny: masked views + a non-PII allowlist

**Rule:** `brygge_dev_ro` reads only (a) the PII-masking `dev_masked.*` views and (b) an explicit allowlist of base tables with no person-identifying free text (migrations `000095`, narrowed by `000097`). Free text counts as PII even on accounting tables, because bank-import entries copy the payer's name into descriptions (BRY-336). Everything else — `audit_log`, credential tables, and any newly-added table — is denied until explicitly classified. The full deny/allow/mask classification is the source of truth in [`../security/dev-query.md`](../security/dev-query.md), not `accounting/purge.go`. Identity, credential and free-text columns in the masked views are `NULL`, never partial-masked (a partial value plus the opaque id is a reversible membership oracle in a small club); correlation keys pass through verbatim. Because the role can never read raw PII, a full-SQL audit of its queries contains no PII by construction.

**Failure mode:** a new table auto-granted to the role (the pre-BRY-286 default-allow) silently exposes PII/secrets; a leaked session enumerates or erases its own `audit_log` evidence and the blast radius becomes unbounded.

### Name tokens are keyed, function-free, and only from names the agent can't write

**Rule:** the dev-query role sees names only as keyed HMAC pair tokens (BRY-343).
- **The key:** `dev_private.token_key` is never granted to `brygge_dev_ro`. Views read it through a scalar subquery, never through a function or an IMMUTABLE accessor, and no function that reaches `dev_private` may be executable by the role.
- **What gets tokenised:** only names the agent has no way to write. Data from public, unauthenticated inputs (such as `orders.guest_name` from the QR pay endpoint) is never tokenised.
- **Granularity:** tokens cover unordered word pairs, never single words.

**Failure mode:** a readable key, or any callable tokeniser, lets the role compute the token for a guessed name and confirm who someone is. A tokenised public input does the same by another route: submit a name, then read its token back. Single-word tokens re-identify people by frequency (a few common local surnames).

### Developer tokens are hashed, scoped and role-less

**Rule:** developer bearer tokens (BRY-115) are stored only as `sha256(token)`, and the plaintext is returned once at mint. `RequireDevToken` is the **only** middleware on `POST /api/v1/dev/query` and is mounted on no other route. It ignores cookies and injects claims with **no roles**, so a token can never satisfy `RequireRole` or reach a session route. The route exists only when `DEV_QUERY_TOKENS_ENABLED=true`. `developer_tokens` is never added to the `brygge_dev_ro` allowlist.

**Failure mode:** a token mounted on a broader route, or carrying the creator's roles, turns a short-lived read credential into an admin session that bypasses TOTP. A plaintext token at rest, or a readable `developer_tokens` table, turns one DB read into token theft.

### Query role is a real privilege boundary, not `SET ROLE`

**Rule:** the `/admin/dev/query` endpoint runs on a dedicated pool that
*authenticates as* `brygge_dev_ro` (a `LOGIN` role, `DEV_QUERY_DATABASE_URL`),
never via `SET ROLE` on the `brygge` connection. `brygge_dev_ro` is **not** a
member of `brygge`, so a session has no higher role to return to. The role's
read-only / timeout / `search_path` GUCs are pinned with `ALTER ROLE`.

**Failure mode:** `SET ROLE` is reversible by the session user —
`SELECT set_config('role','none',true), query_to_xml('select … from users', …)`
resets to the owning superuser and runs arbitrary SQL via SPI, so `SET ROLE`
was never a boundary (BRY-285). Granting `brygge_dev_ro` membership to `brygge`
re-opens the same hole.

---

## When to add a new invariant

When you fix a bug that traces back to "we didn't explicitly say X must hold," add an invariant here. The doc is append-only — invariants don't get retired, they get tightened.

The format is:

```markdown
### One-line statement

**Rule:** Concrete description of what the code does to enforce it. Name the enforcing function/middleware/index so the next person knows where to look.

**Failure mode:** What goes wrong if you violate it. Specific symptom > abstract concern.
```

Keep failure modes specific — "data corruption" is too abstract for an agent to recognize. "Two invoices with the same KID; bank can't tell them apart" is concrete enough that pattern-matching it during review actually catches the bug.
