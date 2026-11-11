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
| `ActionLoginSuccess` | `auth.login_success` | session | Once per login, after the session is created and its cookie set (BRY-659): magic-link `POST /auth/verify` (`HandleConsumeMagicLink`), passkey `POST /auth/passkeys/login/finish` (`HandleLoginFinish`), and `POST /join/confirm` (`HandleJoinConfirm`, which logs the new applicant in). Actor and club are the logged-in user's; `resource_id` empty | `method`: `magic_link`, `passkey`, `join_confirm`. Nothing else: no token, token hash, session id, credential id or address |
| `ActionLoginFailed` | `auth.login_failed` | session | Once per refused login attempt that presented a credential (BRY-659). Magic link: a token that cannot be consumed, refused either by the login (`POST /auth/verify`) or by the verify page's peek (`POST /auth/verify/peek`, BRY-660; `stage: peek`), which judges it by the same rules; a successful peek writes nothing; the actor is the user the link was mailed to (looked up by the link row's address, active or deactivated), NULL for an unknown token, whose row is scoped to the deployment's club (`CLUB_SLUG`). Passkey: an assertion that fails verification; the actor is the member only when the credential id is one of theirs, else NULL, with the club of the user handle or of `CLUB_SLUG`. A deactivated member's handle, and a user lookup that errors, resolve to no one, so both are recorded as `passkey_unknown_credential` with no actor. A row with no actor is written at most once a minute per client IP bucket, method and reason (Redis `SET NX` on `loginfail:<ip bucket>:<method>:<reason>`, 60 s; `SetLoginFailDedupe`), so random tokens from one client cannot flood the table. Further ones in the window are dropped, not counted. The IP bucket is `middleware.IPBucket`: an IPv4 address, or an IPv6 client's /64 (BRY-662), so rotating addresses within a /64 does not escape the dedupe. A row naming a user is never deduped. Without Redis, or when Redis errors, the row is written; the login routes' strict limiter fails closed on the same outage, so such requests mostly never reach the handler. Not written for requests that present no credential: rate-limited (429, the limiter's log covers that volume), refused because the limiter cannot read Redis (503 `rate_limit_unavailable`), wrong content type (415), malformed body (400), an empty token, a missing or expired passkey ceremony, or a body that does not parse as an assertion. On a row with no actor the client IP (`actor_ip`) is the only personal data; it and the rest of the row follow the general audit retention (BRY-651) | `method`: `magic_link`, `passkey`; `reason`: `invalid` (unknown token, or the user was deactivated or is not a person; 410 `login_token_invalid`), `used` (410 `login_token_used`), `expired` (410 `login_token_expired`), `passkey_unknown_credential` (the user handle or the credential id resolves to no member's passkey), `passkey_verification_failed` (the member's credential, but the assertion did not verify); `stage`: `peek` when the verify page's peek refused the token, absent when the login itself did. Peek rows have their own dedupe: at most one an hour per subject, method, reason and stage, where the subject is the user for an attributed row and the IP bucket for an anonymous one (Redis `SET NX` on `loginfail:peek:user:<id>:<method>:<reason>` or `loginfail:peek:ip:<bucket>:<method>:<reason>`, 1 h; written when Redis is missing or errors), so a member reopening an old email writes one row an hour and repeated attempts still show hourly. The login's own rows keep the dedupe above (anonymous only, 60 s; attributed always written). No token, hash or address, hashed or not |
| `ActionLogout` | `auth.logout` | session | `POST /auth/session/logout` (`HandleSessionLogout`), after the session delete is attempted and the cookie cleared (BRY-659). Actor and club are the member's; `resource_id` empty (the session id is the cookie value) | — |
| `ActionTokenRevoked` | `auth.token_revoked` | session | Reserved, currently unused: nothing writes it. Logout writes `auth.logout`; an admin passkey reset writes `admin.passkey_reset_by_admin` | — |
| `ActionInvalidToken` | `auth.invalid_token` | session | Reserved, currently unused: nothing writes it (a session cookie that does not validate is answered 401 by the middleware without an audit row) | — |

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
| `ActionUserDeactivated` | `user.deactivated` | user | A member is deactivated (`DeactivateMemberTx`; reversible; hides + blocks login, keeps data, revokes their developer tokens): by an admin or the board, or by the daily membership-end job as the club's system actor when a cancelled membership has ended (with `membership.ended`) | — (ids only; the member is `resource_id`) |
| `ActionUserReactivated` | `user.reactivated` | user | Admin reactivates a deactivated member | — (ids only) |
| `ActionMemberPIIPurged` | `user.pii_purged` | user | Scheduled sweep erases a deactivated member's PII once retention clears (actor is NULL — system job) | `reason` |
| `ActionUserRejoinBlockedSet` | `user.rejoin_blocked_set` | user | `PATCH /admin/users/{id}` set or cleared the board's "can't rejoin" mark (BRD-15), in the update's transaction; written whenever the field is sent | `rejoin_blocked` (bool, the new value) |
| `ActionUserMailProvisioned` | `user.mail_provisioned` | user | Stalwart mailbox created for the user | `jmap_user` (the principal slug, no PII) |
| `ActionUserMailDeprovisioned` | `user.mail_deprovisioned` | user | Stalwart mailbox removed | `jmap_user` |
| `ActionUserHarborShareSet` | `user.harbor_share_set` | user | The harbor share import (BRY-139) set a member as owner or filled in a missing paid date; one row per member it changed, in the import's transaction. A PATCH of the same fields logs `update_user` instead. | `source` (`import`); never the values |
| `ActionUserHarborSharesImported` | `user.harbor_shares_imported` | user | Summary of one applied harbor share import; no `resource_id` | the per-outcome row counts (`set`, `date_added`, `unchanged`, `conflict`, `not_owner_but_set`, `duplicate`, `skipped`, `unmatched`, `ambiguous`, `error`); counts only |

`HandleDeleteUser` (`DELETE /admin/users/{userID}`, which always anonymises in place) writes the raw action string `delete_user` rather than `ActionUserDeleted`: resource `user`, `extra` `sessions_revoked` (session rows deleted by `revokeMemberAccess`, including expired ones). The string is shipped, so it stays. Rows written before BRY-536 also carry `old_email`, `old_full_name` and `erasure`; the identity keys are scrubbed for members who are erased.

The other member writes in `admin_users.go` also use raw strings: `create_user` (`roles`), `update_user` (`fields`, the names of the fields changed, never their values; a harbour share edit names `harbor_share_owner` / `harbor_share_paid_on`, and removing ownership names both because it clears the date) and `update_roles` (`old_roles`, `new_roles`). **Audit rows about a member hold ids only.** A name, email, phone or address in `details` outlives the member's erasure; the erasure paths remove `audit.MemberPIIKeys` from the member's rows (`audit.ScrubMemberPII`), and a new action about a member must not write those keys. Rows that name a recipient rather than a member (`invoice.emailed`, `invoice.reminded`, `invoice.delivery_log_viewed`, `invoice.recipient_unsuppressed`, all keyed on the faktura) are outside that scrub.

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
| `ActionFinancialSettingsUpdated` | `update_financial_settings` | clubs (the club id) | `PATCH /admin/settings/site` (Økonomiinnstillinger and the other club settings): any field of the request was saved. The string predates the `<resource>.<verb>` convention and is shipped, so it stays | `fields`: the names of the fields set, sorted; when `join_price_item_id` is sent, also `join_price_item_id: {old, new}` (the price item ids, "" = joining closed: catalogue ids, neither personal data nor a secret, BRY-634); when `municipality_codes` is sent, also `municipality_codes: {old, new}` (both lists, `[]` when empty: public municipality numbers, neither personal data nor a secret, and they decide who counts as local, BRY-643). **No other values**: the request carries a secret (`anthropic_api_key`) and board members' addresses and phone. Rows written before BRY-556 held the whole request under `new`, the Anthropic key and board contact details included; migration 000127 deliberately rewrote them all to `{"fields":[...]}` (who and when are kept, the values are not). The other club-settings writes (`update_booking_settings`: allow-listed ints and season dates; `update_session_settings`: three minute counts) and the bank-account create/update (the club's own account number, role, GL code, label) hold only club configuration and log their payload as before |
| `ActionInvoiceCreated` | `invoice.created` | invoice | After `INSERT invoices` succeeds. Also written by **Betal med bank** (`POST /join/bank-invoice`, BRY-631) when it issues a join faktura (not on a repeat call that returns the same one); the actor is the applicant | `invoice_number`, `total_amount`; the join faktura instead logs `invoice_number`, `kid`, `total`, `user_id`, `purpose` (`membership_join`) and `order_id` (its pay order) |
| `ActionInvoicePDFGenerated` | `invoice.pdf_generated` | invoice | `finance.GeneratePDF` succeeded and bytes stored | `invoice_number` |
| `ActionInvoiceEmailed` | `invoice.emailed` | invoice | After `SendWithAttachment` succeeds (post-`sent_at` stamp). `HandleSendInvoice` also logs it when the send's outcome is unknown (`email.ErrDeliveryAmbiguous`): the faktura keeps `sent_at` and is booked, since the member may have it (BRY-589) | `email`, `invoice_number`, `delivery_unknown` (only on an ambiguous send). The join faktura's email (BRY-631, actor the applicant) logs `invoice_number` only |
| `ActionInvoiceReminded` | `invoice.reminded` | invoice | Per row inside `HandleBulkSendReminder` after reminder email sent | `email`, `invoice_number` |
| `ActionInvoiceOnlinePaymentSet` | `invoice.vipps_link_set` | invoice | `HandleSetInvoiceOnlinePayment` (per invoice), and the reminder batch's On/Off override (resource_id empty, `count`) | `opt_in` (and `count` for the batch). The value is historical: it predates card payments and is kept so existing rows still match; the setting covers every provider |
| `ActionInvoiceCreditLinked` | `invoice.credit_linked` | invoice (the credit note) | `HandleLinkCreditNote`: a credit note is settled against the faktura it credits | `credit_note_number`, `invoice_number`, `amount`, `outcome` (`settled_pair` / `credit_owed`), `manual_pick` (chosen in the manual search, as the client reports it), `default_candidate` (server-derived: same payer and amount, so the plain candidate list would have offered it), `member_mismatch` (the two bill different payers), `confirm_other_member` (the treasurer confirmed a cross-payer link), `credit_note_has_payer` |
| `ActionInvoiceVoided` | `invoice.voided` | invoice | `HandleVoidInvoice`, after the guarded status update and the GL reversal of the faktura's whole entry family | `invoice_number`, `reversal_entry_ids` (the reversal entries posted; empty when nothing was booked), `gl_reversal_error` when the reversal failed |
| `ActionInvoiceCreditUnlinked` | `invoice.credit_unlinked` | invoice (the credit note) | `HandleUnlinkCreditNote`: the settlement is undone and the faktura reopens | `credit_note_number`, `invoice_id` |
| `ActionInvoiceRefundCreditIssued` | `invoice.refund_credit_issued` | invoice (the credit note) | The Stripe booking pass (`BookStripeBalance`) issued a credit note for a card refund (BRY-446), once per note, after the commit; the actor is the pass's acting user: the person who synced, or the club's system actor when the automatic Stripe run booked it (boot, nightly, or `stripe_event` after a webhook, BRY-570) | `credit_note_number`, `invoice_number` (the faktura it credits; 0 for an order with no faktura), `amount_ore`, `refund_txn_id` |
| `ActionInvoiceRegenerated` | `invoice.pdf_regenerated` | invoice | Per row inside `HandleBulkRegeneratePDF` after archive+overwrite succeeds | `invoice_number`, `bank_account`, `prior_pdf_bytes` |
| `ActionInvoiceDeliveryLogViewed` | `invoice.delivery_log_viewed` | invoice | Treasurer opens the delivery log for a sent invoice (PII visibility audit — recipient email and the remote servers' replies are exposed in the response) | `recipient_email`, `matches_returned` (journal events read for the recipient) |
| `ActionInvoiceRecipientUnsuppressed` | `invoice.recipient_unsuppressed` | invoice | Treasurer removes the recipient of one faktura send from the Amazon SES suppression list (`HandleUnsuppressRecipient`, BRY-590), after SES answered; also when the address was already off the list | `send_id` (the `email_sends` row), `recipient_email`, `was_listed` (false when SES answered NotFound) |
| `ActionFinanceCSVExported` | `finance.csv_exported` | finance | Treasurer downloads a payments/invoices CSV | `format`, `rows` |
| `ActionAccountingInvoicesResplit` | `accounting.invoices_resplit` | fiscal_period | Treasurer applies **Korriger inntektsfordeling** (resplit). Not logged for the preview | `posted`, `account_totals`, `skipped` |
| `ActionAccountingVippsDraftsPosted` | `accounting.vipps_drafts_posted` | fiscal_period | Treasurer applies **Bokfør Vipps-utkast** for a period (one row per batch; not logged for the preview). Each reclassified charge also writes its own `vipps_charge.auto_assigned` | `year`, `drafts`, `posted`, `booking_deferred` (the Vipps booking slot stayed busy, so the payout sweep did not run), `reclassified`, `refused`, `chosen` (unique ids sent in `reclassify_entry_ids`; null when absent, i.e. all), `unchecked` (reclassifications left out and untouched), `stale` (sent ids that were no reclassification, or whose target changed since the preview), `posted_totals` |
| `ActionAccountingVippsFallbackReclassified` | `accounting.vipps_fallback_reclassified` | journal_entry (resource id empty) | Treasurer applies **Reklassifiser Vipps-inntekter på 3900** (BRY-557); one row per batch, after the charges are processed; not logged for the preview, for a request without `targets`, or when it is refused (auto-assign off, period not open). Individual charges write no `vipps_charge.*` row of their own | `from_account` (3900), `requested` (targets sent), `proposed` (reclassifications the rules give at apply time), `reclassified`, `skipped` (not in the request, target changed, already moved), `failed`, `stale` (sent ids that are no proposal now), `needs_tildel`, `stays`, `totals` (per target account: `account_code`, `count`, `amount`), `replaced` (per moved charge: `entry_number` of the voided bilag, `reversal`, `replacement`, `to`, `amount`) |
| `ActionAccountingPeriodReferenceSet` | `accounting.period_reference_set` | fiscal_period | `HandleSetPeriodReference` (`POST /accounting/periods/{periodID}/reference`) marks a year as a reference year (BRY-418). Not logged when the period already had the value, nor on a refusal | `year`, `status`, `drafts` (draft entries the marking leaves in place) |
| `ActionAccountingPeriodReferenceCleared` | `accounting.period_reference_cleared` | fiscal_period | The same route clears the mark. Not logged for a no-op or a refusal | `year`, `status` |
| `ActionBankRowUndismissed` | `bank_row.undismissed` | bank_import_row | `HandleUndismiss` (`POST /accounting/bank-rows/{rowID}/undismiss`) returns a dismissed outgoing bank row of a reference year to the Referanseår list (BRY-418). A `superseded` or `duplicate` dismissal is final and answers 409. Not logged on a 409 (not such a row) | `prior_reason`, `prior_dismissed_by`, `prior_dismissed_at` (the dismissal the undo removed) |
| `ActionAccountingDraftDeleted` | `accounting.draft_deleted` | journal_entry | An unlinked draft bilag deleted from the Bilag page (`DeleteDraftJournalEntry`) | — |
| `ActionAccountingStripeSynced` | `accounting.stripe_balance_synced` | stripe_balance | `HandleStripeSync` (`POST /accounting/stripe/sync`) completes its ingest and the booking pass and payout sweep after it; resource id is the club. Not logged when it answers 409 (another Stripe sync or booking running) or the ingest fails; a booking pass that fails after a good ingest is still logged | `fetched`, `inserted`, `needs_review` (count per review reason), `payouts_seen`, `payouts_linked`, `members_linked`, `errors` (count); booking: `charges_booked`, `fees_booked`, `booking_needs_review` (count per review reason), `booking_deferred` (count, closed or missing period), `booking_errors` (count), `booking_failed` (bool); payout sweep: `payouts_cleared`, `payouts_waiting`, `payouts_skipped`, `payouts_failed` (counts), `payout_sweep_failed` (bool) |
| `ActionAccountingAutoBookingRun` | `accounting.auto_booking_run` | accounting | `RunAutomaticBooking` (ACC-13, BRY-567), the automatic booking run at API start (`trigger: boot`) and every 24 h (`nightly`), and `RunAutomaticStripeBooking` (BRY-570, `provider: stripe`) at the same times and when a Stripe webhook kicked it (`trigger: stripe_event`), when the club has `automatic_booking` on and the run changed something (a booking, a cleared payout, a KID match, a transfer draft), failed, was aborted (its context ended; written anyway), was `busy` (the Vipps booking slot, or for Stripe the club's Stripe slot, stayed held past the run's 2-minute wait) or could not ingest, or a payout failed to clear. One row per run and provider; resource id is the club. The actor is the club's system actor (NULL when it could not be resolved); IP is empty. Also written when it healed a payment back-link, and for a run cancelled during its ingest (`aborted`). A stuck state (a settlement that does not net to 0, a payout in a closed period, a step that keeps failing) writes a row on every run until a person fixes it. Not written with the switch off, with accounting off, or for a run that changed nothing | `provider` (`vipps` or `stripe`), `trigger` (`boot`, `nightly`, `stripe_event`), `busy`, `aborted`, `ingest_failed`, `duration_ms`, `ingest` {`msn`, `imported`, `failed_dates`, `ledger_failed`}, `vipps` {`repaired`, `orders_booked`, `orders_skipped`, `auto_assigned`, `needs_review`, `declined_skipped`, `payouts_cleared` [settlement], `payouts_waiting`, `payouts_skipped`, `payouts_failed` [{`settlement`, `reason`}: posting refused, or a settlement that does not net to 0 (BRY-560)], `payouts_error`} (absent when the Vipps step did not run), `vipps_skipped_legacy`, `bank` {`kid_matched`, `kid_skipped_settled`, `transfers_linked` (draft bilags), `payments_linked`, `auto_assigned`, `by_reason`, `declined_skipped`, `error`}, `missing_periods`, `closed_periods`, `fiscal_year_notices`, `errors`. A Stripe row has `provider`, `trigger`, `busy`, `aborted`, `ingest_failed`, `duration_ms`, `errors`, `ingest` {`fetched`, `inserted`, `payouts_seen`, `errors`} and `stripe` {`charges_booked`, `fees_booked`, `refunds_booked`, `disputes_booked`, `deferred`, `needs_review`, `booking_errors`, `booking_error`, `payouts_cleared` [payout id], `payouts_failed` [{`payout`, `reason`}], `payouts_waiting`, `payouts_skipped`, `declined_skipped`, `payouts_error`}. Ids and counts only; each list is capped at 20 with the rest counted in `<list>_truncated` |
| `ActionDeveloperQuery` | `developer.db_query` | developer_query | A read-only SQL query runs through `/admin/dev/query` (admin session) or `/api/v1/dev/query` (developer bearer token, BRY-115). The full SQL is logged: the `brygge_dev_ro` role reads only masked, non-PII data, so the text can't expose identity | `sql` (capped at 8 KB), `sql_sha256`, `columns`, `row_count`, `elapsed_ms`, `truncated`, `token_id` (token path only), `error` (if any) |
| `ActionDeveloperTokenCreated` | `developer.token_created` | developer_token | Admin mints a developer bearer token (BRY-115). The plaintext is never logged | `name`, `prefix`, `scope`, `expires_at` |
| `ActionDeveloperTokenRevoked` | `developer.token_revoked` | developer_token | Admin revokes a developer bearer token | — |
| `ActionPaymentCreated` | `payment.created` | payment | Direct payment row insert (not invoice-paid back-link, which is internal) | `type`, `amount` |
| `ActionPaymentViewed` | `finance.payment_viewed` | payment | Admin opens a payment detail (PII visibility audit) | — |

### Top-level accounting events (resource = `bank`/`vipps`/`bank_import`)

These don't have constants in `audit.go` — they're inlined as strings because each fires once per operator-triggered batch. Consider promoting to constants if more handlers add the same string.

| String | Resource | When | `extra` |
|---|---|---|---|
| `accounting.bank_synced` | `bank` | `HandleBankSync` completes | `kid_matched`, `kid_skipped_settled` (KID-cascade rows left in Tildel because their faktura was already settled), `vipps_reconciled`, `transfers_linked`, `payments_linked`, `closed_periods`, `missing_periods` (years with no fiscal period: payments the passes left for the treasurer to create the year, BRY-578), `auto_assigned` (deposits booked by bank auto-assign), `stripe_booked` (Stripe charges and fees booked), `stripe_payouts_cleared` (Stripe payouts cleared), `declined_skipped` (rows the automatic passes left because a person's undo declined them, ACC-13(b), BRY-565: the legacy Vipps pass (`legacy_vipps_declined` in the response), bank auto-assign and both payout sweeps; the KID cascade and transfer pairing leave them without a count), `booking_deferred` (pass 5 could not take the Vipps booking slot) |
| `ActionBankRowAssignedInvoice` | `bank_row.assigned_invoice` | bank_import_row | Operator assigns a row to an open invoice via the Tildel tab (DIL-392). Creates the GL journal + `linkInvoicePayment` | `invoice_id`, `journal_entry_id` |
| `ActionBankRowAutoAssigned` | `bank_row.auto_assigned` | bank_import_row | `AutoAssignBankDeposits` books an unambiguous KID-less deposit to a faktura after a bank import or **Synk bank** (BRY-366), and in the automatic booking run (BRY-567). The actor is the user who ran the import or sync, or in the run the club's system actor; with none, nothing is booked. Undone with `bank_row.unassigned`, after which the rule leaves the row (counted as `declined`) until a person books it | `rule` (`member_invoice`), `target` ("faktura N"), `invoice_id`, `journal_entry_id`, `match_pass` (`alias` / `full_name` / `first_last`), `trigger` (`bank_import` / `bank_sync`, or the run's `boot` / `nightly`) |
| `ActionBankRowAssignedAccount` | `bank_row.assigned_account` | bank_import_row | Operator assigns a row to a GL account (DIL-392). `kind` = `expense` (outgoing), `revenue` (incoming, not a faktura) or `group` (one row of rows booked together as one bilag, BRY-380; one audit row per row) | `account_code`, `kind`, `journal_entry_id`, `confirm_alone` (single row booked despite a strong partner); for `group`: `group_row_ids`, `net` |
| `ActionBankRowDismissed` | `bank_row.dismissed` | bank_import_row | Operator marks a row not-actionable. `reason` is one of the enum values; no free text (DIL-392 + DIL-394 follow-up). Also written by `SupersedeProvisionalBankRows` when a provisional row gives way to its settled row (BRY-378), actor = the importing or syncing user | `reason`; on the automatic path `superseded_by`, `automatic` |
| `ActionBankRowRefreshed` | `bank_row.refreshed` | bank_import_row | A bank import updated a provisional in-flight row in place with the settled text of an incoming row carrying the same bank reference (BRY-378) | `reference`, `bank_import_id`; `old_date`, `new_date` when the settled row is dated later |
| `ActionBankRowRefundPaired` | `bank_row.refund_paired` | bank_import_row (the inbound row) | `HandlePairRefund`: an incoming row and the outgoing refund of it are booked as a pair | `outbound_row_id`, `inbound_journal_id`, `outbound_journal_id` |
| `ActionBankRowUnassigned` | `bank_row.unassigned` | bank_import_row | Operator reverses a prior assignment: voids the journal, removes the payments row, resets `journal_entry_id` (DIL-392). Every row it releases (siblings and a legacy settlement's charges included) leaves the automatic passes (`auto_declined_at`, ACC-13(b), BRY-565) until a person books it; on a Vipps payout deposit ("Angre innbetalingen") a person's booking of a charge in the settlement lifts that again | `prior_invoice_id`, `prior_journal_entry_id`, `sibling_row_ids` (other rows released with it: a group's, a transfer's or a multi-row faktura's), `reopened_invoice_ids` (every faktura whose payment was removed), `released_vipps_row_ids` (the Vipps rows of a legacy settlement released with it) |
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
| `ActionStripeTxnRefundBooked` | `stripe_txn.refund_booked` | stripe_balance_transaction (the refund or refund failure) | The Stripe booking pass booked a card refund or a refund failure (BRY-446), after the commit; the actor is the person who synced, or the system actor for the automatic Stripe run (BRY-570) | `balance_transaction_id`, `category` (`refund` / `refund_failure`), `origin_txn_id` (the charge, or the refund a failure books against), `outcome` (`credit`, `recollect`, `account`, `duplicate`, `mirror`), `amount_ore`, `document_numbers` (credit notes, or the debit fakturas a failure issued) |
| `ActionStripeTxnDisputeBooked` | `stripe_txn.dispute_booked` | stripe_balance_transaction (the dispute or reversal) | The Stripe booking pass booked a card dispute (money withdrawn) or its reversal (the club won) to the dispute account (BRY-446), after the commit; the actor is the pass's acting user (the system actor for the automatic Stripe run, BRY-570) | `balance_transaction_id`, `category` (`dispute` / `dispute_reversal`), `origin_txn_id` (the charge, empty when not stored), `account` (the dispute account code), `amount_ore`, `fee_ore` |
| `ActionStripeTxnDuplicateRefunded` | `stripe_txn.duplicate_refunded` | stripe_balance_transaction (the charge) | The Stripe booking pass booked a duplicate card capture with the refund that returned all of it, through the suspense account (BRY-446) | `refund_txn_id`, `amount_ore` |
| `ActionVippsChargeAutoAssigned` | `vipps_charge.auto_assigned` | vipps_import_row | `AutoAssignVippsCharges` books an unambiguous charge after an import or Synk Vipps (BRY-340), or in the automatic booking run (BRY-567). The actor is the user whose action triggered the run, or in the automatic run the club's system actor. A charge a person undid is not booked again (`declined_skipped` in the run's result) | `rule` (`member_invoice` / `price_item`), `target`, `journal_entry_id`, `trigger` (what ran the rules: `vipps_upload`, `report_sync`, `vipps_resync`, or the automatic run's `boot` / `nightly`; absent on a backlog rebook and on rows written before BRY-567); a charge rebooked by the Vipps backlog (`rebookVippsChargeTx`) also carries `prior_journal_entry_id` and `prior_invoice_id` (the draft it replaced and the faktura it had settled, empty for none) |
| `ActionVippsChargeUnassigned` | `vipps_charge.unassigned` | vipps_import_row | Operator undoes a charge booking (manual or automatic): draft deleted or posted entry voided, settled faktura reopened, charge back in Tildel (BRY-340). Also a release of a 1500-parked backlog draft from the **Bokfør Vipps-utkast** preview (BRY-392). Either way the charge leaves the automatic passes (`auto_declined_at`, ACC-13(b), BRY-565) until a person books it | `journal_entry_id`, `invoice_id`, `released_from_backlog` (true on a backlog release only) |
| `accounting.account_created` | `account` | `HandleCreateAccount` adds a custom account to the kontoplan | `code`, `name` |
| `accounting.account_updated` | `account` | `HandleUpdateAccount` renames an account or changes its description or MVA eligibility. System accounts take a name and description only (BRY-377) | `code`, `name`, `previous_name`, `description`, `previous_description`, `mva_eligible` (the effective value: the stored one when the request omits it) |
| `accounting.vipps_imported` | `vipps_import` | `HandleImportVippsSettlement` after the rows are stored (the Vipps CSV upload) | `filename`, `msn`, `rows_total`, `imported`, `skipped_dup`, `declined_skipped` (charges and deposits the Vipps booking left because a person's undo declined them, ACC-13(b), BRY-565), `booking_deferred` (the Vipps booking slot stayed busy, so nothing was booked) |
| `accounting.vipps_report_synced` | `vipps_import` | `HandleVippsReportSync` pulled rows for a date (not logged for `try_later` or an empty date) | `date`, `ledger_id`, `imported`, `skipped_dup`, `declined_skipped` (as on `accounting.vipps_imported`), `booking_deferred` (the Vipps booking slot stayed busy, so nothing was booked) |
| `accounting.vipps_resynced` | `vipps` | `HandleResyncVipps` completes. Not logged when the booking slot stays busy (409 `vipps_booking_busy`) | `scanned`, `resynced`, `skipped`, `failed`, `declined_skipped` (per-charge model: rows the booking left because a person's undo declined them, BRY-565) |
| `accounting.bank_import_reassigned` | `bank_import` | `HandleReassignBankImport` succeeds (cascades through journal_lines) | `from`, `to`, `cascaded_journals` |
| `accounting.bank_imported` | `bank_import` | `HandleImportBankStatement` after rows ingested | `format`, `rows_total`, `imported`, `skipped_dup`, `matched`, `kid_skipped_settled`, `transfers`, `missing_periods` (years with no fiscal period whose rows were left unbooked, BRY-578), `auto_assigned`, `stripe_booked`, `stripe_payouts_cleared`, `vipps_payouts_cleared` (Vipps payouts the upload's sweep cleared), `declined_skipped` (rows bank auto-assign and the payout sweeps left because a person's undo declined them, ACC-13(b), BRY-565), `booking_deferred` (the Vipps booking slot stayed busy, so the sweep did not run) |

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

## Membership join

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionMembershipJoinRequested` | `membership.join_requested` | membership_application | Once per accepted `POST /join` (not a honeypot hit), after the per-address work; actor NULL, `actor_ip` the visitor. `resource_id` is the application written, empty when none was | `email_hmac` (HMAC-SHA256 of the lowercased address, keyed from `TOTP_ENCRYPTION_KEY` via HKDF; never the address, a plain hash of it, or a name), `outcome`: `new`, `resent` (an unconfirmed application was replaced), `applicant` (an active applicant-only user), `reactivation` (a deactivated former user), `member` (an active user: login link sent, nothing created), `blocked` (the board has set `rejoin_blocked`: contact-board email sent, nothing created), `throttled` (per-recipient budget spent, nothing sent), `capped` (the club-wide daily cap is spent, nothing sent), `failed` (throttle unreadable or a write failed, nothing sent) |
| `ActionMembershipJoinConfirmed` | `membership.join_confirmed` | user | `POST /join/confirm` created or reactivated the applicant, in the confirm's transaction; actor and `resource_id` are the applicant | `application_id`, `order_id` (the pending join order), `reactivated` (bool), `newsletter` (bool) |
| `ActionMembershipGranted` | `membership.granted` | user | Settling a join payment made the applicant a member, in the settlement's transaction; written only by the call that added the `member` role, so a redelivered settlement writes none. Actor NULL; `resource_id` is the member | `source`: `online` (a provider capture of the join order, or of an order paying a join faktura), `kid` (a bank payment linked to the join faktura: KID match or manual assignment), `sweep` (the hourly membership sweep healed it); `order_id` or `invoice_id` (the join source) |
| `ActionMembershipJoinRefunded` | `membership.join_refunded` | user | A join payment was refunded (a succeeded `payment_refunds` row on the join order, or on an order paying a join faktura) or a paid join faktura was credited; written with the board's `membership_join_refunded` delivery, once per join source. Membership is not revoked. Actor NULL; `resource_id` is the member | `ref` (`order:<id>` or `invoice:<id>`) |
| `ActionMembershipCancelled` | `membership.cancelled` | user | `POST /members/me/membership/cancel` set the cancellation, in its transaction, with the receipt and board notice; a repeat writes none. Actor and `resource_id` are the member | `ends_on` (31 December of the year cancelled), `reason_given` (bool; the reason itself is never logged) |
| `ActionMembershipCancelUndone` | `membership.cancel_undone` | user | `POST /members/me/membership/undo` cleared a cancellation before its end date, with the board's undo notice; nothing is written when there was none. Actor and `resource_id` are the member | `ends_on` (the end date that was cleared) |
| `ActionMembershipEnded` | `membership.ended` | user | The daily membership-end job deactivated a member whose `membership_ends_on` has passed and withdrew their waiting-list entries, in the same transaction as the `user.deactivated` row. Actor is the club's system actor; `resource_id` is the member. Not written when the deactivation is refused (last active admin) | `ends_on`, `waiting_list_withdrawn` (count) |
| `ActionMembershipGrantWithheld` | `membership.grant_withheld` | user (or order/invoice) | Real money settled a join source but the integrity check (or the payer's state) withheld the grant; written with the board's `membership_grant_withheld` delivery, once per join source, in the settlement's (or the sweep's) transaction. Actor NULL; `resource_id` is the payer, or, for a source with no user, resource `order`/`invoice` and its id | `ref` (`order:<id>` or `invoice:<id>`), `reason` (the check's reason, e.g. `join line total differs from quantity times unit price`, `total below the join line`, `captured amount below the order total`, `user is deactivated`) |
| `ActionMembershipJoinExpired` | `membership.join_expired` | user | The hourly sweep ended a join left unpaid for 60 days (BRY-631, ACC-14), in the transaction that ended it. Actor NULL; `resource_id` is the applicant | `outcome`: `deactivated` (never billed: order expired, applicant deactivated; the purge erases them next), `credited` (owed join faktura credited in full, applicant deactivated); `order_ids`, and for `credited` `invoice_ids` and `credit_note_numbers` |
| `ActionMembershipJoinExpiredReview` | `membership.join_expired_review` | user | An expired join has part of the money in, so it was neither credited nor deleted; written with the board's `membership_grant_withheld` delivery (ref `join_expired:<source>`), once per join source. Actor NULL; `resource_id` is the applicant | `ref` (`join_expired:invoice:<id>` or `join_expired:order:<id>`), `reason` |

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
