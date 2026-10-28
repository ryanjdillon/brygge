# Developer DB query — read-only, PII-masked access

The `/admin/dev/query` endpoint, the `just prod-query` recipe, and
developer-token access let an operator — or an AI agent acting for one —
run read-only SQL against the
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

## Three ways in, one boundary

| Path | Auth | Use when |
|------|------|----------|
| Admin UI → Database query (`POST /admin/dev/query`) | admin session + fresh TOTP | An operator investigating by hand |
| `just prod-query "<sql>"` | SSH key → `sudo -u brygge psql` as the role over the socket | Operator shell with a working SSH key |
| `just prod-query-http "<sql>"` (`POST /api/v1/dev/query`) | developer bearer token | An external tool or agent that cannot use the SSH key (BRY-115) |

All three run as the same `brygge_dev_ro` role, so they see the same masked data.

### Developer bearer tokens

- **Minted per use** by an admin under Admin → Developer tokens (fresh TOTP at
  click time). The plaintext (`bry_dev_…`) is shown **once**; only its
  `sha256` is stored, in `developer_tokens`. That table is not on the role's
  allowlist, so a query session can't read or enumerate tokens.
- **Short-lived:** 24 h by default, 7 days maximum. Revocation is immediate.
  Tokens are purged 7 days after expiry or revocation.
- **Scoped, not privileged:** the token authenticates only
  `POST /api/v1/dev/query`. The middleware injects the creator and club with
  **no roles**, so a token can't reach any session-authenticated route.
- **Opt-in:** the route is only mounted when `DEV_QUERY_TOKENS_ENABLED=true`.
- **Throttled and audited:** a per-IP limit blunts token guessing, and a
  per-token 60/min limit throttles a leaked token. Every query's full SQL,
  columns and `token_id` land in `audit_log` (`developer.db_query`). The text
  is safe to log because the role can't read raw PII.
- **Exposure:** the route is reachable over HTTPS, not loopback-only. This is
  deliberate: the motivating case is an agent that can't use SSH, and the
  blast radius of a leaked token is bounded to masked, non-PII, read-only
  data for at most its TTL.

On the agent side, keep the token out of the repo and out of agent settings.
Export it as `BRYGGE_DEV_TOKEN`, or store it with `chmod 600` in
`~/.config/brygge/dev-token`, which is where `just prod-query-http` reads it
from. The recipe passes the header through a process substitution, so the
token never appears in `ps`.

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
| `payment_webhook_events` | payload (raw JSONB; may carry payer name/email) | — |
| `payment_refunds` | reason (staff-typed) | `reason_length` |
| `slip_shares` | notes | — |
| `boats` | name, registration_number, mmsi, call_sign | — |
| `journal_entries` | description (bank-import entries copy the bank text, payer name included), attachment_url | `description_kid`, `description_length`, `has_attachment` |
| `journal_lines` | description | — |
| `payments` | description | — |
| `invoice_lines` | description, sub_description | — |
| `account_mapping_rules` | name, match_value (rules can match on a payer's name) | — |
| `vipps_message_rules` | pattern (same) | — |
| `slips`, `dock_fingers` | notes | — |

Org fields (`*_org_name/number/address`), the club's own `bank_account`/`org_number`/`address` (printed on fakturas), and `customer_phone_masked` (masked at import) pass through.

### Allowlist — readable raw (verified no PII)

`accounts`, `bank_imports`, `docks`, `fiscal_periods`,
`mva_compensation_reports`, `order_lines`, `order_sources`, `payment_attempts`,
`price_exemptions`, `price_items`, `product_variants`, `products`,
`slip_assignments`, `vipps_imports`, `waiting_list_entries`.

`payment_attempts` (000115) holds only ids, the provider name, the opaque
provider reference, status, amount and currency.

**Rule: a table is allowlisted raw only if none of its free-text columns can
name a person.** "Structural" isn't enough: bank-import journal entries copy
the bank text with the payer's name, and rules or notes are typed by people
(BRY-336). Every text/JSON column the role can read raw is listed and reviewed
in `rawTextColumns` in `dev_masked_test.go`; a new one fails the test until it
is reviewed or its table moves to a masked view.

### Denied — not granted (default)

Everything else, notably: `audit_log` (the role's own evidence),
`developer_tokens` (when it lands), `club_settings` (KV `value` may hold
secrets), `invoice_pdf_archive`, and all credential/consent tables
(`sessions`, `refresh_tokens`, `revoked_tokens`, `magic_links`,
`totp_recovery_codes`, `webauthn_credentials`, `user_mail_credentials`,
`user_consents`, `deletion_requests`, `push_subscriptions`,
`notification_preferences`, `vipps_customer_aliases`).

## Matching people without names: keyed name tokens

Names stay NULL, but reconciliation often has to ask "is the payer on this bank row the member who owns that invoice?". For that, five views expose **keyed name tokens**:

| View | Key columns | Tokens |
|------|-------------|--------|
| `user_name_tokens` | `user_id` | `name_pair_toks`, `household_tok` (address + postcode) |
| `invoice_name_tokens` | `invoice_id`, `user_id`, `recipient_id` | `member_pair_toks` (issue-time snapshot), `recipient_contact_pair_toks` |
| `bank_row_payer_tokens` | `bank_import_row_id` | `payer_pair_toks` ("Fra: … Betalt:" payer, else counterpart; never the club's own name) |
| `vipps_row_customer_tokens` | `vipps_import_row_id` | `customer_pair_toks` |
| `billing_recipient_tokens` | `recipient_id` | `contact_pair_toks` |

A name is normalised the same way `accounting.normalizeName` does it: lowercase, diacritics folded, "." and "," dropped, anything else non-letter becomes a space, one-letter initials dropped, first four distinct words kept. It then becomes the set of `HMAC(key, "wordA wordB")` over its unordered word pairs, truncated to 12 hex characters. Match with array overlap, which is independent of word order, middle names and double surnames:

```sql
SELECT b.bank_import_row_id, u.user_id
FROM bank_row_payer_tokens b
JOIN user_name_tokens u ON b.payer_pair_toks && u.name_pair_toks;
```

Spouses and joint accounts link through `household_tok`. A shared surname alone deliberately doesn't link two people, because single-word tokens would leak through frequency analysis.

**Why this can't be reversed**
- The key lives in `dev_private.token_key` (migration `000098`). The role has neither USAGE on that schema nor SELECT on the table.
- The views read the key with their owner's rights, through a scalar subquery. There is **no function** that reaches the key. EXECUTE inside a view is checked against the caller, so such a function would either fail for the role or let it tokenise any name it guessed. An IMMUTABLE accessor would also get folded into `EXPLAIN VERBOSE` output; the endpoint refuses `EXPLAIN VERBOSE` anyway.
- Only names the agent can't write are tokenised. **`orders.guest_name` is excluded**: the public QR pay endpoint stores any caller-supplied name before payment, which would make it a chosen-plaintext oracle.
- The key is generated server-side and is stable across deploys (the repeatable views file doesn't touch it). Rotate it with `just rotate-name-token-key`, after which every token changes.

Tests in `dev_masked_test.go` ("keyed name tokens") cover:
- the key being unreachable;
- no executable function touching `dev_private`;
- no key bytes in `EXPLAIN` output;
- cross-source matches;
- tokens staying stable when the views are re-applied, and changing when the key is rotated.

## Adding a table or column

- **New table:** denied by default. If the agent needs it, add it to the
  allowlist in `000095`'s pattern (a new migration) when it holds no PII, or add
  a `dev_masked` view when it does, and record it above.
- **New column on a masked table:** the parity test fails until you classify it
  in `dev_masked.sql` — keep it, `NULL`-mask it, or expose a derived hint — and
  update the table above. See the
  [add-migration checklist](../checklists/add-migration.md).
