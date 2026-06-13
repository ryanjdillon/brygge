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

### Bulk-reminder queue is in-process, not persistent

**Rule:** `HandleBulkSendReminder` validates rows synchronously and enqueues each eligible job onto an in-memory channel on the `InvoiceHandler`. A single background goroutine drains the queue at `cfg.BulkSendThrottle` (default 1s) per send. The queue is **not persisted** — on an API restart, every pending reminder is lost. Operators choosing to redeploy mid-batch must accept that the in-flight reminders won't resume; re-running the bulk action is the recover path (already-sent rows skip themselves via the `payment_id IS NOT NULL` / audit-driven dedup logic on retry).

**Failure mode:** a deploy or crash during a 100-row send loses the un-drained tail. Per-row audit rows show which made it through, so the operator can re-select the rest on the Sent tab and click "Send purring" again. DIL-388 captures the Phase 2 plan (persistent queue) if this becomes a real pain point.

---

## Sessions

### Sliding idle window vs absolute cap

**Rule:** Sessions have a 12-hour sliding idle window and a 7-day absolute cap (`sessionIdleWindow`, `sessionAbsoluteCap` in `internal/auth/session.go`). The cookie MaxAge tracks the absolute cap. `ValidateSession` enforces both bounds; the idle extension is throttled (`sessionExtendThreshold`).

**Failure mode:** sessions that live forever, or sessions that get killed without warning before the user's done.

---

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

## Developer tokens (DIL-365 — pending)

### Token DB role cannot SELECT its own evidence

**Rule:** `brygge_dev_ro` Postgres role gets `SELECT` on `public.*` EXCEPT `audit_log` and `developer_tokens`. A leaked token must not be able to enumerate or erase its own audit trail.

**Failure mode:** leaked token covers its tracks, blast radius becomes unbounded.

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
