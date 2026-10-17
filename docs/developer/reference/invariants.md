# Invariants

The things that must never be violated. Agents respect what's explicit and invent what's not — these are the implicit rules that get silently broken otherwise.

Each invariant lists the **rule**, then the **failure mode** it prevents. When proposing code that touches the relevant subsystem, this is the first doc to consult.

---

## Accounting

### Closed fiscal periods are immutable

**Rule:** no journal entry can be created in, posted to, or voided into a period whose `fiscal_period_status` is `closed` or `locked`. The guard sits inside the service functions themselves, not only in their callers: `CreateJournalEntry` and `PostJournalEntry` refuse a non-open period, and so does `VoidJournalEntry` (BRY-333), all with `ErrPeriodClosed`. Handlers map that error to 409 with `code: "period_closed"`.

A void is **refused**. Its reversal never moves to a later open period instead, because a closed year's figures have been reported. To correct it, either reopen the period deliberately, or book a new correcting entry (e.g. a credit note) in the current period. Callers rely on that guard rather than a period pre-check of their own, which `ClosePeriod` could race. Callers that act on a group of entries (`ReverseInvoice`, `UnassignBankRow`) void them all in one transaction through `voidJournalEntryTx`; one entry in a closed period rolls back the voids before it, so the group is reversed completely or not at all.

Closing is serialised against writers. `CreateJournalEntry`, `PostJournalEntry` and `VoidJournalEntry` hold the period row `FOR SHARE` until they commit. `ClosePeriod` takes it `FOR UPDATE` before it counts drafts, so it waits for in-flight writers and then sees what they committed.

**Failure mode:**
* Silent corruption of a balance sheet that has already been reported to the styre or filed with Skatteetaten (a bokføringsloven §13 violation).
* Before BRY-333, `VoidJournalEntry` wrote its reversal into closed periods unchecked.
* A draft created while the period was being closed could land in it for good.

### Posted journal entries cannot be edited

**Rule:** once `journal_status` is `posted`, the only legal write is `VoidJournalEntry`. It marks the original `voided` and adds a balanced, posted reversal, and both stay in the GL. A posted entry is never updated or deleted: corrections are new entries (reversals, `invoice_resplit` reclassifications, credit notes). Only `draft` entries may be edited or deleted, and only drafts that no bank or Vipps row links to (`DeleteDraftJournalEntry`). Unassigning a row deletes its draft and voids its posted entry.

**Failure mode:** historical reports start disagreeing with themselves, and an auditor finds the GL doesn't tie back to the printed annual report.

### Accounting reads and writes are club-scoped

**Rule:** every service function that acts on an id from a request also takes the caller's club id and filters on it. This covers journal entries (get, post, void, delete-draft), accounts (update, deactivate, get), fiscal periods (close, reopen) and bank rows (assign, unassign). Another club's id is reported as not found (`ErrEntryNotFound`, `ErrAccountNotFound`, `ErrBankRowNotFound`, mapped to 404), so a foreign id reveals nothing. The "already booked" subqueries in KID matching also join on the faktura's club.

**Failure mode:** an admin or treasurer of one club posts, voids, reopens or renames another club's records by guessing a UUID (BRY-367). This was harmless while every deployment ran one club, but not in a multi-tenant one.

### An order-linked Vipps charge is booked from its order

**Rule (BRY-337):** a Vipps `belastning` whose `order_id` equals an `orders.vipps_reference` is booked only by `AssignVippsChargeToOrder`:
- CR 1500 for each `order_sources` faktura, with a payment back-link;
- CR the category revenue account for each catalog line.

It is refused when the split differs from the charge amount, or when a faktura on the order is already paid or voided. It is never classified by amount or payer name.

**Failure mode:** before BRY-337 nothing read the order reference. Pay-link payments stayed "betaling mottatt, venter oppgjør" for weeks: the money had been paid out, but the faktura never settled and the payout could not clear.

### Vipps charges are auto-booked only when unambiguous, and only on opt-in

**Rule (BRY-340):** `AutoAssignVippsCharges` runs after every Vipps CSV upload, operator Report API sync and **Synk Vipps**, but books nothing unless `clubs.vipps_auto_assign` is on. It books a charge in one of two cases. Anything else is left in Tildel.
- **Member faktura:** the payer resolves to one member by a strong pass (taught alias, KID, or the exact normalized full or first+last name; never last-name-only, fuzzy or email), that member has exactly one sent faktura not settled for the member (`finance.SettledForMemberSQL`), and the totals are equal.
- **Price item:** the amount fits active price items in one category. Several categories count as a tie. The club's `vipps_auto_assign_tie_category` resolves a tie only when every match is a per-unit (night, day or hour) price, for example guest slip versus motorhome at the same nightly rate. If the amount is also an exact one-off price in any tied category (a season, slipping, slip rent), the charge stays manual, so the tie category never absorbs amounts it can't explain. A members-only item also needs a strong member match. A member who has an open faktura for the exact amount is never price-classified.

Charges naming a checkout order are left to `AutoReconcileOrderCharges`. Each booking is audited (`vipps_charge.auto_assigned`) and can be undone with `UnassignVippsCharge`.

**Failure mode:** guessing on a weak name match or a cross-category amount books a member's dues as walk-up revenue, or the reverse. The member is then chased for a faktura they paid, or revenue lands on the wrong account. With no opt-in, a club that never chose this would find bookings it didn't make.

### Vipps postings are posted on creation; no period closes with drafts

**Rule (BRY-339):** every Vipps posting path posts its entry right away: per-charge charge→faktura and charge→account, the order-linked split, auto-assign, the payout's fee and bank entries, and the legacy `ReconcileVippsConfirm`.
- Each path marks its source row with a guarded update first, then posts. If the post fails, it unlinks the row and deletes the draft (`unwindVippsPosting`), so the charge returns to Tildel.
- The legacy `ResyncVippsBilags` re-creates existing drafts and keeps them as drafts. Posting a pre-existing backlog is a separate, supervised step: `PostVippsDraftBacklog` (BRY-341), per fiscal period, preview then fresh-TOTP apply. It refuses unbalanced or orphan drafts, drafts whose source row another posted entry already booked, and drafts whose faktura another posted entry already settled. It never posts a 1500 credit that isn't tied to a faktura: such a draft is always refused, never reclassified (BRY-370), because it may be the only record that a faktura was paid. Only a parked 3900 draft is rebooked by rule when auto-assign is on. Migration 000106 fills `vipps_import_rows.invoice_id` for legacy bookings where the `vipps_charge.assigned_invoice` audit row names the charge, its current entry and a still-paid faktura; everything else needs Angre and a fresh assignment in Tildel.
- `ClosePeriod` refuses (409 `period_has_drafts`) while the period holds any draft, and lists them per source with count and total. `DeleteDraftJournalEntry` removes a draft that no bank or Vipps row references. Linked drafts are undone through Tildel unassign.
- Posted entries are never hard-deleted. Undoing a posted charge voids it with a reversal (`UnassignVippsCharge`, `VoidJournalEntry`). Unassign checks the period first (a charge booked in a closed period is refused), then voids, and only then clears the row links and reopens the faktura (BRY-370). A failed void changes nothing; a crash after the void is completed by running Angre again.

**Failure mode:**
- Reports and momskomp count only booked entries, so draft Vipps postings left all Vipps income and fees out of the accounts. In prod that was 869 drafts.
- Closing a period with drafts strands them, because posting into a closed period is refused.

### Booked faktura bilags are corrected, never rebuilt

**Rule (BRY-364):** a posted `invoice_sync` entry is never deleted or edited. When its revenue split is wrong, `ResplitInvoiceBilags` posts an `invoice_resplit` entry for the invoice that moves the difference between revenue accounts. The difference is the lines' expected per-account split minus what the sync entry and earlier resplits booked. The run is idempotent, a preview posts nothing, and apply is behind fresh TOTP. An invoice whose booked revenue differs from its total is skipped with a reason for manual correction. The old `RebuildInvoiceBilags` (delete and re-sync) was removed.

**Failure mode:** deleting `invoice_sync` entries also deletes voided originals whose `manual` reversals survive, so reports go negative. It also erases the audit trail that bokføringsloven expects corrections to leave.

### An auto-match posts or leaves nothing

**Rule (BRY-324):** the KID auto-match in `ImportBankRows` and in the bank sync either creates **and posts** the journal entry and links the row, or it leaves no entry at all. Posting uses the importing user (`bank_imports.imported_by`) as `posted_by`. The row is linked first, with a guarded update (`journal_entry_id IS NULL`), and then the entry is posted (BRY-370). A lost race discards the draft; a failed post unlinks the row and discards it (`unwindVippsPosting`). Either failure is returned in the import result's `row_errors`, never swallowed. The row stays unmatched for manual Tildel. Concurrent syncs therefore book a row once.

- Every payment-match entry (KID, invoice number or counterpart, from import or sync) is sourced to its row (`source_table = 'bank_import_rows'`, `source_id` = the row) and described exactly `kidMatchDescription` (`Innbetaling KID <kid>: <row description>`, or `Innbetaling: <row description>` without a KID).
- A crash between link and post leaves the row linked to a draft. Bank sync releases such rows (`healStrandedKIDDrafts`) once the draft is older than 15 minutes and matches them again. It recognises them only by that source and exact description, so mapping-rule drafts from `AutoMatchImport`, which may also credit 1500 but carry the row's own description and wait for a gated post, are never touched.
- Manual assignments (`AssignBankRowToInvoice`, `AssignBankRowToAccount`) follow the same order: guarded link (`journal_entry_id IS NULL`, not dismissed, club-scoped), then post, then unlink and discard on a failed post. Of two concurrent assigns of one row, exactly one books.

**Failure mode:** before BRY-324 the importer passed the club id as `posted_by`. The users FK rejected it, and the error was swallowed. Every import-time match left an orphan draft, and the bank sync later booked the same row again, so the draft became a duplicate that would double-book the payment if posted. That left 56 in prod (230,831 kr), removed by migration `000100`.

### Bilag numbers are allocated with a retry, not a lock

**Rule (BRY-370):** a journal entry's number is `MAX(entry_number) + 1` within its club and period, read while the period is only share-locked. Two writers can read the same MAX, so the insert runs inside a savepoint: the loser hits the `(club_id, fiscal_period_id, entry_number)` unique key, rolls back to the savepoint and re-reads MAX, which by then includes the winner's committed number (`insertWithNextEntryNumber`, up to 25 attempts). Other unique violations are returned as they are.

**Failure mode:** without the retry, a bank sync and a manual void in the same period could collide on a number. The family reversal then rolled back with a 500.

### Reports count posted and voided entries

**Rule (BRY-352):** `VoidJournalEntry` sets the original to `voided` but keeps its lines, and adds a *posted* reversal. Every report and the momskomp calculation therefore select `je.status IN ('posted', 'voided')` (`bookedEntryFilter` in `sync.go`), so the original and its reversal net to zero. Only `VoidJournalEntry` sets `voided`, and only on a posted entry, so the filter means "was ever posted". Drafts are never counted.

**Failure mode:** counting only `posted` drops the original but keeps the reversal, so a voided entry shows as its own **negative**. A voided 2,500 kr faktura would push receivables and revenue down 2,500 kr instead of back to zero.

### A sent faktura has exactly one GL entry, booked at send time

**Rule (BRY-363):** sending a faktura (`HandleSendInvoice`, or `HandleCreateInvoice` with `send_email`) books it through `accounting.BookInvoice` in the same request, and voiding it (`HandleVoidInvoice`) reverses that entry through `ReverseInvoice`. Both use the same per-faktura code path as the repair sync (`bookInvoiceEntry`). A unique index (`idx_journal_entries_invoice_sync_once`, migration 000104) allows one `invoice_sync` entry per faktura; a voided entry keeps the slot, so a voided faktura is never re-booked. The email is the event that matters, so a booking failure never fails the send: it is returned as `gl_booking: {status: "failed", error}`, shown in the faktura list, logged, and picked up by **Avstem periode** (`SyncInvoices`). Voiding reverses the faktura's whole entry family, `invoice_sync` and every `invoice_resplit` correction (BRY-370), and audits `invoice.voided` with the reversal ids. If any entry of the family sits in a non-open period, the void is refused with 409 `period_closed` (credit note instead) and nothing is reversed, because the reversal would land in the closed year.

**Failure mode:** with booking only on a manual button, nothing was booked between 2026-05-14 and 2026-10-16 (13 fakturas, 84,031 kr missing from the reports). Two booking paths without the unique index could double-book a faktura on a race.

### Invoice GL sync books only sent, non-voided fakturas

**Rule (BRY-352):** `SyncInvoices` books a faktura only when `sent_at IS NOT NULL` and `status <> 'voided'`.
- A faktura voided after it was booked has its `invoice_sync` and `invoice_resplit` entries reversed on the next sync (`reverseVoidedInvoiceEntries`, within the open period being synced).
- Each line books to its own `account_id` when set. Otherwise it uses its price-item category's account, and failing that 3100.
- Signs drive the sides, so a credit note books DR revenue / CR 1500. A faktura that nets to zero books its lines without a 1500 line (a fully exempt faktura: CR revenue / DR the exemption expense account); one with no non-zero line is skipped with a reason.
- `SyncPayments` never books a payment that settles an invoice (`invoices.payment_id`). The bank or Vipps reconciliation that created it already booked DR bank / CR 1500.

**Failure mode:**
- Drafts and voided fakturas inflate receivables and revenue.
- Re-booking invoice-settling payments as DR bank / CR revenue doubles both (all 219 completed prod payments in 2026 were invoice-linked).
- Skipped items with no reason hide missing accounts or bad data.

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

### Anything that can post a journal entry is treasurer/admin with fresh TOTP

**Rule (BRY-370):** every accounting route that can create or post a journal entry — manual entries, post, void, delete draft, the GL syncs (payments, invoices, resplit, Vipps drafts), bank and Vipps imports, bank sync, Vipps resync and the bank-row Vipps reconcile confirm — goes through `moneyGate` in `cmd/api/main.go`: `RequireRole("treasurer", "admin")` plus `RequireFreshTOTPDefault()`. Previews stay read-only and ungated. The SPA calls `ensureFreshTotp()` when the button is clicked.

**Failure mode:** with only the accounting-router role check, a board member with a stale session could post or void entries, import statements that auto-book, or run syncs that book a whole period.

### Board viewing never widens a write

**Rule (BRY-372):** every board role and admin (`roles.CanViewAdmin`) may read every admin page except Developer and Site. Reads open through `middleware.RequireRoleOrBoardRead(<write roles>)`, which admits viewers only on `GET`/`HEAD` and applies `RequireRole(<write roles>)` to every other method, so a surface's write roles are the ones it had before. A handler mounted on `GET` behind it must not write, mint a credential or send anything; such a `GET` keeps its own `RequireRole` (the pay-link endpoints do). In the SPA, write controls on viewable pages hide behind `useCan()`.

**Failure mode:** swapping a group's `RequireRole("board", …)` for a plain viewer check would hand `chair`, `secretary` or `deputy` every `POST`/`PUT`/`DELETE` that relied on the group gate (account create, rules, momskomp save, slip edits). The read helper exists so opening reads cannot do that.

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

### Settled means one thing, defined once

**Rule (BRY-325):** whether an invoice still needs paying is decided only through `internal/finance/settled.go`:
- **`SettledForMemberSQL`** returns true for `payment_id IS NOT NULL OR vipps_paid_at IS NOT NULL OR status = 'voided'`, or when a live credit note credits the invoice (`CreditedSQL`, BRY-342).
- **`OwedByMemberSQL`** is `IsDebtSQL` (a positive total) and not settled for the member. It answers "is the member chased for this?" and is used by reminders (both enqueue *and* the worker's send-time check), KID reissue, the member payables page and "unpaid" filter, checkout totals, the Vipps-blocked check, Vipps auto-assign's open-faktura lookup, and the Økonomi overview's overdue figures.
- **`SettledForGLSQL`** returns true for `payment_id IS NOT NULL`. It is used where the receivable must actually be cleared: reconciliation and bookkeeping.
- **Automatic matching of incoming money** to a faktura (bank KID and invoice-number lookups at import and in the bank sync, the counterpart-name match, the Vipps member match) uses `OwedByMemberSQL` (BRY-370). Money for a voided, Vipps-captured, credited or paid faktura is an overpayment a human must see, so the row stays in Tildel.
- **`OpenReceivableSQL`** (a debt, not voided, no `payment_id`, not credited) is the one exception: ePayment order reconciliation books the capture of a faktura that the webhook already marked `vipps_paid_at`, so it must not treat that flag as settled.

Voiding an invoice that is settled for the member (paid, or captured via Vipps) is refused with 409, because money arrived and it needs a refund or credit note instead. A credited faktura is refused with `code: credited` and the credit note's number (unlink first). The void's UPDATE repeats every check in its WHERE clause, so a payment or credit link that lands mid-request returns 409 `changed` and nothing is audited or reversed.

**Failure mode:**
- Settlement lags the Vipps capture by days. A check on `payment_id` alone chases members who already paid through the pay link. The reminder worker did exactly that at send time until BRY-325.
- A voided paid invoice hides received money that nothing is owed against.

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

### A role exemption is an offsetting line, booked gross

**Rule (BRY-351):** a club may exempt a board role (`roles.Board`, the one canonical list) from a price item or a whole price category (`price_exemptions`). Nothing is exempt by default. When a faktura is generated, bulk or single, for a member who holds an exempting role, the item stays on the faktura at full price and an offsetting negative line follows it:
- The offset line carries the item's `price_item_id`, `exemption_role`, a NULL `category` (so the per-line duplicate check still sees only the real line) and `account_id` = the club's `exemption_expense_account`. GL booking honours `account_id` over the category, so the faktura books **gross**: CR the item's revenue, DR the expense account. The waived revenue is never netted away.
- Exemptions apply only while the expense account resolves to an active `expense` account. Otherwise nothing is exempted and the run returns a warning, so a misconfiguration bills in full rather than booking to the wrong account.
- Roles are read when the faktura is generated. Removing a role makes the next run bill normally; existing fakturas are not touched.
- The bulk preview (`preview: true`) lists every match with a `key`; `skip_exemptions` takes those keys and bills the unticked ones in full. The key is `<user_id>:<price_item_id>`, plus `:<boat_id>` on a boat-bound line, so one boat of a multi-boat member can be unticked on its own. A single faktura can opt out with `skip_exemptions: true`.
- A bulk run reads the exemption config (rules and expense account) **once, at its start**. A settings change saved while a run is in progress takes effect from the next run; the running batch keeps applying the snapshot it started with.
- Exemptions only offset positive lines. A credit note (negative total) and any line with `quantity × unit_price ≤ 0` get no offset, which would otherwise be positive and cancel the credit.
- The per-item overview reports `billed` gross and the exemption lines separately as `exempted`; outstanding is `billed − exempted − received`, and outstanding counts only fakturas members still owe.
- The member portal reads `owed` (`OwedByMemberSQL`): a 0 kr faktura shows as nothing to pay, never unpaid or overdue, and its KID is hidden.
- A faktura whose total is 0 is **settled on issue**: `IsDebtSQL` is `total_amount > 0`, so it is never owed, overdue, reminded or on the payables page, and `vippsAllowedExpr` never offers Vipps (`total_amount <= 0 → false`). It still gets a KID (the column is NOT NULL and unique), but the PDF and email hide it and say nothing is due. Its GL entry has no 1500 line.
- Settings writes (`PUT /settings/economy/exemptions`) are treasurer/admin with fresh TOTP and audited as `pricing.exemptions_updated`.

**Failure mode:** without the offset line, exempt members were billed and then manually credited or waived off-book, so the waived amount disappeared from revenue and expense alike. Netting the item price to zero instead would hide what the exemption costs the club.

### A credit note is never a debt, and a credit link never posts

**Rule (BRY-342):** an invoice with a negative total is a credit note. It is never overdue, never reminded and never on the payables page (`OwedByMemberSQL`). A credit note settles the faktura it credits only through an explicit link, `invoices.credits_invoice_id`, set by an admin (`POST …/credit-link`, fresh TOTP). Nothing pairs them automatically, because several open fakturas can share a payer and amount.
- The link is refused unless both invoices are live and issued (sent or imported), have the same payer (member or billing recipient), and the credit equals the faktura's total. Partial credits are refused, because every "amount owed" path would have to net them.
- A faktura is credited by at most one live credit note (`idx_invoices_credits_once`). The link re-checks inside its transaction, after locking, and maps the unique violation to 409 `already_credited`, so two concurrent links settle once.
- **The link never posts to the GL.** A booked credit note's own entry (DR revenue / CR 1500) already offsets the faktura's receivable, and imported history has no bookings on either side. A pair where only one side is booked is refused (`booking_mismatch`), with one exception: a booked Brygge credit note may settle an *imported*, unbooked faktura. The imported receivable lives in the opening balance on 1500, so the credit note's own CR 1500 is what clears it; posting anything at link time would clear it twice.
- That exception holds only once the receivable is actually on 1500: a posted `opening_balance` entry (source added in 000107, BRY-370) that debits 1500, dated **on or after the faktura's issue date** (an earlier opening balance can't contain it) and on or before the credit note. Without one the link is refused with `booking_mismatch` naming the missing opening balance, because the credit note's CR 1500 would otherwise drive 1500 negative.
- **Nothing writes `source = 'opening_balance'` yet.** The journal API always books `manual`, so until the cutover import in BRY-365 posts the opening balance with that source, the exception never applies and every booked-credit-note-against-imported-faktura link is refused.
- The opening receivable is computed **at the cutover date**: every imported faktura still owed on that date, including ones a later Brygge credit note credits. Leaving the credited ones out (because they look settled today) double-counts the credit: its CR 1500 then clears a receivable that was never opened.
- A faktura that was already paid when it was credited makes the credit note **credit owed to the member** (refund or carry-forward). It is shown as such, never as overdue.
- A linked credit note can't be voided until the link is undone, so a void can't silently reopen the faktura.

**Failure mode:** before BRY-342, 19 imported credit notes (−27,700 kr) counted as overdue debts. Five open pairs each showed both halves as overdue, even though they net to zero.

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

### Backups are dumped with the server's own Postgres major version

**Rule:** `nix/backup.nix` takes `pg_dump` from `config.services.postgresql.package`, never an unpinned `pkgs.postgresql`. A dump has to be restorable with the server's own `pg_restore`.

**Failure mode:** the backup scripts used `pkgs.postgresql`, which follows nixpkgs (17 by October 2026) while the server ran 16. Every nightly and pre-deploy dump was written in archive format 1.16, which the server's `pg_restore` 16 rejects ("unsupported version (1.16) in file header"). A disaster restore on the host would have failed. Found by `migrate-check-prod` on 2026-10-16.

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
