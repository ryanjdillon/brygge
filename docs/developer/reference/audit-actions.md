# Audit actions reference

Every `audit.Action*` constant defined in `backend/internal/audit/audit.go`, with the action string, the resource type the row binds to, when it fires, and the notable `extra` fields.

When adding a new action, follow [`checklists/add-audit-action.md`](checklists/add-audit-action.md) and add the row here in the same commit.

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
| `ActionPricingUpdated` | `pricing.updated` | price_item | Treasurer edits the price catalog | `price_item_id`, `field` |
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
| `ActionInvoiceRegenerated` | `invoice.pdf_regenerated` | invoice | Per row inside `HandleBulkRegeneratePDF` after archive+overwrite succeeds | `invoice_number`, `bank_account`, `prior_pdf_bytes` |
| `ActionInvoiceDeliveryLogViewed` | `invoice.delivery_log_viewed` | invoice | Treasurer opens the Stalwart delivery-log popover for a sent invoice (PII visibility audit — recipient email is exposed in the response) | `recipient_email`, `matches_returned` |
| `ActionFinanceCSVExported` | `finance.csv_exported` | finance | Treasurer downloads a payments/invoices CSV | `format`, `rows` |
| `ActionPaymentCreated` | `payment.created` | payment | Direct payment row insert (not invoice-paid back-link, which is internal) | `type`, `amount` |
| `ActionPaymentViewed` | `finance.payment_viewed` | payment | Admin opens a payment detail (PII visibility audit) | — |

### Top-level accounting events (resource = `bank`/`vipps`/`bank_import`)

These don't have constants in `audit.go` — they're inlined as strings because each fires once per operator-triggered batch. Consider promoting to constants if more handlers add the same string.

| String | Resource | When | `extra` |
|---|---|---|---|
| `accounting.bank_synced` | `bank` | `HandleBankSync` completes | `kid_matched`, `vipps_reconciled`, `transfers_linked`, `closed_periods` |
| `accounting.vipps_resynced` | `vipps` | `HandleResyncVipps` completes | `scanned`, `resynced`, `skipped`, `failed` |
| `accounting.bank_import_reassigned` | `bank_import` | `HandleReassignBankImport` succeeds (cascades through journal_lines) | `from`, `to`, `cascaded_journals` |
| `accounting.bank_imported` | `bank_import` | `HandleImportBankStatement` after rows ingested | `format`, `rows_total`, `imported`, `skipped_dup`, `matched`, `transfers` |

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
| `ActionGDPRDeletionProcessed` | `gdpr.deletion_processed` | user | Cooling-off elapsed; data wipe ran | `records_wiped` |
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

Per [`checklists/add-audit-action.md`](checklists/add-audit-action.md):

1. Add constant to `internal/audit/audit.go` (alphabetical within its block — Auth, TOTP, User, Slip, Content, Finance, etc.)
2. Use from the handler behind `if h.audit != nil`
3. Add a row to this doc with: constant, string, resource, when, `extra` fields
4. Action string: `<resource>.<verb>` past tense

## Action string permanence

**Action strings, once shipped, are permanent.** Any operator script or compliance report that filters on `action = 'invoice.emailed'` breaks if the string changes. Renaming is effectively impossible without a migration that rewrites historical rows.

If you must rename, write the migration: `UPDATE audit_log SET action = 'new.name' WHERE action = 'old.name'`. The Go constant can change freely — only the string is the public API.
