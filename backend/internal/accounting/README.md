# `accounting/` — GL, bank import, Vipps reconciliation, faktura sync

The double-entry GL plus everything that posts to it: bank statement parsing, KID matching, Vipps payout reconciliation, periodic faktura sync from `payments`, momskompensasjon reports, kontoplan seeding.

## What lives here

| File | What |
|---|---|
| `service.go` | `Service` struct (db, log, audit). Constructor takes a pgxpool, audit service, and zerolog logger. Every method in the package hangs off this struct. |
| `journal.go` | `CreateJournalEntry`, `PostJournalEntry`, `VoidJournalEntry`. Double-entry validation lives here — every entry must balance debit = credit. |
| `period.go` | Fiscal periods (open/closed/locked) + `resolvePeriod` (always go through this — never query `fiscal_periods` directly). |
| `kontoplan.go` | NS 4102 chart of accounts seeding. `SeedKontoplan` runs on first club creation; adding a new account type adds to the seed list here. |
| `bankimport.go` | CSV parsing per `BankFormat`, `ImportBankRows`, infer-own-account-number heuristic, auto-match KID at import time, intra-bank transfer detection. |
| `banksync.go` | On-demand re-run of KID matching + Vipps reconciliation across all of a club's unmatched bank rows. Called by the "Synk bank" button. |
| `automatch.go` | `AutoMatchImport` — applies operator-defined `account_mapping_rules` against unmatched rows. |
| `vippsimport.go` | Parse Vipps export CSV (rows are `payout`, `belastning`, `fee`). |
| `vippsreconcile.go` | `ReconcileVippsPreview` + `ReconcileVippsConfirm` — the cascade that classifies each Vipps belastning. See cascade below. |
| `vippsresync.go` | `ResyncVippsBilags` — on the per-charge model, books order-linked charges and runs auto-assign; on the legacy model, re-runs the cascade across existing drafts that still land on 2900/3900 and keeps the results as drafts (posting a backlog is a supervised step, BRY-341). Called by the "Synk Vipps" button. |
| `invoicepayment.go` | `Service.linkInvoicePayment` — inserts a `payments` row and back-links `invoices.payment_id`. Used by both bank KID match and Vipps invoice match so the dashboard faktura-status widgets reflect the payment. |
| `resplit.go` | `ResplitInvoiceBilags` — corrects the per-account revenue split of booked fakturas with `invoice_resplit` reclassification entries (preview + apply). |
| `sync.go` | `paymentTypeAccountMap`, `SyncPayments`, `SyncInvoices`, `bookedEntryFilter`. The faktura → journal entry sync; results carry `skipped_details` with a reason per skipped row. |
| `description_parse.go` | `ExtractKIDFromDescription`, `ExtractInvoiceNumberFromDescription`, `ExtractPayerFromDescription`. Vendor-specific (DNB/Sparebank) heuristics for pulling structure out of unstructured bank row descriptions. |
| `momskomp.go`, `momskomp_pdf.go` | Momskompensasjon (VAT reimbursement for non-profits) report + PDF. |
| `reports.go`, `reports_pdf.go` | Income statement, balance sheet generation. |

## The Vipps reconciliation cascade

When a bank row matching `Utb. NNN Vippsnr MMM` arrives, the reconciliation pulls the matching payout + all `belastning` + `fee` rows from the same booking date and proposes a balanced bilag. For each belastning, the classification cascade runs:

1. **Member + matching open invoice** → CR 1500 receivables + `LinkedInvoiceID` (so confirm calls `linkInvoicePayment` after posting and back-links `invoices.payment_id`)
2. **Amount → price-article** (`matchVippsAmountToPriceArticle`) → CR mapped revenue account from `vippsCategoryAccountMap` (handles guest-slip × N nights, motorhome × N nights via integer-multiple match)
3. **Member resolved, no clear classification** → CR 1500 receivables (treasurer reconciles manually)
4. **Fallback** → CR 3900 `vippsFallbackRevenueCode` (Andre inntekter)

**Order-linked charges (per-charge model, BRY-337).** An ePayment charge (faktura pay link, QR checkout) carries our merchant reference (`chk-…`) as the row's `order_id`. `AutoReconcileOrderCharges` books it from the order itself, with no amount or name guessing: DR clearing / CR 1500 for each faktura in `order_sources` (with the `linkInvoicePayment` back-link), and CR the category revenue account for each catalog line (`resolveOrderChargeSplit`). It refuses if the order total and the charge differ, or if a faktura on the order is already paid or voided; those are reported in `skipped_details`. It runs from **Synk Vipps**, an operator Report API sync, and CSV upload. The nightly Report sync only repairs row types, because journal entries need a human `created_by`. Like every Vipps posting, the entry is posted on creation (BRY-339).

**Auto-assign (per-charge model, BRY-340).** After order-linked charges, `AutoAssignVippsCharges` (`vipps_auto_assign.go`) books the rest of the queue when the outcome is unambiguous and the club has opted in (`clubs.vipps_auto_assign`):
1. `member_invoice`: a strong member match (`resolveCustomerToMemberPass` up to `memberPassFirstLast`) with exactly one open faktura of equal total goes through `AssignVippsChargeToInvoice`.
2. `price_item`: `priceArticleCandidates` in one category, or a cross-category tie resolved by `clubs.vipps_auto_assign_tie_category`, goes through `CreateVippsChargeBilag` to `vippsCategoryAccountMap[category]`. Members-only items also need a strong member match.

Everything else stays in Tildel; the result reports `assigned` and `needs_review`. It runs where `AutoReconcileOrderCharges` runs. `UnassignVippsCharge` undoes any charge booking: it deletes a draft or voids a posted entry, and reopens the faktura recorded in `vipps_import_rows.invoice_id`.

⚠️ **Never route to 2900.** The `vippsClearingAccountCode = "2900"` constant exists only for backwards-compat with legacy reports. New lines must land on 3900 — see [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## The bank-row KID matching cascade

When a bank import lands or `BankSync` runs:

1. Extract KID from `bir.kid_number` (CSV column) or fall back to `ExtractKIDFromDescription`
2. Match invoice by KID; tiebreak by checking no other matched row already used the same KID
3. If no KID match, try `ExtractInvoiceNumberFromDescription` + amount within 0.005
4. If matched, create a balanced bilag (DR bank, CR receivables) and call `linkInvoicePayment` to back-link `invoices.payment_id`

## Helper functions worth knowing

- `Service.linkInvoicePayment(ctx, clubID, invoiceID, paidAt)` — idempotent. Skips org-recipient invoices (no `user_id`), skips already-paid invoices, maps `invoices.category` → `payment_type`. Call after any GL posting that should also mark the invoice paid.
- `Service.matchVippsAmountToPriceArticle(ctx, clubID, amount)` — returns `(accountCode, label)` when exactly one active price-item matches the amount. Exact match for `once/year/season` units; integer-multiple (2-60×) for per-time units.
- `Service.resolvePeriod(ctx, clubID, date, override)` — always use this for "which fiscal period does this date land in?" Auto-creates a calendar-year period on demand; respects `closed`/`locked` status.
- `Service.resolveCustomerToMember(ctx, clubID, customerName, message)` — fuzzy member match for Vipps payer names. KID-in-message first, then normalized full-name, first+last, unique-last-name. `normalizeName` handles æøå + diacritics + hyphens + case.

## Fixed account constants

| Constant | Code | Name |
|---|---|---|
| `bankAccountCode` | 1920 | Bankkonto drift |
| `receivablesAccountCode` | 1500 | Kundefordringer |
| `defaultRevenueCode` | 3100 | Medlemskontingent (invoice sync fallback) |
| `vippsFallbackRevenueCode` | 3900 | Andre inntekter (Vipps cascade fallback) |
| `vippsFeeAccountCode` | 7700 | Bankgebyrer |
| `vippsClearingAccountCode` | 2900 | **Legacy** — kept for report compat, no new lines route here |

See [`../../../docs/developer/reference/enums.md`](../../../docs/developer/reference/enums.md) for the full mapping tables.

## Invariants

- `posted` journal entries are immutable — only `VoidJournalEntry` (creates a balanced reversal)
- Every Vipps posting (charge, order split, auto-assign, fee, payout, legacy confirm) is posted on creation; a failed post unlinks its rows and discards the draft
- `ClosePeriod` refuses while the period holds drafts (409 `period_has_drafts`, listed per source); `DeleteDraftJournalEntry` removes an unlinked draft
- `closed`/`locked` fiscal periods reject new entries
- Vipps belastning amounts are positive; fees are negative
- Bank rows with `journal_entry_id IS NOT NULL` cannot be reassigned without cascading `journal_lines`
- Reports and momskomp count `posted` **and** `voided` entries (`bookedEntryFilter`), so a void and its reversal net to zero
- KID auto-match (import and bank sync) posts as the importing user or discards its draft — never leaves an orphan; failures surface as `row_errors`
- `SyncInvoices` books only sent, non-voided fakturas and reverses ones voided after booking; `SyncPayments` skips invoice-settling payments

Full list: [`../../../docs/developer/reference/invariants.md`](../../../docs/developer/reference/invariants.md).

## Common changes

- Adding a new Vipps category mapping → edit `vippsCategoryAccountMap` + `paymentTypeAccountMap` + `frontend/src/composables/usePricing.ts categoryKeys` + [`enums.md`](../../../docs/developer/reference/enums.md)
- Adding a new bank format → new YAML in `bankformats/`, no Go changes needed
- Adding a new audit action → see [`../../../docs/developer/checklists/add-audit-action.md`](../../../docs/developer/checklists/add-audit-action.md)
- Adding a new migration → see [`../../../docs/developer/checklists/add-migration.md`](../../../docs/developer/checklists/add-migration.md)
