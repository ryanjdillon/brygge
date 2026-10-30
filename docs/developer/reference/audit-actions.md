# Audit actions reference

Every `audit.Action*` constant defined in `backend/internal/audit/audit.go`, with the action string, the resource type the row binds to, when it fires, and the notable `extra` fields.

When adding a new action, follow [`checklists/add-audit-action.md`](../checklists/add-audit-action.md) and add the row here in the same commit.

The action string is the JSON wire format (`row.action`); the constant is the Go reference. Downstream filters (CSV exports, audit-log search) match on the string, not the constant — once shipped, never change.

## Naming convention

`<resource>.<verb>` — verb past tense because the action has already happened by the time the log row is written. ✅ `invoice.emailed` ❌ `regen_invoice`, `invoice.regenerate`.

---

## Auth

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionLoginSuccess` | `auth.login_success` | session | Magic-link verified + session created | `email` |
| `ActionLoginFailed` | `auth.login_failed` | session | Magic-link verification fails (wrong/expired token) | `email`, `reason` |
| `ActionTokenRevoked` | `auth.token_revoked` | session | Logout + admin-initiated session revoke | — |
| `ActionInvalidToken` | `auth.invalid_token` | session | Session cookie present but doesn't validate (deleted, expired absolute cap) | — |

## TOTP

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionTOTPVerified` | `admin.totp_verified` | session | User passes the TOTP gate (either step-up or fresh) | `window` |
| `ActionTOTPRecoveryRedeemed` | `admin.totp_recovery_redeemed` | user | Recovery code consumed via fallback flow | `codes_remaining` |
| `ActionTOTPCodesRegenerated` | `admin.totp_codes_regenerated` | user | User generates a fresh batch of recovery codes; wipes unused | — |
| `ActionTOTPAdminDisabled` | `admin.totp_disabled_by_admin` | user | Admin disables another user's TOTP (lockout recovery) | `target_user_id` |
| `ActionPasskeyAdminReset` | `admin.passkey_reset_by_admin` | user (the target) | Admin removes every passkey of another user and revokes their sessions (lockout recovery, BRY-247) | — |

## Admin user operations

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionUserRoleUpdated` | `user.role_updated` | user | Grant/revoke a role on a user | `role`, `granted` (bool) |
| `ActionUserDeleted` | `user.deleted` | user | Reserved, currently unused: nothing writes it. The admin delete writes the raw string `delete_user` (below). | — |
| `ActionUserDeactivated` | `user.deactivated` | user | Admin deactivates a member (reversible; hides + blocks login, keeps data, revokes their developer tokens) | — (ids only; the member is `resource_id`) |
| `ActionUserReactivated` | `user.reactivated` | user | Admin reactivates a deactivated member | — (ids only) |
| `ActionMemberPIIPurged` | `user.pii_purged` | user | Scheduled sweep erases a deactivated member's PII once retention clears (actor is NULL — system job) | `reason` |
| `ActionUserMailProvisioned` | `user.mail_provisioned` | user | Stalwart mailbox created for the user | `jmap_user` (the principal slug, no PII) |
| `ActionUserMailDeprovisioned` | `user.mail_deprovisioned` | user | Stalwart mailbox removed | `jmap_user` |

`HandleDeleteUser` (`DELETE /admin/users/{userID}`, which always anonymises in place) writes the raw action string `delete_user` rather than `ActionUserDeleted`: resource `user`, `extra` `sessions_revoked` (session rows deleted by `revokeMemberAccess`, including expired ones). The string is shipped, so it stays. Rows written before BRY-536 also carry `old_email`, `old_full_name` and `erasure`; the identity keys are scrubbed for members who are erased.

The other member writes in `admin_users.go` also use raw strings: `create_user` (`roles`), `update_user` (`fields`, the names of the fields changed, never their values) and `update_roles` (`old_roles`, `new_roles`). **Audit rows about a member hold ids only.** A name, email, phone or address in `details` outlives the member's erasure; the erasure paths remove `audit.MemberPIIKeys` from the member's rows (`audit.ScrubMemberPII`), and a new action about a member must not write those keys. Rows that name a recipient rather than a member (`invoice.emailed`, `invoice.reminded`, `invoice.delivery_log_viewed`, all keyed on the faktura) are outside that scrub.

## Slips

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionSlipCreated` | `slip.created` | slip | New slip added to the harbor map | `slip_code` |
| `ActionSlipAssigned` | `slip.assigned` | slip | Member assigned to a slip (permanent or seasonal) | `user_id`, `assignment_type` |
| `ActionSlipReleased` | `slip.released` | slip | Assignment ended (member moved, retired, waiting-list flow) | `user_id` |

## Content operations

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionEventCreated` | `event.created` | event | Calendar event created | `title`, `start_date` |
| `ActionEventDeleted` | `event.deleted` | event | Calendar event deleted | — |
| `ActionPricingCreated` | `pricing.created` | price_item | Price item created | `name`, `category`, `amount`, `audience` |
| `ActionPricingUpdated` | `pricing.updated` | price_item | Price item edited (any change other than active → inactive) | `name`, `category`, `amount_before`, `amount_after`, `is_active_after` |
| `ActionPricingArchived` | `pricing.archived` | price_item | Price item edited from active to inactive, including the "archive instead" path when delete is refused | same as `pricing.updated` |
| `ActionPricingDeleted` | `pricing.deleted` | price_item | Unreferenced price item hard-deleted. A refused delete (409) is not logged. | `name` |
| `ActionPricingExemptionsUpdated` | `pricing.exemptions_updated` | price_exemptions (club id) | Role exemption rules or their expense account replaced in Economy settings | `rules_before`, `rules_after`, `expense_account_before`, `expense_account_after` |
| `ActionPaymentFilterGroupCreated` | `payment_filter_group.created` | payment_filter_group | `HandleCreate` (`POST /admin/payment-filter-groups`) makes a group of the Payments page (BRY-522) | `slug`, `name`, `icon`, `price_item_ids`, `moved_from` (item id → slug of the group it left; the group it left also gets an `items_updated` row) |
| `ActionPaymentFilterGroupUpdated` | `payment_filter_group.updated` | payment_filter_group | `HandleUpdate` (`PATCH …/{slug}`) changed the name, icon or enabled flag; not logged when nothing changed | `slug`, and for each changed field `name_before/after`, `icon_before/after`, `enabled_before/after` |
| `ActionPaymentFilterGroupItemsUpdated` | `payment_filter_group.items_updated` | payment_filter_group | `HandleSetItems` (`PUT …/{slug}/items`) changed the group's price items, or a create or put moved items out of it (then `removed` lists them and `moved_to` names the group they went to); not logged when the set is the same | `slug`, `added`, `removed`, `moved_from` (item id → slug of the group it left), `moved_to` (on the group an item left) |
| `ActionPaymentFilterGroupsReordered` | `payment_filter_group.reordered` | payment_filter_group (club id) | `HandleReorder` (`PUT …/order`) changed the order; not logged when it is the same | `order_before`, `order_after` (slugs) |
| `ActionPaymentFilterGroupDeleted` | `payment_filter_group.deleted` | payment_filter_group | `HandleDelete` (`DELETE …/{slug}`); the group's items become unassigned | `slug`, `name`, `price_item_ids` |
| `ActionBroadcastSent` | `broadcast.sent` | broadcast | Communication broadcast emailed/pushed | `recipient_count` |
| `ActionDocumentUploaded` | `document.uploaded` | document | New doc uploaded to the archive | `filename`, `visibility` |
| `ActionDocumentDeleted` | `document.deleted` | document | Doc removed | `filename` |

## Finance

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionFinancialSettingsUpdated` | `update_financial_settings` | clubs (the club id) | `PATCH /admin/settings/site` (Økonomiinnstillinger and the other club settings): any field of the request was saved. The string predates the `<resource>.<verb>` convention and is shipped, so it stays | `fields`: the names of the fields set, sorted. **Never values**: the request carries a secret (`anthropic_api_key`) and board members' addresses and phone. Rows written before BRY-556 held the whole request under `new`, the Anthropic key and board contact details included; migration 000127 deliberately rewrote them all to `{"fields":[...]}` (who and when are kept, the values are not). The other club-settings writes (`update_booking_settings`: allow-listed ints and season dates; `update_session_settings`: three minute counts) and the bank-account create/update (the club's own account number, role, GL code, label) hold only club configuration and log their payload as before |
| `ActionInvoiceCreated` | `invoice.created` | invoice | After `INSERT invoices` succeeds | `invoice_number`, `total_amount` |
| `ActionInvoicePDFGenerated` | `invoice.pdf_generated` | invoice | `finance.GeneratePDF` succeeded and bytes stored | `invoice_number` |
| `ActionInvoiceEmailed` | `invoice.emailed` | invoice | After `SendWithAttachment` succeeds (post-`sent_at` stamp) | `email`, `invoice_number` |
| `ActionInvoiceReminded` | `invoice.reminded` | invoice | Per row inside `HandleBulkSendReminder` after reminder email sent | `email`, `invoice_number` |
| `ActionInvoiceOnlinePaymentSet` | `invoice.vipps_link_set` | invoice | `HandleSetInvoiceOnlinePayment` (per invoice), and the reminder batch's On/Off override (resource_id empty, `count`) | `opt_in` (and `count` for the batch). The value is historical: it predates card payments and is kept so existing rows still match; the setting covers every provider |
| `ActionInvoiceCreditLinked` | `invoice.credit_linked` | invoice (the credit note) | `HandleLinkCreditNote`: a credit note is settled against the faktura it credits | `credit_note_number`, `invoice_number`, `amount`, `outcome` (`settled_pair` / `credit_owed`), `manual_pick` (chosen in the manual search, as the client reports it), `default_candidate` (server-derived: same payer and amount, so the plain candidate list would have offered it), `member_mismatch` (the two bill different payers), `confirm_other_member` (the treasurer confirmed a cross-payer link), `credit_note_has_payer` |
| `ActionInvoiceVoided` | `invoice.voided` | invoice | `HandleVoidInvoice`, after the guarded status update and the GL reversal of the faktura's whole entry family | `invoice_number`, `reversal_entry_ids` (the reversal entries posted; empty when nothing was booked), `gl_reversal_error` when the reversal failed |
| `ActionInvoiceCreditUnlinked` | `invoice.credit_unlinked` | invoice (the credit note) | `HandleUnlinkCreditNote`: the settlement is undone and the faktura reopens | `credit_note_number`, `invoice_id` |
| `ActionInvoiceRefundCreditIssued` | `invoice.refund_credit_issued` | invoice (the credit note) | The Stripe booking pass (`BookStripeBalance`) issued a credit note for a card refund (BRY-446), once per note, after the commit; the actor is the pass's acting user | `credit_note_number`, `invoice_number` (the faktura it credits; 0 for an order with no faktura), `amount_ore`, `refund_txn_id` |
| `ActionInvoiceRegenerated` | `invoice.pdf_regenerated` | invoice | Per row inside `HandleBulkRegeneratePDF` after archive+overwrite succeeds | `invoice_number`, `bank_account`, `prior_pdf_bytes` |
| `ActionInvoiceDeliveryLogViewed` | `invoice.delivery_log_viewed` | invoice | Treasurer opens the Stalwart delivery-log popover for a sent invoice (PII visibility audit — recipient email is exposed in the response) | `recipient_email`, `matches_returned` |
| `ActionFinanceCSVExported` | `finance.csv_exported` | finance | Treasurer downloads a payments/invoices CSV | `format`, `rows` |
| `ActionAccountingInvoicesResplit` | `accounting.invoices_resplit` | fiscal_period | Treasurer applies **Korriger inntektsfordeling** (resplit). Not logged for the preview | `posted`, `account_totals`, `skipped` |
| `ActionAccountingVippsDraftsPosted` | `accounting.vipps_drafts_posted` | fiscal_period | Treasurer applies **Bokfør Vipps-utkast** for a period (one row per batch; not logged for the preview). Each reclassified charge also writes its own `vipps_charge.auto_assigned` | `year`, `drafts`, `posted`, `reclassified`, `refused`, `chosen` (unique ids sent in `reclassify_entry_ids`; null when absent, i.e. all), `unchecked` (reclassifications left out and untouched), `stale` (sent ids that were no reclassification, or whose target changed since the preview), `posted_totals` |
| `ActionAccountingVippsFallbackReclassified` | `accounting.vipps_fallback_reclassified` | journal_entry (resource id empty) | Treasurer applies **Reklassifiser Vipps-inntekter på 3900** (BRY-557); one row per batch, after the charges are processed; not logged for the preview, for a request without `targets`, or when it is refused (auto-assign off, period not open). Individual charges write no `vipps_charge.*` row of their own | `from_account` (3900), `requested` (targets sent), `proposed` (reclassifications the rules give at apply time), `reclassified`, `skipped` (not in the request, target changed, already moved), `failed`, `stale` (sent ids that are no proposal now), `needs_tildel`, `stays`, `totals` (per target account: `account_code`, `count`, `amount`), `replaced` (per moved charge: `entry_number` of the voided bilag, `reversal`, `replacement`, `to`, `amount`) |
| `ActionAccountingDraftDeleted` | `accounting.draft_deleted` | journal_entry | An unlinked draft bilag deleted from the Bilag page (`DeleteDraftJournalEntry`) | — |
| `ActionAccountingStripeSynced` | `accounting.stripe_balance_synced` | stripe_balance | `HandleStripeSync` (`POST /accounting/stripe/sync`) completes its ingest and the booking pass and payout sweep after it; resource id is the club. Not logged when it answers 409 (another Stripe sync or booking running) or the ingest fails; a booking pass that fails after a good ingest is still logged | `fetched`, `inserted`, `needs_review` (count per review reason), `payouts_seen`, `payouts_linked`, `members_linked`, `errors` (count); booking: `charges_booked`, `fees_booked`, `booking_needs_review` (count per review reason), `booking_deferred` (count, closed or missing period), `booking_errors` (count), `booking_failed` (bool); payout sweep: `payouts_cleared`, `payouts_waiting`, `payouts_skipped`, `payouts_failed` (counts), `payout_sweep_failed` (bool) |
| `ActionDeveloperQuery` | `developer.db_query` | developer_query | A read-only SQL query runs through `/admin/dev/query` (admin session) or `/api/v1/dev/query` (developer bearer token, BRY-115). The full SQL is logged: the `brygge_dev_ro` role reads only masked, non-PII data, so the text can't expose identity | `sql` (capped at 8 KB), `sql_sha256`, `columns`, `row_count`, `elapsed_ms`, `truncated`, `token_id` (token path only), `error` (if any) |
| `ActionDeveloperTokenCreated` | `developer.token_created` | developer_token | Admin mints a developer bearer token (BRY-115). The plaintext is never logged | `name`, `prefix`, `scope`, `expires_at` |
| `ActionDeveloperTokenRevoked` | `developer.token_revoked` | developer_token | Admin revokes a developer bearer token | — |
| `ActionPaymentCreated` | `payment.created` | payment | Direct payment row insert (not invoice-paid back-link, which is internal) | `type`, `amount` |
| `ActionPaymentViewed` | `finance.payment_viewed` | payment | Admin opens a payment detail (PII visibility audit) | — |

### Top-level accounting events (resource = `bank`/`vipps`/`bank_import`)

These don't have constants in `audit.go` — they're inlined as strings because each fires once per operator-triggered batch. Consider promoting to constants if more handlers add the same string.

| String | Resource | When | `extra` |
|---|---|---|---|
| `accounting.bank_synced` | `bank` | `HandleBankSync` completes | `kid_matched`, `kid_skipped_settled` (KID-cascade rows left in Tildel because their faktura was already settled), `vipps_reconciled`, `transfers_linked`, `payments_linked`, `closed_periods`, `auto_assigned` (deposits booked by bank auto-assign), `stripe_booked` (Stripe charges and fees booked), `stripe_payouts_cleared` (Stripe payouts cleared) |
| `ActionBankRowAssignedInvoice` | `bank_row.assigned_invoice` | bank_import_row | Operator assigns a row to an open invoice via the Tildel tab (DIL-392). Creates the GL journal + `linkInvoicePayment` | `invoice_id`, `journal_entry_id` |
| `ActionBankRowAutoAssigned` | `bank_row.auto_assigned` | bank_import_row | `AutoAssignBankDeposits` books an unambiguous KID-less deposit to a faktura after a bank import or **Synk bank** (BRY-366). The actor is the user who ran the import or sync; with none, nothing is booked. Undone with `bank_row.unassigned` | `rule` (`member_invoice`), `target` ("faktura N"), `invoice_id`, `journal_entry_id`, `match_pass` (`alias` / `full_name` / `first_last`), `trigger` (`bank_import` / `bank_sync`) |
| `ActionBankRowAssignedAccount` | `bank_row.assigned_account` | bank_import_row | Operator assigns a row to a GL account (DIL-392). `kind` = `expense` (outgoing), `revenue` (incoming, not a faktura) or `group` (one row of rows booked together as one bilag, BRY-380; one audit row per row) | `account_code`, `kind`, `journal_entry_id`, `confirm_alone` (single row booked despite a strong partner); for `group`: `group_row_ids`, `net` |
| `ActionBankRowDismissed` | `bank_row.dismissed` | bank_import_row | Operator marks a row not-actionable. `reason` is one of the enum values; no free text (DIL-392 + DIL-394 follow-up). Also written by `SupersedeProvisionalBankRows` when a provisional row gives way to its settled row (BRY-378), actor = the importing or syncing user | `reason`; on the automatic path `superseded_by`, `automatic` |
| `ActionBankRowRefreshed` | `bank_row.refreshed` | bank_import_row | A bank import updated a provisional in-flight row in place with the settled text of an incoming row carrying the same bank reference (BRY-378) | `reference`, `bank_import_id`; `old_date`, `new_date` when the settled row is dated later |
| `ActionBankRowRefundPaired` | `bank_row.refund_paired` | bank_import_row (the inbound row) | `HandlePairRefund`: an incoming row and the outgoing refund of it are booked as a pair | `outbound_row_id`, `inbound_journal_id`, `outbound_journal_id` |
| `ActionBankRowUnassigned` | `bank_row.unassigned` | bank_import_row | Operator reverses a prior assignment: voids the journal, removes the payments row, resets `journal_entry_id` (DIL-392) | `prior_invoice_id`, `prior_journal_entry_id`, `sibling_row_ids` (other rows released with it: a group's, a transfer's or a multi-row faktura's), `reopened_invoice_ids` (every faktura whose payment was removed), `released_vipps_row_ids` (the Vipps rows of a legacy settlement released with it) |
| `ActionPayerAliasTaught` | `payer_alias.taught` | vipps_customer_alias | A payer name is saved as a member's alias (BRY-383): from Tildel when a treasurer assigns a bank deposit to a faktura with **Hugs betalaren** ticked (in the faktura-lock transaction, so it fires only when the booking succeeded), or from the Vipps queue's **Alias**. The payer name itself is not logged | `user_id` (the member), `source` (`bank_tildel` / `vipps_queue`), `override` (the name was another member's own name or alias and the treasurer confirmed taking it over; always `false` from the Vipps queue) |
| `ActionPayerAliasRemoved` | `payer_alias.removed` | vipps_customer_alias | A treasurer deletes an alias from the Vipps queue's **Lærte alias** list (BRY-383) | `user_id` (the member it pointed at), `source` (`vipps_queue`) |
| `ActionVippsChargeAssignedInvoice` | `vipps_charge.assigned_invoice` | vipps_import_row | Operator assigns a Vipps charge to a faktura in Tildel (`AssignVippsChargeToInvoice`). A refusal (for example 409 `imported_no_opening_balance`) is not logged | `invoice_id`, `journal_entry_id` |
| `ActionVippsChargeBilag` | `vipps_charge.bilag` | vipps_import_row | Operator books a Vipps charge to an account with **Lag bilag** | `account_code`, `description`, `journal_entry_id` |
| `ActionVippsChargeDismissed` | `vipps_charge.dismissed` | vipps_import_row | Operator dismisses a charge (avvist), so it leaves the queue and no longer blocks its settlement's payout clear | `reason` |
| `ActionVippsPayoutCleared` | `vipps_payout.cleared` | bank_import_row (the payout deposit) | `HandleClearVippsPayout` books the fee and bank entries for a settlement whose charges are all booked or dismissed. Not logged when charges remain or the payout was already cleared | `settlement`, `fee_entry_id`, `bank_entry_id`, `clearing_balance` |
| `ActionStripePayoutCleared` | `stripe_payout.cleared` | stripe_payout (the `stripe_payouts` row) | `HandleClearStripePayout` (`POST /accounting/stripe/payouts/{payoutID}/clear`) books a Stripe payout's deposit a person picked (`ClearStripePayoutManually`). Not logged on a refusal (409 with a code), a busy Stripe slot or an unknown payout or row. The automatic sweep is counted in `accounting.stripe_balance_synced` / `accounting.bank_synced` instead | `payout_id` (Stripe's id), `bank_row_id`, `journal_entry_id`, `manual` (true), `members_count`, `members_net_ore`, `clearing_balance_ore` (the Stripe clearing account remainder over the members and the entry, øre; absent for a payout without members and when it could not be read) |
| `ActionStripeTxnAssignedInvoice` | `stripe_txn.assigned_invoice` | stripe_balance_transaction | `HandleAssignStripeTxnToInvoice` (`POST /accounting/stripe/transactions/{txnID}/assign-invoice`) books a Stripe charge in review against a faktura (BRY-466) | `balance_transaction_id`, `invoice_id`, `journal_entry_id` |
| `ActionStripeTxnAssignedAccount` | `stripe_txn.assigned_account` | stripe_balance_transaction | `HandleAssignStripeTxnToAccount` (`…/assign-account`) books a Stripe row in review to an account | `balance_transaction_id`, `account_code`, `note`, `journal_entry_id` |
| `ActionStripeTxnDismissed` | `stripe_txn.dismissed` | stripe_balance_transaction | `HandleDismissStripeTxn` (`…/dismiss`) sets a Stripe row in review aside; not logged when it was dismissed already | `balance_transaction_id`, `reason`, `note`, `review_reason` |
| `ActionStripeTxnUndismissed` | `stripe_txn.undismissed` | stripe_balance_transaction | `HandleUndismissStripeTxn` (`…/undismiss`) takes a dismissal back; not logged when the row was not dismissed | `balance_transaction_id`, `status` (the state it returned to), `review_reason` |
| `ActionStripeTxnUnassigned` | `stripe_txn.unassigned` | stripe_balance_transaction | `HandleUnassignStripeTxn` (`…/unassign`) undoes a Stripe row's booking: voids its entry, reopens the fakturas it settled and holds the row in review (`unassigned`) (BRY-467). Not logged on a refusal (409 with a code), a busy Stripe slot or an unknown row | `balance_transaction_id`, `journal_entry_id` (the voided entry), `reversal_entry_id` (empty when it was voided already), `already_voided`, `invoice_ids` (the fakturas reopened) |
| `ActionStripeTxnRebooked` | `stripe_txn.rebooked` | stripe_balance_transaction | `HandleRebookStripeTxn` (`…/rebook`) hands a held row back to the booking pass, which books it in the same request. Logged once the row is released, whatever the pass then did | `balance_transaction_id`, `outcome` (`booked`, `needs_review`, `deferred`, `failed`), `reason` (the review or deferral reason), `journal_entry_id` (the new entry when booked) |
| `ActionOrderRefundRequested` | `order.refund_requested` | order | `OrderRefundHandler.HandleRefundOrder` (`POST /admin/orders/{orderID}/refund`) started a refund (BRY-497): logged once the request is stored and the provider answered or its answer is awaited, also when the provider refused it; not logged for a repeated request key or a refusal (409/400). The actor is the treasurer or admin; nothing is booked here | `refund_id`, `attempt_id`, `amount_ore`, `outcome` (`credit` / `recollect` / null), `request_key`, `status` (`requested` / `succeeded` / `failed`), `reason` |
| `ActionInvoiceReissuedAfterRefund` | `invoice.reissued_after_refund` | invoice (the credited faktura) | `InvoiceHandler.HandleReissueAfterRefund` (`POST /admin/financials/invoices/{invoiceID}/reissue-after-refund`, BRY-498) issued a new faktura for a faktura that card refunds credited in full: logged once the new faktura is stored, whether or not it could be emailed. The actor is the treasurer or admin. The new faktura also gets the usual `invoice.created` (with `reissue_of_invoice_number`) and, once emailed, `invoice.emailed` | `invoice_number` (the credited faktura), `new_invoice_id`, `new_invoice_number`, `total`, `sent` (it was emailed) |
| `ActionStripeTxnRefundBooked` | `stripe_txn.refund_booked` | stripe_balance_transaction (the refund or refund failure) | The Stripe booking pass booked a card refund or a refund failure (BRY-446), after the commit | `balance_transaction_id`, `category` (`refund` / `refund_failure`), `origin_txn_id` (the charge, or the refund a failure books against), `outcome` (`credit`, `recollect`, `account`, `duplicate`, `mirror`), `amount_ore`, `document_numbers` (credit notes, or the debit fakturas a failure issued) |
| `ActionStripeTxnDisputeBooked` | `stripe_txn.dispute_booked` | stripe_balance_transaction (the dispute or reversal) | The Stripe booking pass booked a card dispute (money withdrawn) or its reversal (the club won) to the dispute account (BRY-446), after the commit; the actor is the pass's acting user | `balance_transaction_id`, `category` (`dispute` / `dispute_reversal`), `origin_txn_id` (the charge, empty when not stored), `account` (the dispute account code), `amount_ore`, `fee_ore` |
| `ActionStripeTxnDuplicateRefunded` | `stripe_txn.duplicate_refunded` | stripe_balance_transaction (the charge) | The Stripe booking pass booked a duplicate card capture with the refund that returned all of it, through the suspense account (BRY-446) | `refund_txn_id`, `amount_ore` |
| `ActionVippsChargeAutoAssigned` | `vipps_charge.auto_assigned` | vipps_import_row | `AutoAssignVippsCharges` books an unambiguous charge after an import or Synk Vipps (BRY-340). The actor is the user whose action triggered the run | `rule` (`member_invoice` / `price_item`), `target`, `journal_entry_id`; a charge rebooked by the Vipps backlog (`rebookVippsChargeTx`) also carries `prior_journal_entry_id` and `prior_invoice_id` (the draft it replaced and the faktura it had settled, empty for none) |
| `ActionVippsChargeUnassigned` | `vipps_charge.unassigned` | vipps_import_row | Operator undoes a charge booking (manual or automatic): draft deleted or posted entry voided, settled faktura reopened, charge back in Tildel (BRY-340). Also a release of a 1500-parked backlog draft from the **Bokfør Vipps-utkast** preview (BRY-392) | `journal_entry_id`, `invoice_id`, `released_from_backlog` (true on a backlog release only) |
| `accounting.account_created` | `account` | `HandleCreateAccount` adds a custom account to the kontoplan | `code`, `name` |
| `accounting.account_updated` | `account` | `HandleUpdateAccount` renames an account or changes its description or MVA eligibility. System accounts take a name and description only (BRY-377) | `code`, `name`, `previous_name`, `description`, `previous_description`, `mva_eligible` (the effective value: the stored one when the request omits it) |
| `accounting.vipps_resynced` | `vipps` | `HandleResyncVipps` completes | `scanned`, `resynced`, `skipped`, `failed` |
| `accounting.bank_import_reassigned` | `bank_import` | `HandleReassignBankImport` succeeds (cascades through journal_lines) | `from`, `to`, `cascaded_journals` |
| `accounting.bank_imported` | `bank_import` | `HandleImportBankStatement` after rows ingested | `format`, `rows_total`, `imported`, `skipped_dup`, `matched`, `kid_skipped_settled`, `transfers`, `auto_assigned`, `stripe_booked`, `stripe_payouts_cleared` |

## Booking

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionBookingConfirmed` | `booking.confirmed` | booking | Booking moves from `pending` to `confirmed` (after payment or manual approval) | `booking_id`, `resource_type` |

## Shared inbox

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionInboxACLChanged` | `inbox.acl_changed` | inbox | Reconciler grants/revokes mailbox ACLs based on role membership | `mailbox`, `users_added`, `users_removed` |
| `ActionInboxACLSyncFailed` | `inbox.acl_sync_failed` | inbox | Reconciler couldn't reach Stalwart or got an unexpected response | `mailbox`, `error` |
| `ActionInboxThreadViewed` | `inbox.thread_viewed` | inbox | User opens a thread in the shared-inbox UI | `mailbox`, `thread_id` |
| `ActionInboxThreadArchived` | `inbox.thread_archived` | inbox | User archives a thread | `mailbox`, `thread_id` |
| `ActionInboxMarkRead` | `inbox.mark_read` | inbox | User marks read in bulk (or via auto-mark on view) | `mailbox`, `count` |
| `ActionInboxMessageSent` | `inbox.message_sent` | inbox | Compose flow successfully submits a message | `mailbox`, `to` |

## GDPR + legal

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionGDPRExportRequested` | `gdpr.export_requested` | user | Member kicks off an export from My page → Privacy | — |
| `ActionGDPRDeletionRequested` | `gdpr.deletion_requested` | user | Member kicks off the deletion countdown | `cooling_off_days` |
| `ActionGDPRDeletionCancelled` | `gdpr.deletion_cancelled` | user | Member cancels during the cooling-off window | — |
| `ActionGDPRDeletionProcessed` | `gdpr.deletion_processed` | user | Cooling-off elapsed; non-retained data wiped, member deactivated with sessions and credentials revoked, identity erased or deferred behind retention | `request_id` (the `deletion_requests` row; `resource_id` is the erased user, `actor_id` the processing admin, `actor_ip` their address), `records_wiped`, `identity_erased` (bool), `retention_clears_at`, `sessions_revoked` (session rows deleted, including expired ones not yet purged) |
| `ActionLegalDocumentCreated` | `legal_document.created` | legal_document | New club bylaw / vedtekt uploaded | `kind`, `version` |
| `ActionNotificationConfigUpdated` | `notification_config.updated` | user | User updates notification prefs (per-channel, per-category) | — |

---

## The audit row shape

```json
{
  "id": "uuid",
  "club_id": "uuid",
  "actor_id": "uuid",       // user who triggered the action
  "remote_addr": "1.2.3.4",
  "action": "invoice.reminded",
  "resource_type": "invoice",
  "resource_id": "uuid",
  "extra": {                // free-form JSON
    "email": "member@example.com",
    "invoice_number": 42
  },
  "created_at": "2026-06-13T12:34:56Z"
}
```

Read via `SELECT * FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at DESC`. The `extra` JSON is `jsonb`, so `extra->>'invoice_number'` works in WHERE clauses.

## Querying examples

```sql
-- Every faktura reminder sent to a specific member
SELECT created_at, resource_id, extra->>'invoice_number' AS invoice
  FROM audit_log
 WHERE club_id = $1
   AND action = 'invoice.reminded'
   AND extra->>'email' = $2
 ORDER BY created_at DESC;

-- Every TOTP recovery code redemption (security review)
SELECT created_at, actor_id, extra->>'codes_remaining' AS remaining
  FROM audit_log
 WHERE club_id = $1
   AND action = 'admin.totp_recovery_redeemed'
 ORDER BY created_at DESC
 LIMIT 50;

-- All accounting batch operations from the last week
SELECT created_at, action, extra
  FROM audit_log
 WHERE club_id = $1
   AND action LIKE 'accounting.%'
   AND created_at > now() - interval '7 days'
 ORDER BY created_at DESC;
```

## Adding a new action

Per [`checklists/add-audit-action.md`](../checklists/add-audit-action.md):

1. Add constant to `internal/audit/audit.go` (alphabetical within its block — Auth, TOTP, User, Slip, Content, Finance, etc.)
2. Use from the handler behind `if h.audit != nil`
3. Add a row to this doc with: constant, string, resource, when, `extra` fields
4. Action string: `<resource>.<verb>` past tense

## Action string permanence

**Action strings, once shipped, are permanent.** Any operator script or compliance report that filters on `action = 'invoice.emailed'` breaks if the string changes. Renaming is effectively impossible without a migration that rewrites historical rows.

If you must rename, write the migration: `UPDATE audit_log SET action = 'new.name' WHERE action = 'old.name'`. The Go constant can change freely — only the string is the public API.
