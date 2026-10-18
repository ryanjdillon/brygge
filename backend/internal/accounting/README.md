# `accounting/` — GL, bank import, Vipps reconciliation, faktura sync

The double-entry GL plus everything that posts to it: bank statement parsing, KID matching, Vipps payout reconciliation, periodic faktura sync from `payments`, momskompensasjon reports, kontoplan seeding.

## What lives here

| File | What |
|---|---|
| `service.go` | `Service` struct (db, log, audit). Constructor takes a pgxpool, audit service, and zerolog logger. Every method in the package hangs off this struct. |
| `journal.go` | `CreateJournalEntry`, `PostJournalEntry`, `VoidJournalEntry`. Double-entry validation lives here — every entry must balance debit = credit. |
| `period.go` | Fiscal periods (open/closed/locked) + `resolvePeriod` (always go through this — never query `fiscal_periods` directly). |
| `kontoplan.go` | NS 4102 chart of accounts seeding. `SeedKontoplan` runs on first club creation; adding a new account type adds to the seed list here. |
| `price_hints.go` | `PriceItemCategories`, `PriceCategoryAccount` (a price-item category's default account), `ListAccountsWithPriceHints` (the accounts list with the price items and categories that book to each account) and the `price_item` account-suggestion rule used by bank-row and Vipps-charge suggestions. |
| `bankimport.go` | CSV parsing per `BankFormat`, `ImportBankRows`, infer-own-account-number heuristic, auto-match KID at import time, intra-bank transfer detection. |
| `banksync.go` | On-demand re-run of KID matching + Vipps reconciliation across all of a club's unmatched bank rows. Called by the "Synk bank" button. |
| `kid_cascade.go` | What import and sync share in the KID cascade: the row eligibility predicate (`kidCascadeEligibleSQL`) and the locked per-row booking (`bookKIDCascadeMatch`). |
| `automatch.go` | `AutoMatchImport` — applies operator-defined `account_mapping_rules` against unmatched rows. |
| `vippsimport.go` | Parse Vipps export CSV (rows are `payout`, `belastning`, `fee`). |
| `vippsreconcile.go` | `ReconcileVippsPreview` + `ReconcileVippsConfirm` — the cascade that classifies each Vipps belastning. See cascade below. |
| `vippsresync.go` | `ResyncVippsBilags` — on the per-charge model, books order-linked charges and runs auto-assign; on the legacy model, re-runs the cascade across existing drafts that still land on 2900/3900 and keeps the results as drafts (posting a backlog is a supervised step, BRY-341). Called by the "Synk Vipps" button. |
| `auto_assign_member.go` | The member/faktura half shared by Vipps and bank auto-assign: `matchMemberName` (unique name passes), `resolveStrongMember` (alias or unique exact name), `decideMemberInvoice` (one open faktura, issued by the payment date, nothing booked against it, not imported, equal total). |
| `bank_auto_assign.go` | `AutoAssignBankDeposits` (+ `…Reported` for hooks, `PreviewBankAutoAssign` for the read-only projection): books unambiguous KID-less deposits to a faktura on opt-in (`clubs.bank_auto_assign`, BRY-366). |
| `invoicepayment.go` | `Service.linkInvoicePayment` — inserts a `payments` row and back-links `invoices.payment_id`. Used by both bank KID match and Vipps invoice match so the dashboard faktura-status widgets reflect the payment. |
| `vipps_backlog.go` | `PostVippsDraftBacklog`: posts a fiscal period's leftover Vipps drafts (preview, then apply). Parked charges on 3900, or on 1500 with no faktura, are rebooked by the auto-assign rules first when the club has auto-assign on. Unsafe drafts (unbalanced, orphan, source already booked, faktura already settled) are refused with a reason. |
| `resplit.go` | `ResplitInvoiceBilags` — corrects the per-account revenue split of booked fakturas with `invoice_resplit` reclassification entries (preview + apply). |
| `revenue_accounts.go` | `revenueAccountMap`, the one category → revenue account map for every payment path, and its two forms: `RevenueAccount` (code, known) for paths that must refuse an unknown category (Vipps auto-assign, price hints) and `RevenueAccountOrFallback` (unknown → `fallbackRevenueCode` 3900) for paths that must book regardless (invoice sync, Vipps per-charge, order split). |
| `sync.go` | `BookInvoice` / `ReverseInvoice` (send- and void-time booking, called from the invoice handlers), `SyncInvoices` (the repair sync behind **Avstem periode**), `SyncPayments`, `bookedEntryFilter`. One per-faktura path (`bookInvoiceEntry`) serves both; results carry `skipped_details` with a reason per skipped row. |
| `description_parse.go` | `ExtractKIDFromDescription`, `ExtractInvoiceNumberFromDescription`, `ExtractPayerFromDescription`. Vendor-specific (DNB/Sparebank) heuristics for pulling structure out of unstructured bank row descriptions. |
| `momskomp.go`, `momskomp_pdf.go` | Momskompensasjon (VAT reimbursement for non-profits) report + PDF. |
| `reports.go`, `reports_pdf.go` | Income statement, balance sheet generation. |

## The Vipps reconciliation cascade

When a bank row matching `Utb. NNN Vippsnr MMM` arrives, the reconciliation pulls the matching payout + all `belastning` + `fee` rows from the same booking date and proposes a balanced bilag. For each belastning, the classification cascade runs:

1. **Member + matching open invoice** → CR 1500 receivables + `LinkedInvoiceID` (so confirm calls `linkInvoicePayment` after posting and back-links `invoices.payment_id`)
2. **Amount → price-article** (`matchVippsAmountToPriceArticle`) → CR the category's account from `RevenueAccountOrFallback` (handles guest-slip × N nights, motorhome × N nights via integer-multiple match)
3. **Member resolved, no clear classification** → CR 1500 receivables (treasurer reconciles manually)
4. **Fallback** → CR 3900 `fallbackRevenueCode` (Andre inntekter)

**Order-linked charges (per-charge model, BRY-337).** An ePayment charge (faktura pay link, QR checkout) carries our merchant reference (`chk-…`) as the row's `order_id`. `AutoReconcileOrderCharges` books it from the order itself, with no amount or name guessing: DR clearing / CR 1500 for each faktura in `order_sources` (with the `linkInvoicePayment` back-link), and CR the category revenue account for each catalog line (`resolveOrderChargeSplit`). It refuses if the order total and the charge differ, or if a faktura on the order is already paid or voided; those are reported in `skipped_details`. It runs from **Synk Vipps**, an operator Report API sync, and CSV upload. The nightly Report sync only repairs row types, because journal entries need a human `created_by`. Like every Vipps posting, the entry is posted on creation (BRY-339).

**Auto-assign (per-charge model, BRY-340).** After order-linked charges, `AutoAssignVippsCharges` (`vipps_auto_assign.go`) books the rest of the queue when the outcome is unambiguous and the club has opted in (`clubs.vipps_auto_assign`):
1. `member_invoice`: a strong member match (`resolveCustomerToMemberPass` up to `memberPassFirstLast`; a name two members share is never strong) where `decideMemberInvoice` (`auto_assign_member.go`) finds the member's single open faktura — issued by the charge date, nothing booked against it, not imported, total equal to the øre — goes through `AssignVippsChargeToInvoice`.
2. `price_item`: `priceArticleCandidates` in one category, or a cross-category tie resolved by `clubs.vipps_auto_assign_tie_category` (only when every match is a per-night/day/hour price; an exact one-off price in any tied category leaves it manual), goes through `CreateVippsChargeBilag` to `RevenueAccount(category)`; a category the map does not know is left for review, never booked to the fallback. Members-only items also need a strong member match.

Everything else stays in Tildel; the result reports `assigned` and `needs_review`. It runs where `AutoReconcileOrderCharges` runs. `UnassignVippsCharge` undoes any charge booking: it refuses a closed period, voids a posted entry (or deletes a draft) *before* clearing the row, then reopens the faktura recorded in `vipps_import_rows.invoice_id`.

**Payout clearing (per-charge model, BRY-340).** `ClearReadyVippsPayouts` (`vipps_payout_sweep.go`) runs Stage 2 without a human: every unbooked, undismissed bank row matching `Utb. NNN Vippsnr MMM` goes through `ClearVippsPayout`, which posts the fee entry (DR 7700 / CR clearing) and the bank entry (DR bank / CR clearing) once no `belastning` with the payout's MSN + booking date is still unbooked and undismissed. It runs at the end of `BankSync`, after auto-assign in every Vipps path that has an acting user (CSV upload, operator Report API sync, **Synk Vipps**), and after a `PostVippsDraftBacklog` apply. The result splits settlements into `cleared`, `waiting` (charges left in Tildel), `skipped` (a deposit naming an unimported or already-cleared settlement, two settlements on one MSN + booking date, or an imported payout with no open deposit carrying its reference) and `failed` (deposit ≠ net payout to the øre, a period that is not open, a concurrent clear), each with a Norwegian reason; unknown errors get a generic reason and are logged. Already-linked deposits are never candidates, so a re-run is a no-op. Sync and import paths call `ClearReadyVippsPayoutsReported`, so a sweep failure lands in the result's `error` instead of failing work already done. On the legacy model the sweep returns empty; `syncVippsReconciliations` owns those payouts. The manual `POST /bank-rows/{rowID}/clear-vipps-payout` stays for a single row.

⚠️ **Never route to 2900.** The `vippsClearingAccountCode = "2900"` constant exists only for backwards-compat with legacy reports. New lines must land on 3900 — see [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## The bank-row KID matching cascade

When a bank import lands or `BankSync` runs (after provisional rows are refreshed or superseded; see Common misses):

1. Candidates are incoming rows passing `kidCascadeEligibleSQL` (unbooked, undismissed, not refund-paired; `kid_cascade.go`). Bank sync lists them with it; an import only sees its fresh inserts. Rejected-KID notices (`IsRejectedKIDDescription`, "ble ikke akseptert": the money never arrived) are skipped at import and sync alike and stay in Tildel. Extract KID from `bir.kid_number` (CSV column) or fall back to `ExtractKIDFromDescription`
2. Match invoice by KID among fakturas still owed (`finance.OwedByMemberSQL`: not voided, paid, Vipps-captured or credited); tiebreak by checking no other matched row already used the same KID. Any amount books: a partial KID payment marks the faktura paid (`linkInvoicePayment` records the faktura total), and a second payment with the same KID finds no owed faktura and is left in Tildel
3. If no KID match, try `ExtractInvoiceNumberFromDescription` + amount within 0.005
4. Only while `clubs.bank_auto_assign` is off: `findInvoiceByCounterpartAndAmount`, the member whose normalized full name equals the counterpart with exactly one owed faktura of that amount. With the switch on this step is skipped and the auto-assign rule below replaces it
5. If matched, `bookKIDCascadeMatch` books the row in a transaction of its own: lock the faktura (`lockInvoiceForAssign` in `lockKIDCascade` mode, which re-checks it is still owed and has nothing booked against it, by `invoice_id` or KID or a Vipps charge), create a balanced bilag (DR bank, CR receivables), link the row with an update guarded on `kidCascadeEligibleSQL` that records `invoice_id`, post, release the lock, and call `linkInvoicePayment` to back-link `invoices.payment_id`. Under the lock the row is re-checked first: if the booking it waited for was this row's own (a second sync, or an import during a sync), it is `errKIDRowTaken` and nothing is reported. A faktura found paid or booked under the lock is `errInvoiceSettled`: the row stays in Tildel and is counted in `KIDSkippedSettled` (on import also a `row_errors` entry). One voided or credited meanwhile is `errInvoiceNotOpen` (import: `row_errors` "faktura no longer open"; not counted). A period closed mid-run (`ErrPeriodClosed` from create or post) is reported like any closed period. A lost link discards the draft; a failed post unwinds the link and `invoice_id`. A crash between link and post leaves the row `auto_matched` to an unposted draft; `BankSync` first releases such rows (KID drafts with a CR 1500 line, older than 15 minutes, clearing `invoice_id` too) so they match again

**Bank auto-assign (BRY-366).** After the cascade, `AutoAssignBankDeposits` (`bank_auto_assign.go`) runs at the end of `ImportBankRows` and as pass 4c of `BankSync` (before the Vipps payout sweep), club-wide, when the club has opted in (`clubs.bank_auto_assign`). A KID-less incoming row in an open period is booked to a faktura only when its payer (`Fra:` name, else counterpart) is not the club, resolves through `resolveStrongMember`, `decideMemberInvoice` settles it, it has no strong partner, and no other row in the run reaches the same faktura. Every other row is counted by reason in `by_reason`. Booking goes through `assignBankRowToInvoice` with `assignOpts{auto: true}`: the faktura is locked by `lockInvoiceForAssign` (`FOR NO KEY UPDATE`; plain `FOR UPDATE` would deadlock with the row link's foreign-key check), which the manual, split-payment and Vipps assigns also take, and re-checked in a fresh statement, the entry is posted at once and records `invoice_id`, and `auto_matched` stays false. Booked imports get their `matched_count` recounted. Results ride on `ImportResult.AutoAssign` / `BankSyncResult.AutoAssign` (nil when off); hooks call `AutoAssignBankDepositsReported`, so a failure sets `error` instead of failing the import or sync. `PreviewBankAutoAssign` runs the same decisions as a dry run.

## Helper functions worth knowing

- `Service.linkInvoicePayment(ctx, clubID, invoiceID, paidAt)` — idempotent. Skips org-recipient invoices (no `user_id`), skips already-paid invoices, maps `invoices.category` → `payment_type`. Call after any GL posting that should also mark the invoice paid.
- `Service.matchVippsAmountToPriceArticle(ctx, clubID, amount)` — returns `(accountCode, label)` when exactly one active price-item matches the amount. Exact match for `once/year/season` units; integer-multiple (2-60×) for per-time units.
- `Service.resolvePeriod(ctx, clubID, date, override)` — always use this for "which fiscal period does this date land in?" Auto-creates a calendar-year period on demand; respects `closed`/`locked` status.
- `Service.resolveCustomerToMember(ctx, clubID, customerName, message)` — fuzzy member match for Vipps payer names. KID-in-message first, then normalized full-name, first+last (only when exactly one member carries the name), unique-last-name. `normalizeName` handles æøå + diacritics + hyphens + case. The name passes live in `matchMemberName` (`auto_assign_member.go`) over a member list loaded once (`loadMemberCands`).

## Fixed account constants

| Constant | Code | Name |
|---|---|---|
| `bankAccountCode` | 1920 | Bankkonto drift |
| `receivablesAccountCode` | 1500 | Kundefordringer |
| `fallbackRevenueCode` | 3900 | Andre inntekter (fallback for every payment path: unmapped category, invoice residual or uncategorised line, unmatched Vipps charge) |
| `vippsFeeAccountCode` | 7700 | Bankgebyrer |
| `vippsClearingAccountCode` | 2900 | **Legacy** — kept for report compat, no new lines route here |

See [`../../../docs/developer/reference/enums.md`](../../../docs/developer/reference/enums.md) for the full mapping tables.

## Invariants

- `posted` journal entries are immutable: the only write is `VoidJournalEntry` (original marked `voided` + a posted balanced reversal); never updated or deleted
- Every Vipps posting (charge, order split, auto-assign, fee, payout, legacy confirm) is posted on creation; a failed post unlinks its rows and discards the draft
- A per-charge Vipps payout clears automatically (bank sync, Vipps sync, backlog apply) once every charge of its settlement is booked or dismissed; fee rows and the bank row are each linked with a guarded update, and a lost race unwinds instead of booking the fee twice
- `ClosePeriod` refuses while the period holds drafts (409 `period_has_drafts`, listed per source); `DeleteDraftJournalEntry` removes an unlinked draft
- `closed`/`locked` fiscal periods reject create, post **and void** (`ErrPeriodClosed` → 409 `period_closed`); a void is refused, never moved to a later period. Writers hold the period `FOR SHARE`; `ClosePeriod` takes it `FOR UPDATE` before counting drafts
- Every function acting on a request id takes the club id and filters on it; a foreign id is not found (404)
- `UnassignBankRow`: one tx voids the posted entries (`voidJournalEntryTx`, so a closed period rolls it all back), clears the rows (incl. a transfer's other row), deletes drafts and reopens the faktura; retry-safe. For a row with no `invoice_id` (assigned before migration 000057) it recovers the faktura from the payment only when the entry credits receivables (1500); an account booking never reopens a faktura that happens to be paid the same day for the same amount
- Vipps belastning amounts are positive; fees are negative
- Bank rows with `journal_entry_id IS NOT NULL` cannot be reassigned without cascading `journal_lines`
- Reports and momskomp count `posted` **and** `voided` entries (`bookedEntryFilter`), so a void and its reversal net to zero
- KID auto-match (import and bank sync) posts as the importing user or discards its draft — never leaves an orphan; failures surface as `row_errors`. It never books a dismissed, refund-paired or rejected-KID row, and locks the faktura per row across link + post
- Payment-match entries are sourced to their row and described by `kidMatchDescription`; bank sync's stranded-draft heal keys on exactly that, never on mapping-rule drafts
- Manual bank-row assigns link the row (guarded) before posting; bilag numbers retry on a unique-key collision instead of failing
- Bank auto-assign books only on opt-in, never creates a fiscal period, never targets an imported faktura, and locks the faktura across link + post; while it is off the cascade's name+amount step still runs
- Matching incoming money to a faktura uses `finance.OwedByMemberSQL`; only ePayment order reconciliation uses `finance.OpenReceivableSQL` (it books captures the webhook already flagged)
- A role-exemption line books to its own `account_id` (the club's exemption expense account), so an exempt item books gross: CR revenue, DR expense. A zero-total faktura books with no 1500 line
- A faktura is booked when sent and reversed when voided (`BookInvoice` / `ReverseInvoice`, which reverses the whole `invoice_sync` + `invoice_resplit` family or nothing); `SyncInvoices` only repairs what was missed. At most one `invoice_sync` entry per faktura (unique index). `SyncPayments` skips invoice-settling payments

Full list: [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## Common misses

- **Arkivref dedup.** `ImportBankRows` skips an incoming row whose bank reference another row already carries, before the hash insert. A provisional in-flight row (bare `Overførsel` on the export day) and its settled row usually share that reference, so the settled text would never land. The one exception is `refreshProvisionalByReference`, which updates the provisional row in place; keep its conditions narrow (see [invariants](../../../docs/developer/reference/invariants.md#a-provisional-bank-row-gives-way-to-its-settled-row)).
- **The statement's last day is `bank_imports.period_to`, not the latest surviving row.** An overwrite reimport deletes some of an older import's rows; deriving "the export day" from what survives turns a bare mid-statement row into a provisional one. Reimport paths record the full statement with `recordStatementPeriod` before importing a subset.
- **Rows booked together share one `journal_entry_id`.** A group booking (`AssignBankRowsToAccountGroup`, BRY-380) links 2–5 rows on one bank account to a single entry with one bank line for their net, and an intra-bank transfer links its two rows to one entry. Never assume one row per entry: unassign, reimport and reports must treat every row pointing at the entry as one booking (`UnassignBankRow` already releases them all).
- **Partner suggestions have one source.** `loadPartnerRows` + `scorePartner` feed `/partners`, the list's `partner_hint` and the 409 `has_partner` guard on assign-account; change the signals there, never in one consumer, or the list badge and the guard disagree. A known counterparty account number decides whether a row is the club's own transfer; the club-name test applies only when no account is known, because an inbound row's Motpart is often the club's own account label. Group refusals are exported sentinels (`ErrGroup*`); a group never books to a bank account or to receivables (1500).
- **Account suggestions for incoming money start from price items.** `rankAccountSuggestions` serves bank rows (`/bank-rows/{id}/suggestions`) and Vipps charges (`/vipps-charges/{id}/suggestions`). Its first pass reuses `priceArticleCandidates`, the same amount match auto-assign uses, and `priceItemAccountSuggestions` owns the rule (sannsynleg when every fitting item points at one account, else svak; members-only items need a known payer). Change the amount match in `priceArticleCandidates`, never in a consumer, or suggestions and auto-assign disagree. Members-only items are ignored entirely for an unknown payer, tooltip included. A Vipps settlement payout row gets no price_item pass (its amount is a sum of charges), and the description hints (`outgoingHints`, all expense accounts) run only for outgoing amounts, so a payer's message such as "Strøm" never offers an expense account for money coming in. A charge id that is not the club's answers 404 (`ErrVippsChargeNotFound`). A new price-item category needs an entry in `PriceItemCategories` and a mapping, or its items drop out of the hints.
- **Every path that settles a faktura takes its lock.** `lockInvoiceForAssign` serialises the manual bank assigns, the split-payment assign, the Vipps assign, both auto-assign rules and the KID cascade on one faktura. A new settle path must take it too, record the faktura on its row (`invoice_id`) while the lock is held so the others see the booking, and hold it per row, never across a whole import.
- **Locking a faktura across a row link.** `linkBankRows` writes `bank_import_rows.invoice_id` on its own connection, and that foreign key takes `KEY SHARE` on the faktura. A caller holding the faktura `FOR UPDATE` in an outer transaction therefore waits on itself forever; use `FOR NO KEY UPDATE`. And read anything that must reflect the winner of the lock in a statement *after* the locking one: under READ COMMITTED, a statement that waited for the lock still evaluates its subqueries against its start snapshot.
- **Provisional rows are not matchable money.** Never book a bare `Overførsel` from an export's last day against a Vipps payout or a faktura by amount. The settled row arrives with the next export and would become an unbooked duplicate. `SupersedeProvisionalBankRows` dismisses the provisional row once the settled one is imported under another reference.

## Common changes

- Adding a new price-item category → edit `PriceItemCategories` + `revenueAccountMap` + `TestRevenueAccountResolver` + the handlers' `validCategories` + `frontend/src/lib/priceCategories.ts` (categories and their label keys, shared by pricing and the Tildel account hints) + [`enums.md`](../../../docs/developer/reference/enums.md)
- Adding a new bank format → new YAML in `bankformats/`, no Go changes needed
- Adding a new audit action → see [`../../../docs/developer/checklists/add-audit-action.md`](../../../docs/developer/checklists/add-audit-action.md)
- Adding a new migration → see [`../../../docs/developer/checklists/add-migration.md`](../../../docs/developer/checklists/add-migration.md)
