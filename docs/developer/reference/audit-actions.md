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

## Admin user operations

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionUserRoleUpdated` | `user.role_updated` | user | Grant/revoke a role on a user | `role`, `granted` (bool) |
| `ActionUserDeleted` | `user.deleted` | user | Hard-delete via admin action (not the soft GDPR path) | — |
| `ActionUserDeactivated` | `user.deactivated` | user | Admin deactivates a member (reversible; hides + blocks login, keeps data) | `email`, `full_name` |
| `ActionUserReactivated` | `user.reactivated` | user | Admin reactivates a deactivated member | `email`, `full_name` |
| `ActionMemberPIIPurged` | `user.pii_purged` | user | Scheduled sweep erases a deactivated member's PII once retention clears (actor is NULL — system job) | `reason` |
| `ActionUserMailProvisioned` | `user.mail_provisioned` | user | Stalwart mailbox created for the user | `email` |
| `ActionUserMailDeprovisioned` | `user.mail_deprovisioned` | user | Stalwart mailbox removed | `email` |

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
| `ActionBroadcastSent` | `broadcast.sent` | broadcast | Communication broadcast emailed/pushed | `recipient_count` |
| `ActionDocumentUploaded` | `document.uploaded` | document | New doc uploaded to the archive | `filename`, `visibility` |
| `ActionDocumentDeleted` | `document.deleted` | document | Doc removed | `filename` |

## Finance

| Constant | String | Resource | When | `extra` |
|---|---|---|---|---|
| `ActionInvoiceCreated` | `invoice.created` | invoice | After `INSERT invoices` succeeds | `invoice_number`, `total_amount` |
| `ActionInvoicePDFGenerated` | `invoice.pdf_generated` | invoice | `finance.GeneratePDF` succeeded and bytes stored | `invoice_number` |
| `ActionInvoiceEmailed` | `invoice.emailed` | invoice | After `SendWithAttachment` succeeds (post-`sent_at` stamp) | `email`, `invoice_number` |
| `ActionInvoiceReminded` | `invoice.reminded` | invoice | Per row inside `HandleBulkSendReminder` after reminder email sent | `email`, `invoice_number` |
| `ActionInvoiceVippsLinkSet` | `invoice.vipps_link_set` | invoice | `HandleSetInvoiceVippsLink` (per invoice), and the reminder batch's On/Off override (resource_id empty, `count`) | `opt_in` (and `count` for the batch) |
| `ActionInvoiceCreditLinked` | `invoice.credit_linked` | invoice (the credit note) | `HandleLinkCreditNote`: a credit note is settled against the faktura it credits | `credit_note_number`, `invoice_number`, `amount`, `outcome` (`settled_pair` / `credit_owed`), `manual_pick` (chosen in the manual search, as the client reports it), `default_candidate` (server-derived: same payer and amount, so the plain candidate list would have offered it), `member_mismatch` (the two bill different payers), `confirm_other_member` (the treasurer confirmed a cross-payer link), `credit_note_has_payer` |
| `ActionInvoiceVoided` | `invoice.voided` | invoice | `HandleVoidInvoice`, after the guarded status update and the GL reversal of the faktura's whole entry family | `invoice_number`, `reversal_entry_ids` (the reversal entries posted; empty when nothing was booked), `gl_reversal_error` when the reversal failed |
| `ActionInvoiceCreditUnlinked` | `invoice.credit_unlinked` | invoice (the credit note) | `HandleUnlinkCreditNote`: the settlement is undone and the faktura reopens | `credit_note_number`, `invoice_id` |
| `ActionInvoiceRegenerated` | `invoice.pdf_regenerated` | invoice | Per row inside `HandleBulkRegeneratePDF` after archive+overwrite succeeds | `invoice_number`, `bank_account`, `prior_pdf_bytes` |
| `ActionInvoiceDeliveryLogViewed` | `invoice.delivery_log_viewed` | invoice | Treasurer opens the Stalwart delivery-log popover for a sent invoice (PII visibility audit — recipient email is exposed in the response) | `recipient_email`, `matches_returned` |
| `ActionFinanceCSVExported` | `finance.csv_exported` | finance | Treasurer downloads a payments/invoices CSV | `format`, `rows` |
| `ActionAccountingInvoicesResplit` | `accounting.invoices_resplit` | fiscal_period | Treasurer applies **Korriger inntektsfordeling** (resplit). Not logged for the preview | `posted`, `account_totals`, `skipped` |
| `ActionAccountingVippsDraftsPosted` | `accounting.vipps_drafts_posted` | fiscal_period | Treasurer applies **Bokfør Vipps-utkast** for a period (one row per batch; not logged for the preview). Each reclassified charge also writes its own `vipps_charge.auto_assigned` | `year`, `drafts`, `posted`, `reclassified`, `refused`, `posted_totals` |
| `ActionAccountingDraftDeleted` | `accounting.draft_deleted` | journal_entry | An unlinked draft bilag deleted from the Bilag page (`DeleteDraftJournalEntry`) | — |
| `ActionDeveloperQuery` | `developer.db_query` | developer_query | A read-only SQL query runs through `/admin/dev/query` (admin session) or `/api/v1/dev/query` (developer bearer token, BRY-115). The full SQL is logged: the `brygge_dev_ro` role reads only masked, non-PII data, so the text can't expose identity | `sql` (capped at 8 KB), `sql_sha256`, `columns`, `row_count`, `elapsed_ms`, `truncated`, `token_id` (token path only), `error` (if any) |
| `ActionDeveloperTokenCreated` | `developer.token_created` | developer_token | Admin mints a developer bearer token (BRY-115). The plaintext is never logged | `name`, `prefix`, `scope`, `expires_at` |
| `ActionDeveloperTokenRevoked` | `developer.token_revoked` | developer_token | Admin revokes a developer bearer token | — |
| `ActionPaymentCreated` | `payment.created` | payment | Direct payment row insert (not invoice-paid back-link, which is internal) | `type`, `amount` |
| `ActionPaymentViewed` | `finance.payment_viewed` | payment | Admin opens a payment detail (PII visibility audit) | — |

### Top-level accounting events (resource = `bank`/`vipps`/`bank_import`)

These don't have constants in `audit.go` — they're inlined as strings because each fires once per operator-triggered batch. Consider promoting to constants if more handlers add the same string.

| String | Resource | When | `extra` |
|---|---|---|---|
| `accounting.bank_synced` | `bank` | `HandleBankSync` completes | `kid_matched`, `kid_skipped_settled` (KID-cascade rows left in Tildel because their faktura was already settled), `vipps_reconciled`, `transfers_linked`, `payments_linked`, `closed_periods`, `auto_assigned` (deposits booked by bank auto-assign) |
| `ActionBankRowAssignedInvoice` | `bank_row.assigned_invoice` | bank_import_row | Operator assigns a row to an open invoice via the Tildel tab (DIL-392). Creates the GL journal + `linkInvoicePayment` | `invoice_id`, `journal_entry_id` |
| `ActionBankRowAutoAssigned` | `bank_row.auto_assigned` | bank_import_row | `AutoAssignBankDeposits` books an unambiguous KID-less deposit to a faktura after a bank import or **Synk bank** (BRY-366). The actor is the user who ran the import or sync; with none, nothing is booked. Undone with `bank_row.unassigned` | `rule` (`member_invoice`), `target` ("faktura N"), `invoice_id`, `journal_entry_id`, `match_pass` (`alias` / `full_name` / `first_last`), `trigger` (`bank_import` / `bank_sync`) |
| `ActionBankRowAssignedAccount` | `bank_row.assigned_account` | bank_import_row | Operator assigns a row to a GL account (DIL-392). `kind` = `expense` (outgoing), `revenue` (incoming, not a faktura) or `group` (one row of rows booked together as one bilag, BRY-380; one audit row per row) | `account_code`, `kind`, `journal_entry_id`, `confirm_alone` (single row booked despite a strong partner); for `group`: `group_row_ids`, `net` |
| `ActionBankRowDismissed` | `bank_row.dismissed` | bank_import_row | Operator marks a row not-actionable. `reason` is one of the enum values; no free text (DIL-392 + DIL-394 follow-up). Also written by `SupersedeProvisionalBankRows` when a provisional row gives way to its settled row (BRY-378), actor = the importing or syncing user | `reason`; on the automatic path `superseded_by`, `automatic` |
| `ActionBankRowRefreshed` | `bank_row.refreshed` | bank_import_row | A bank import updated a provisional in-flight row in place with the settled text of an incoming row carrying the same bank reference (BRY-378) | `reference`, `bank_import_id`; `old_date`, `new_date` when the settled row is dated later |
| `ActionBankRowUnassigned` | `bank_row.unassigned` | bank_import_row | Operator reverses a prior assignment: voids the journal, removes the payments row, resets `journal_entry_id` (DIL-392) | `prior_invoice_id`, `prior_journal_entry_id`, `sibling_row_ids` (other rows released with it: a group's, a transfer's or a multi-row faktura's) |
| `ActionPayerAliasTaught` | `payer_alias.taught` | vipps_customer_alias | A payer name is saved as a member's alias (BRY-383): from Tildel when a treasurer assigns a bank deposit to a faktura with **Hugs betalaren** ticked (in the faktura-lock transaction, so it fires only when the booking succeeded), or from the Vipps queue's **Alias**. The payer name itself is not logged | `user_id` (the member), `source` (`bank_tildel` / `vipps_queue`), `override` (the name was another member's own name or alias and the treasurer confirmed taking it over; always `false` from the Vipps queue) |
| `ActionPayerAliasRemoved` | `payer_alias.removed` | vipps_customer_alias | A treasurer deletes an alias from the Vipps queue's **Lærte alias** list (BRY-383) | `user_id` (the member it pointed at), `source` (`vipps_queue`) |
| `ActionVippsChargeAutoAssigned` | `vipps_charge.auto_assigned` | vipps_import_row | `AutoAssignVippsCharges` books an unambiguous charge after an import or Synk Vipps (BRY-340). The actor is the user whose action triggered the run | `rule` (`member_invoice` / `price_item`), `target`, `journal_entry_id` |
| `ActionVippsChargeUnassigned` | `vipps_charge.unassigned` | vipps_import_row | Operator undoes a charge booking (manual or automatic): draft deleted or posted entry voided, settled faktura reopened, charge back in Tildel (BRY-340) | `journal_entry_id`, `invoice_id` |
| `accounting.account_created` | `account` | `HandleCreateAccount` adds a custom account to the kontoplan | `code`, `name` |
| `accounting.account_updated` | `account` | `HandleUpdateAccount` renames an account or changes its description or MVA eligibility. System accounts take a name and description only (BRY-377) | `code`, `name`, `previous_name`, `description`, `previous_description`, `mva_eligible` (the effective value: the stored one when the request omits it) |
| `accounting.vipps_resynced` | `vipps` | `HandleResyncVipps` completes | `scanned`, `resynced`, `skipped`, `failed` |
| `accounting.bank_import_reassigned` | `bank_import` | `HandleReassignBankImport` succeeds (cascades through journal_lines) | `from`, `to`, `cascaded_journals` |
| `accounting.bank_imported` | `bank_import` | `HandleImportBankStatement` after rows ingested | `format`, `rows_total`, `imported`, `skipped_dup`, `matched`, `kid_skipped_settled`, `transfers`, `auto_assigned` |

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
| `ActionGDPRDeletionProcessed` | `gdpr.deletion_processed` | user | Cooling-off elapsed; non-retained data wiped, identity erased or deferred behind retention | `records_wiped`, `identity_erased` (bool), `retention_clears_at` |
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
