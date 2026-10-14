# Developer DB query — read-only, PII-masked access

The `/admin/dev/query` endpoint (and the `just prod-query` recipe) let an
operator — or an AI agent acting for one — run read-only SQL against the
database to troubleshoot data problems (e.g. a Vipps payment that didn't
reconcile to its faktura) **without ever seeing member PII**.

This page is the **source of truth** for what the read-only role may read. It
is not derived from `accounting/purge.go` (which only lists what the GDPR purge
*rewrites*, a narrower set than what is *sensitive*).

## Access model

- **A real privilege boundary.** Queries run as the `brygge_dev_ro` Postgres
  role on a *dedicated connection pool* (`DEV_QUERY_DATABASE_URL`) that
  authenticates **as** that role — never via `SET ROLE` on the `brygge`
  connection, which is reversible (see the invariant
  [Query role is a real privilege boundary](../reference/invariants.md#query-role-is-a-real-privilege-boundary-not-set-role)).
  `brygge_dev_ro` is a `LOGIN` role, is **not** a member of `brygge`, and has
  `default_transaction_read_only`, `statement_timeout`, and
  `search_path = dev_masked, public` pinned via `ALTER ROLE`.
- **Default-deny.** The role has no blanket SELECT. It reads only the
  `dev_masked.*` views and the allowlist below. A newly-added table is invisible
  until it is classified here and granted — the drift guard.
- **Masking, not partial-masking.** Identity, credential and free-text columns
  are `NULL` in the masked views; a partial value (e.g. `ol***@…`) plus the
  opaque `user_id` is a reversible membership oracle in a ~100-member club.
  Correlation keys (`user_id`, `kid_number`, `vipps_reference`, `psp_ref`,
  amounts, statuses, timestamps) pass through verbatim.
- **Reconciliation-friendly.** Free text the reconciliation cascade matches on
  (`bank_import_rows.description`, `vipps_import_rows.message`) is exposed as a
  derived digits-only KID hint rather than raw.

The view definitions live in
[`../../../backend/migrations/repeatable/dev_masked.sql`](../../../backend/migrations/repeatable/dev_masked.sql)
(a repeatable migration, rebuilt from the current schema after every
`migrate up`); the grants live in migration `000095_dev_ro_default_deny`. A
column-parity integration test (`backend/internal/handlers/dev_masked_test.go`)
fails if a base column of a masked table isn't accounted for in its view.

## Classification

### Masked — read via the `dev_masked.*` view only

Unqualified names (`users`) resolve to the view through the role's search_path;
`public.users` is denied.

| Table | NULL-masked (identity / credential / free text / blob) | Derived hint |
|-------|--------------------------------------------------------|--------------|
| `users` | email, phone, address_line, postal_code, city, first_name, last_name, full_name, admin_notes, totp_secret_encrypted, webauthn_handle, payables_token | `has_email` |
| `clubs` | config, faktura_logo_data, site_logo_data, all `*_email` (board), phone, feedback_linear_* + anthropic_api_key (secrets) | — |
| `billing_recipients` | contact_person, email | `has_email` |
| `orders` | guest_email, guest_name, token (pay-link capability) | `has_guest_email` |
| `invoices` | pdf_data, recipient_contact_person, recipient_email, member_name, member_email, vipps_payment_url (embeds token) | `has_pdf` |
| `vipps_import_rows` | customer_name, message | `message_kid`, `message_length` |
| `bank_import_rows` | description, counterpart, from_account, to_account | `description_kid`, `description_length` |
| `vipps_webhook_events` | payload (raw JSONB) | `payload_psp_ref`, `payload_state`, `payload_amount` |
| `slip_shares` | notes | — |
| `boats` | name, registration_number, mmsi, call_sign | — |

Org fields (`*_org_name/number/address`), the club's own `bank_account`/`org_number`/`address` (printed on fakturas), and `customer_phone_masked` (masked at import) pass through.

### Allowlist — readable raw (verified no PII)

`accounts`, `account_mapping_rules`, `bank_imports`, `dock_fingers`, `docks`,
`fiscal_periods`, `invoice_lines`, `journal_entries`, `journal_lines`,
`mva_compensation_reports`, `order_lines`, `order_sources`, `payments`,
`price_items`, `product_variants`, `products`, `slip_assignments`, `slips`,
`vipps_imports`, `vipps_message_rules`, `waiting_list_entries`.

### Denied — not granted (default)

Everything else, notably: `audit_log` (the role's own evidence),
`developer_tokens` (when it lands), `club_settings` (KV `value` may hold
secrets), `invoice_pdf_archive`, and all credential/consent tables
(`sessions`, `refresh_tokens`, `revoked_tokens`, `magic_links`,
`totp_recovery_codes`, `webauthn_credentials`, `user_mail_credentials`,
`user_consents`, `deletion_requests`, `push_subscriptions`,
`notification_preferences`, `vipps_customer_aliases`).

## Adding a table or column

- **New table:** denied by default. If the agent needs it, add it to the
  allowlist in `000095`'s pattern (a new migration) when it holds no PII, or add
  a `dev_masked` view when it does, and record it above.
- **New column on a masked table:** the parity test fails until you classify it
  in `dev_masked.sql` — keep it, `NULL`-mask it, or expose a derived hint — and
  update the table above. See the
  [add-migration checklist](../checklists/add-migration.md).
