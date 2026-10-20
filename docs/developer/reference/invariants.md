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
- **Member faktura:** the payer resolves to one member by a strong pass (taught alias, KID, or the exact normalized full or first+last name; never last-name-only, fuzzy or email), and `decideMemberInvoice` settles it — see "A payment goes to a member's faktura only when it is the only one it can be" below.
- **Price item:** the amount fits active price items in one category. Several categories count as a tie. The club's `vipps_auto_assign_tie_category` resolves a tie only when every match is a per-unit (night, day or hour) price, for example guest slip versus motorhome at the same nightly rate. If the amount is also an exact one-off price in any tied category (a season, slipping, slip rent), the charge stays manual, so the tie category never absorbs amounts it can't explain. The chosen category must then fit the amount as a single unit: an amount that fits only as several nights (2,700 = 9 × 300) stays manual (BRY-398), so the tie rule only ever settles single-unit amounts. A members-only item also needs a strong member match. A member who has an open faktura for the exact amount is never price-classified. That includes an imported faktura (`import_source` set): an open imported faktura of the same amount sends the charge to review rather than to a price item or another faktura.

Charges naming a checkout order are left to `AutoReconcileOrderCharges`. Each booking is audited (`vipps_charge.auto_assigned`) and can be undone with `UnassignVippsCharge`.

**Auto-booking requires a faktura or an exact single-unit price match (BRY-398).** Every path that books without a human — `decideVippsCharge` (auto-assign and the `PostVippsDraftBacklog` rebook) and `matchVippsAmountToPriceArticle` (the legacy cascade and resync) — treats an amount that fits a per-night/day/hour price only as a multiple as undecided. Multiples stay suggestions: `priceItemAccountSuggestions` shows them, never above `svak`.

**Failure mode (multiples):** 2,700 kr fits 9 × 300 kr a night, so it was booked to motorhome revenue, but it was season rent no price item lists. A multiple is a guess at how many nights were paid for, and nothing in the charge confirms it.

**Failure mode:** guessing on a weak name match or a cross-category amount books a member's dues as walk-up revenue, or the reverse. The member is then chased for a faktura they paid, or revenue lands on the wrong account. With no opt-in, a club that never chose this would find bookings it didn't make.

### KID-less bank deposits are auto-booked only when unambiguous, and only on opt-in

**Rule (BRY-366):** `AutoAssignBankDeposits` runs at the end of every bank import (upload and both reimports) and as pass 4c of `BankSync`, but books nothing unless `clubs.bank_auto_assign` is on. That switch is separate from `vipps_auto_assign`. Candidates are incoming rows that are unbooked, undismissed, not refund-paired, not a Vipps payout and not provisional (`partnerEligibleSQL`), carry none of our KIDs (`isBryggeKID`) and are not a bounced transfer. A row stays in Tildel, with the first reason it fails:
- `has_reference`: the description carries a KID or an invoice number; the KID cascade owns it.
- `no_payer`: no `Fra:` name in the description and no counterpart.
- `no_period` / `period_closed`: its date (or the import's chosen period) is not in an existing open fiscal period. The rule never creates a period. Checked after the row-only reasons, so a row that could never be auto-booked is counted as such.
- `own_club`: the payer is the club's own account, or its name when no account is known.
- `no_strong_member`: no taught alias and no unique exact name.
- the `decideMemberInvoice` reasons (see below).
- `has_partner`: the row has a strong BRY-380 partner.
- `possible_double_payment`: two or more rows in the run reach the same faktura; none is booked.

A booking goes through `assignBankRowToInvoice` with the faktura locked (`lockInvoiceForAssign`: `FOR NO KEY UPDATE`, held until the row is linked and posted). Under the lock it re-checks that the faktura is still owed, sent, not imported, has nothing booked against it and equals the amount, so a racing manual assign or a second run books at most once. The single-row and split-payment manual assigns (`AssignMultipleBankRowsToInvoice`) and the Vipps assign (`AssignVippsChargeToInvoice`, used by Vipps auto-assign) take the same lock and refuse a faktura another booked bank row or Vipps charge already points at, so a Vipps and a bank payment can't both settle one faktura. The KID cascade (`ImportBankRows`, `syncKIDMatches`) takes it too and records `invoice_id` on its row, so every path that settles a faktura takes the lock (see "An auto-match posts or leaves nothing"). The entry is described `Automatisk tildeling: …`, records `invoice_id`, and keeps `auto_matched = false` like every Tildel booking: `healStrandedKIDDrafts` only releases KID drafts, so an `auto_matched` rule booking stranded between link and post would never be healed. Provenance is the `bank_row.auto_assigned` audit row. There is no unattended bank sync; without an acting user the rows are reported as `no_actor`. Undo is `UnassignBankRow`, and the next sync books the row again if it is still unambiguous.

While `bank_auto_assign` is off, the KID cascade's third step (`findInvoiceByCounterpartAndAmount`: full name + amount, no aliases or other guards) still runs at import and sync. When it is on, that step is skipped and this rule replaces it. `GET /admin/accounting/bank-rows/auto-assign/preview` projects the rule as if on, writes nothing and returns no names.

**Failure mode:** the ungated name+amount step booked a deposit to whichever member's faktura matched, with no opt-in, no aliases and no check for a payment made before the faktura, a partly paid faktura or a member with two open fakturas. Turning a replacement on at deploy would have changed live bookings before anyone saw what it would do.

### A payment goes to a member's faktura only when it is the only one it can be

**Rule (BRY-340, BRY-366):** every rule that books a payment to a member's faktura without a human (Vipps auto-assign and bank auto-assign) goes through two shared steps in `auto_assign_member.go`:
- **One member.** A name pass (`matchMemberName`, passes full name and first+last) is strong only when the normalized name belongs to exactly one member. A name two members share is no strong match; resolution falls through to the weak passes, which the auto-assign rules never act on. A taught alias wins over every name pass. For Vipps, a KID in the message that resolves to a member wins over the alias, because it names the faktura being paid; when the KID and the alias name different members, `resolveCustomerToMemberPass` returns `memberPassConflict` with no member, and Vipps auto-assign leaves the charge in the queue rather than booking it to either member or classifying it by amount (BRY-383). The other callers of `resolveCustomerToMember` see the same uniqueness: the legacy payout cascade (`ReconcileVippsPreview`) books a member match to 1500 for a human to reconcile, so a shared name there now falls to the weak passes too, and the read-only `vippsaudit.go` report matches the same way.
- **One faktura** (`decideMemberInvoice`). Candidates are the member's sent fakturas still owed (`finance.OwedByMemberSQL`). The payment is booked only when exactly one candidate was issued on or before the payment date, nothing is booked against it yet (no booked bank row with its `invoice_id` or its KID, no booked Vipps charge with its `invoice_id`), and its total equals the amount in integer øre. Imported fakturas (`import_source` set) are never targets, because they have no GL receivable; an imported open faktura of the same amount leaves the payment for a human. Reasons: `no_open_faktura`, `several_open_fakturas`, `amount_mismatch`, `faktura_after_payment`, `faktura_partly_paid`, `imported_faktura`.
- For Vipps, a member with an open faktura of the same amount that the rule could not settle (`SameAmount > 0`) is never price-classified either.

**Failure mode:** before BRY-366 the first of two same-named members took the payment, a payment made before the faktura existed settled it, and a second payment to a partly paid faktura was booked as if it covered all of it.

### A payer alias never takes a name from another member unasked

**Rule (BRY-383):** payer aliases (`vipps_customer_aliases`, unique per club and lowercased name) are taught from two places, and both auto-assign rules resolve them first:
- **Tildel.** Assigning an incoming bank row (single or split) to a member's faktura with `remember_payer` saves the row's payer (`bankRowPayer`: the `Fra:` name, else the counterpart, the same name auto-assign resolves) for the faktura's member. `teachPayerAlias` runs inside the faktura-lock transaction before any entry is created: it re-checks the conflict, inserts the alias, and only then are the rows linked and posted, so a refused or failed booking saves no alias. It saves nothing when there is nothing to learn: a faktura without a member, an empty payer, the club's own payer (`partnerScoring.isOwn`), split rows with different normalized payers, or a payer that already resolves to that member.
- **Vipps queue.** `POST /vipps/customer-aliases` (treasurer/admin, fresh TOTP) refuses a `user_id` that is not in the club. `DELETE` is gated the same way.

A **conflict** is a payer whose normalized name equals *any* other member's full name or first+last (not only a unique match), or is already another member's alias. Tildel refuses it with 409 `alias_conflict` and books nothing unless the request carries `confirm_alias_override`. No response names or identifies another member: the suggestions' `payer_alias` carries only `payer`, `pass`, `own_club` and `taken` (the name is some member's own name or alias), and which candidate faktura the payer already resolves to is a per-candidate `payer_resolves_here` on the suggested and amount-matched fakturas, computed server-side from one resolution per request. Both sources audit `payer_alias.taught` / `payer_alias.removed` without the payer name. An alias never outranks a Vipps KID that names another member (see "One member" below).

**Failure mode:** an alias silently pointing a member's own name at someone else sends that member's future payments to the wrong faktura through auto-assign, with no human in the loop. An ungated alias write did the same from any board session.

### A Vipps payout clears as soon as its settlement's charges are booked

**Rule (BRY-340):** on the per-charge model, `ClearReadyVippsPayouts` clears every unbooked bank deposit carrying `Utb. <settlement> Vippsnr <msn>` whose settlement has no `belastning` (same MSN + booking date) left unbooked and undismissed. It runs at the end of `BankSync`, after auto-assign in each operator Vipps path (CSV upload, Report API sync, **Synk Vipps**) and after a `PostVippsDraftBacklog` apply — all behind the money gate. The nightly Report sync has no acting user and does not clear.
- Clearing is `ClearVippsPayout`: a posted fee entry for the settlement's unbooked fee rows, then a posted bank entry, so the settlement nets the clearing account to 0.
- The fee rows and the bank row are each linked with a guarded update. A clear that links fewer fee rows than it read lost a race and unwinds rather than posting the fee twice.
- A deposit without a parseable reference is never matched by amount. An imported payout no deposit names is reported as skipped with a reason, as is a deposit naming a settlement that was never imported.
- A deposit clears only if it equals the imported net payout to the øre; otherwise it fails with a reason and nothing is posted.
- A settlement clears once. A second deposit naming a settlement whose payout row is already linked to a standing entry, or that another booked deposit names, is skipped ("allerede avstemt"), never booked.
- Charges and fees carry only MSN + booking date, so when two payouts share both, neither is cleared automatically.
- Both the fee and the bank period are checked before either entry is created; a period that is not open refuses the whole clear.
- A sweep failure never fails the sync or import that ran it: callers use `ClearReadyVippsPayoutsReported`, which logs and sets the result's `error`.

**Failure mode:** before this, Stage 2 ran only from the per-row Tildel action, so a sync reported "Vipps-avstemt: 0" and the clearing account kept the full gross of every settlement while the bank deposits sat unbooked.

### A provisional bank row gives way to its settled row

**Rule (BRY-378):** a statement exported while a transfer is in flight carries it as a provisional row (`provisionalRowSQL`). That means description exactly `Overførsel` or `Bokført på vei`, no counterpart, no KID, dated on its statement's last day (`bank_imports.period_to`, recorded at import and widened by the reimport paths; the latest surviving row only for an import older than that column), and `reseen_at` unset. A later export carries the settled row (for a Vipps payout, `Utb. <settlement> Vippsnr <msn>`).
- **Same bank reference (the usual case):** the arkivref dedup in `ImportBankRows` would skip the settled row. Instead `refreshProvisionalByReference` updates the existing row in place: date, description, counterpart, KID, `from_account`/`to_account` and `row_hash`. It does so only when exactly one row carries the reference, that row is provisional, unbooked, undismissed, not refund-paired and on the importing bank account, the incoming row is not bare, the amounts agree to the øre, the settled date is the same day or up to two days later, no other row already holds the new hash, and there is an actor to audit it. A refresh is audited as `bank_row.refreshed` (with the old and new date when it moved) and counted as `refreshed`. The same pair with another amount or a bigger gap is reported as a `reference_mismatches` entry and left alone. Everything else with a known reference is skipped as before.
- **A bare row seen again:** when a later statement that runs past the row's date carries the same bare row again, still bare (same reference, or the same hash without one), the row is a settled transfer with a terse text. It gets `reseen_at` and is never provisional again, so it is never superseded or refreshed.
- **Different reference:** both rows exist. `SupersedeProvisionalBankRows` (each import, and pass 0b of `BankSync`) dismisses the provisional row as `superseded` when it pairs one to one with a non-bare, undismissed row of another import on the same bank account, date and amount. Any other shape is reported as ambiguous and left alone. Booked rows are never touched.
- A refreshed row is not KID-matched at import; the next bank sync (KID pass, payout sweep) picks it up.

**Failure mode:** the settled row was skipped as already imported, so the deposit stayed a bare `Overførsel` in Tildel and its Vipps payout could never clear. Matching such a row to a payout by hand would have booked the provisional row, and the settled row would then arrive later as an unbooked duplicate.

### Vipps postings are posted on creation; no period closes with drafts

**Rule (BRY-339):** every Vipps posting path posts its entry right away: per-charge charge→faktura and charge→account, the order-linked split, auto-assign, the payout's fee and bank entries, and the legacy `ReconcileVippsConfirm`.
- Each path marks its source row with a guarded update first, then posts. If the post fails, it unlinks the row and deletes the draft (`unwindVippsPosting`), so the charge returns to Tildel.
- The legacy `ResyncVippsBilags` re-creates existing drafts and keeps them as drafts. Posting a pre-existing backlog is a separate, supervised step: `PostVippsDraftBacklog` (BRY-341), per fiscal period, preview then fresh-TOTP apply. It refuses unbalanced or orphan drafts, drafts whose source row another posted entry already booked, and drafts whose faktura another posted entry already settled. It never posts a 1500 credit that isn't tied to a faktura (BRY-370), because it may be the only record that a faktura was paid. Migration 000106 fills `vipps_import_rows.invoice_id` for legacy bookings where the `vipps_charge.assigned_invoice` audit row names the charge, its current entry and a still-paid faktura.
- **When a 1500-parked charge may be rebooked (BRY-392).** A charge draft crediting 1500 with no `invoice_id` and no checkout order may be rebooked, released or unassigned only when `receivableChargeSettledFaktura` finds no faktura it may have paid. `payments` has no column naming a Vipps charge, so the test is a heuristic. The charge fails on a faktura marked paid for its amount (±1 kr) that no non-voided entry (draft or posted) linked through `bank_import_rows.invoice_id` or `vipps_import_rows.invoice_id` explains, where marked paid means either:
  - `payment_id` points at a `KID-avstemming` payment of that amount, and the faktura belongs to the member the payer resolves to (any pass) or carries the KID in the message — at any date — or the payment's `paid_at` is within 7 days of the charge date, for any faktura (a payer may pay a household member's faktura, and name resolution can be wrong; legacy payments carry the payout date, a few days after the charge);
  - `vipps_paid_at` is within 7 days of the charge date and the faktura total is that amount, unless a booked charge of a checkout order paying the faktura (`order_sources`) explains it.

  A charge that fails is refused with the faktura number and cannot be released or unassigned: `UnassignVippsCharge` runs the test on every draft with no `invoice_id` that credits 1500, whoever calls it. There is no in-app way yet to fix the faktura side (BRY-397). One that passes goes through `decideVippsCharge` like a new charge (`member_invoice` → `AssignVippsChargeToInvoice`, `price_item` → the category's account), rebooked via `applyBacklogReclass` (unassign the draft, then book). With auto-assign off, or no rule, it is refused and listed in `releasable`. Release is the Vipps unassign with `released_from_backlog: true` (`ReleaseVippsBacklogCharge`): it also requires exactly such a draft in an open period, and is audited as `vipps_charge.unassigned` with that detail. The test does not see a faktura marked paid by a payment with another description.
- `ClosePeriod` refuses (409 `period_has_drafts`) while the period holds any draft, and lists them per source with count and total. `DeleteDraftJournalEntry` removes a draft that no bank or Vipps row references. Linked drafts are undone through Tildel unassign.
- Posted entries are never hard-deleted. Undoing a posted charge voids it with a reversal (`UnassignVippsCharge`, `VoidJournalEntry`). Unassign checks the period first (a charge booked in a closed period is refused), then voids, and only then clears the row links and reopens the faktura (BRY-370). A failed void changes nothing; a crash after the void is completed by running Angre again.

**Failure mode:**
- Reports and momskomp count only booked entries, so draft Vipps postings left all Vipps income and fees out of the accounts. In prod that was 869 drafts.
- Closing a period with drafts strands them, because posting into a closed period is refused.

### Booked faktura bilags are corrected, never rebuilt

**Rule (BRY-364):** a posted `invoice_sync` entry is never deleted or edited. When its revenue split is wrong, `ResplitInvoiceBilags` posts an `invoice_resplit` entry for the invoice that moves the difference between revenue accounts. The difference is the lines' expected per-account split minus what the sync entry and earlier resplits booked. The run is idempotent, a preview posts nothing, and apply is behind fresh TOTP. An invoice whose booked revenue differs from its total is skipped with a reason for manual correction. The old `RebuildInvoiceBilags` (delete and re-sync) was removed.

**Failure mode:** deleting `invoice_sync` entries also deletes voided originals whose `manual` reversals survive, so reports go negative. It also erases the audit trail that bokføringsloven expects corrections to leave.

### An auto-match posts or leaves nothing

**Rule (BRY-324):** the KID auto-match in `ImportBankRows` and in the bank sync either creates **and posts** the journal entry and links the row, or it leaves no entry at all. Posting uses the importing user (`bank_imports.imported_by`) as `posted_by`. The row is linked first, with a guarded update (`kidCascadeEligibleSQL`: unbooked, undismissed, not refund-paired), and then the entry is posted (BRY-370). A lost race discards the draft; a failed post unlinks the row and discards it. Either failure is returned in the import result's `row_errors`, never swallowed. The row stays unmatched for manual Tildel. Concurrent syncs therefore book a row once.

- **Which rows the cascade may book (BRY-382).** Only rows passing `kidCascadeEligibleSQL`: bank sync lists its candidates with it and both cascades guard the link with it, so a row the treasurer dismissed, even while a sync was waiting on the faktura lock, is never booked. Rejected-KID notices (`IsRejectedKIDDescription`) are skipped at import and sync alike.
- **The cascade locks the faktura (BRY-388).** `bookKIDCascadeMatch` takes `lockInvoiceForAssign` (cascade mode: still owed, nothing booked against it by `invoice_id`, KID or a Vipps charge) per row, in its own transaction, and holds it until the row is linked (recording `invoice_id`) and the entry posted. A faktura found settled is skipped and counted (`kid_skipped_settled`), not failed, unless the booking the cascade waited for was its own row's (then nothing is counted); a faktura voided or credited meanwhile is "no longer open", not settled. The lock is never held across a whole import. Under it the period is share-locked briefly inside `CreateJournalEntry`, the row is linked, then the period is share-locked again inside `PostJournalEntry`, as on the manual assign path.

- Every payment-match entry (KID, invoice number or counterpart, from import or sync) is sourced to its row (`source_table = 'bank_import_rows'`, `source_id` = the row) and described exactly `kidMatchDescription` (`Innbetaling KID <kid>: <row description>`, or `Innbetaling: <row description>` without a KID).
- A crash between link and post leaves the row linked to a draft. Bank sync releases such rows (`healStrandedKIDDrafts`) once the draft is older than 15 minutes and matches them again. It recognises them only by that source and exact description, so mapping-rule drafts from `AutoMatchImport`, which may also credit 1500 but carry the row's own description and wait for a gated post, are never touched.
- Manual assignments (`AssignBankRowToInvoice`, `AssignBankRowToAccount`, `AssignBankRowsToAccountGroup`) follow the same order: guarded link (`journal_entry_id IS NULL`, not dismissed, club-scoped), then post, then unlink and discard on a failed post. Of two concurrent assigns of one row, exactly one books. A group links all its rows in one transaction or none.
- Rows that share a `journal_entry_id` were booked together (a group booking or an intra-bank transfer). Unassigning any of them releases them all and reverses the entry once.

### Rows that belong together are booked together

**Rule (BRY-380):** an unbooked bank row with a **strong** partner row (opposite sign on the same bank account within 60 days, and the same counterparty account number, the same counterparty name, or an exactly reversing amount within 14 days) cannot be booked to an account on its own unless the request says `confirm_alone: true`; assign-account answers 409 `has_partner` with the partners. Booking the pair goes through `AssignBankRowsToAccountGroup`: one entry, one bank line for the net, the net on the chosen account. A group must be on one bank account and in one open fiscal period, must not net to zero (that is a transfer), and never books to a bank account or receivables (1500). Group links lock the rows in id order, so overlapping concurrent groups queue rather than deadlock. Rows that are the club's own transfers, provisional, Vipps settlement payouts, dismissed or refund-paired never get partners. Amounts are compared in øre. Faktura assignment is not guarded: the faktura match is its own evidence.

**Failure mode:** a buy-back payout and the overpayment the recipient returned were booked one at a time, the return as income, so revenue was overstated and the buy-back account understated by the returned amount.

**Failure mode:** before BRY-324 the importer passed the club id as `posted_by`. The users FK rejected it, and the error was swallowed. Every import-time match left an orphan draft, and the bank sync later booked the same row again, so the draft became a duplicate that would double-book the payment if posted. That left 56 in prod (230,831 kr), removed by migration `000100`.

### Postings find accounts by code; a system account's name is the club's

**Rule (BRY-377):** every posting path, report and the momskomp calculation finds an account by `code`, never by name. A treasurer may therefore rename a system (seeded) account and change its description in Kontoplan; `UpdateAccount` refuses only a change to its MVA eligibility (`ErrSystemAccountMVALocked`, 400) and the code, type, active state and deletion stay locked. An omitted or unchanged `mva_eligible` is not a change. The seed name only applies to new clubs: no migration renames an existing club's accounts.

Price items carry no account. The category map (`revenueAccountMap`, read through `PriceCategoryAccount`) gives a price item's default account, and `invoice_lines.account_id` overrides it per line. The account hints in the accounts list and the `price_item` suggestion pass name that default account, so they never claim a line override. Price items and invoice lines carry no MVA: amounts are gross.

**Failure mode:** treasurers could not recognise 3300 "Salgsinntekter" as the account slipping books to, and could not rename it because every system account was read-only.

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
- Each line books to its own `account_id` when set. Otherwise it uses the account of the category stored on the line at issue (`finance.LineCategorySQL`: the line's `category`, then its price item's current category only for a line that never stored one), and failing that 3900 (see *Revenue accounts resolve the same on every payment path* and *Invoice line snapshots are immutable*).
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

### Revenue accounts resolve the same on every payment path

**Rule (BRY-384):** A category resolves to its revenue account through one map, `revenueAccountMap` (`revenue_accounts.go`), on every path: invoice sync, Vipps per-charge, Vipps auto-assign, order split and the price hints. Revenue nothing names an account for lands on `fallbackRevenueCode` (3900): an unmapped category, an invoice residual (total ≠ line sum), a faktura with no lines and a line with neither an account nor a category. On a faktura line the category is the one stored on the line at issue, not the price item's current one. Paths that book without a human (Vipps auto-assign) or show a default (price hints) use `RevenueAccount`, which reports an unknown category as unknown, so it is never auto-booked or hinted.

**Failure mode:** before BRY-384 invoice sync fell back to 3100 and Vipps to 3900, so the same price item booked to different accounts depending on how it was paid, and unexplained invoice money was mixed into membership dues. Entries booked to 3100 that way were not rebooked.

### Vipps cascade lands on 3900, never 2900

**Rule:** Unmatched Vipps `belastning` lines route to `fallbackRevenueCode = "3900"` (Andre inntekter). The legacy `vippsClearingAccountCode = "2900"` is kept as a Go constant for backwards-compatible reports but no new lines route there.

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
- **`OwedByMemberSQL`** is `IsDebtSQL` (a positive total) and not settled for the member. It answers "is the member chased for this?" and is used by reminders (both enqueue *and* the worker's send-time check), KID reissue, the member payables page and "unpaid" filter, checkout totals, the Vipps-blocked check, the open-faktura lookup of Vipps and bank auto-assign (`decideMemberInvoice`), and the Økonomi overview's overdue figures.
- **`SettledForGLSQL`** returns true for `payment_id IS NOT NULL`. It is used where the receivable must actually be cleared: reconciliation and bookkeeping.
- **Automatic matching of incoming money** to a faktura (bank KID and invoice-number lookups at import and in the bank sync, the counterpart-name match, the Vipps member match, bank auto-assign) uses `OwedByMemberSQL` (BRY-370). Money for a voided, Vipps-captured, credited or paid faktura is an overpayment a human must see, so the row stays in Tildel.
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

**Rule (BRY-389):** an issued faktura books by the category stored on its lines, never the price item's current category. Every line with a price item stores its category (CHECK `invoice_lines_price_item_category`, migration 000111), exemption lines included. Readers of an issued line's category use `finance.LineCategorySQL` (`COALESCE(il.category, pi.category)`); the item's category is only the fallback for lines written before the constraint. Paths that price a **new** faktura or charge (single and bulk creation, Vipps per-charge matching, the price hints) read the price item, since that is the category at issue. `invoices.category` (used for `payment_type`) is likewise stored at issue and must not be switched to the item's.

**Failure mode:** a price-list correction silently changes amounts on already-issued invoices. Treasurer's totals start drifting. Re-categorising a price item would move an old faktura's revenue to another account on the next resync or resplit.

### A faktura and its lines are written together

**Rule (BRY-389):** single (`HandleCreateInvoice`) and bulk (`createBulkInvoice`) creation insert the `invoices` row and all its `invoice_lines` in one transaction. A line the database refuses fails that faktura: the single request returns 500, and the bulk run reports that member under `skipped` (`create failed: …`) and carries on with the rest. The S3 PDF upload, the email, the GL booking and the audit rows run only after the commit; the upload's `pdf_data` wipe runs on its own connection and would not see an uncommitted faktura.

**Failure mode:** before BRY-389 a failed line insert was only logged, leaving a faktura whose `total_amount` disagreed with its lines; the GL sync then booked the difference to 3900 as a residual.

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
- The offset line carries the item's `price_item_id` and `category`, its `exemption_role` and `account_id` = the club's `exemption_expense_account`. The bulk per-line duplicate check filters `exemption_role IS NULL`, so it still sees only the real line. GL booking honours `account_id` over the category, so the faktura books **gross**: CR the item's revenue, DR the expense account. The waived revenue is never netted away.
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
- **Different payers (BRY-393).** The payer check is relaxed only when the credit note has neither `user_id` nor `recipient_id` (imported history), or the request carries `confirm_other_member: true`, which the UI sends only after asking. This is safe because the link only sets `credits_invoice_id`: it posts nothing and unlinking reverses it. The amount check is never relaxed. The manual search (`GET …/credit-candidates?any=1&q=`) drops the payer and amount predicates but keeps every target rule (club, open, positive, issued or imported, not credited) and marks `same_amount` / `same_payer` per row.
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
