-- dev_masked: PII-masking views for the /admin/dev/query read-only role
-- (BRY-287). REPEATABLE migration — NOT a numbered golang-migrate file.
--
-- Why repeatable, not numbered: a view freezes the column list of every
-- table it references, so a later `ALTER TABLE ... ALTER COLUMN TYPE` /
-- `DROP COLUMN` on a referenced column would fail while the view exists.
-- Keeping the views in one idempotent drop-and-recreate file, applied
-- AFTER golang-migrate, sidesteps that: the views are rebuilt from the
-- current schema on every run. Applied by brygge-migrate.service
-- (ExecStartPost), `just migrate`, `just migrate-check`, and the
-- dev_masked integration test.
--
-- Rule (see docs/developer/reference/invariants.md): direct identifiers,
-- credentials and free text are NULL — never partial-masked, since a
-- partial value plus the opaque id is a reversible membership oracle in
-- a small club. Correlation keys (ids, kid_number, vipps_reference,
-- psp_ref, amounts, statuses, timestamps) pass through verbatim, and
-- free text that the reconciliation cascade matches on is exposed as a
-- derived digits-only KID hint rather than raw.
--
-- The caller role reads these through search_path = dev_masked, public,
-- so unqualified `users` resolves here while `public.users` is denied
-- (BRY-286). Views are owned by brygge; with the default
-- security_invoker = off they read the base tables with the owner's
-- rights, so the caller never needs base-table SELECT.

DROP SCHEMA IF EXISTS dev_masked CASCADE;
CREATE SCHEMA dev_masked;

CREATE VIEW dev_masked.users AS
SELECT
    id, club_id,
    NULL::text  AS email,
    NULL::text  AS phone,
    NULL::text  AS address_line,
    NULL::text  AS postal_code,
    NULL::text  AS city,
    is_local, local_override_by, created_at, updated_at,
    NULL::bytea AS totp_secret_encrypted,
    totp_enabled,
    NULL::text  AS first_name,
    NULL::text  AS last_name,
    NULL::text  AS full_name,
    NULL::text  AS admin_notes,
    hide_in_directory, preferred_language, deactivated_at, deactivated_by,
    NULL::bytea AS webauthn_handle,
    NULL::text  AS payables_token,
    (email IS NOT NULL) AS has_email
FROM public.users;

CREATE VIEW dev_masked.billing_recipients AS
SELECT
    id, club_id, kind,
    org_name, org_number, org_address,   -- organisation data, not personal
    NULL::text AS contact_person,
    NULL::text AS email,
    pii_purged_at, created_at,
    (email IS NOT NULL) AS has_email
FROM public.billing_recipients;

CREATE VIEW dev_masked.orders AS
SELECT
    id, club_id, user_id,
    NULL::text AS guest_email,
    NULL::text AS guest_name,
    status, total_amount, currency, vipps_reference, paid_at,
    created_at, updated_at,
    NULL::text AS token,                  -- pay-link capability secret
    provider,
    (guest_email IS NOT NULL) AS has_guest_email
FROM public.orders;

CREATE VIEW dev_masked.invoices AS
SELECT
    id, club_id, payment_id, user_id, invoice_number, kid_number,
    issue_date, due_date, total_amount,
    NULL::bytea AS pdf_data,
    sent_at, created_at, price_item_id, fiscal_period_id, status, category,
    recipient_kind, recipient_org_name, recipient_org_number,
    recipient_org_address,                -- organisation address, not personal
    NULL::text AS recipient_contact_person,
    recipient_their_ref,
    NULL::text AS recipient_email,
    import_source, external_id, s3_key,
    NULL::text AS member_name,
    NULL::text AS member_email,
    recipient_id, vipps_payment_reference,
    NULL::text AS vipps_payment_url,      -- embeds the order pay-link token
    vipps_payment_expires_at, vipps_paid_at, vipps_payment_link_opt_in,
    (pdf_data IS NOT NULL) AS has_pdf
FROM public.invoices;

CREATE VIEW dev_masked.vipps_import_rows AS
SELECT
    id, vipps_import_id, club_id, row_hash, row_type, tx_at, booking_date,
    amount, fee, net_amount,
    NULL::text AS customer_name,
    customer_phone_masked,                -- already masked at import
    NULL::text AS message,
    psp_ref, order_id, settlement_number, payout_account,
    scheduled_payout_date, msn, journal_entry_id, created_at,
    dismissed_at, dismissed_reason, dismissed_by,
    substring(message from '[0-9]{6,}') AS message_kid,
    length(message)                      AS message_length
FROM public.vipps_import_rows;

CREATE VIEW dev_masked.bank_import_rows AS
SELECT
    id, bank_import_id, row_date,
    NULL::text AS description,
    amount, balance, reference, kid_number,
    NULL::text AS counterpart,            -- counterparty bank account no.
    journal_entry_id, auto_matched, created_at, club_id, row_hash,
    NULL::text AS from_account,
    NULL::text AS to_account,
    dismissed_at, dismissed_by, dismissed_reason, invoice_id,
    refund_paired_with,
    substring(description from '[0-9]{6,}') AS description_kid,
    length(description)                     AS description_length
FROM public.bank_import_rows;

CREATE VIEW dev_masked.vipps_webhook_events AS
SELECT
    id, club_id, event_id, event_name, reference,
    NULL::jsonb AS payload,
    received_at,
    payload->>'pspReference'       AS payload_psp_ref,
    payload->>'state'              AS payload_state,
    payload->'amount'->>'value'    AS payload_amount
FROM public.vipps_webhook_events;

CREATE VIEW dev_masked.slip_shares AS
SELECT
    id, slip_assignment_id, club_id, available_from, available_to,
    NULL::text AS notes,                  -- member-entered free text
    status, created_at, updated_at
FROM public.slip_shares;

CREATE VIEW dev_masked.boats AS
SELECT
    id, user_id, club_id,
    NULL::text AS name,                   -- often personal ("Mette's Marit")
    type, length_m, beam_m, draft_m,
    NULL::text AS registration_number,    -- indirect identifier (public reg)
    boat_model_id, manufacturer, model, weight_kg,
    measurements_confirmed, confirmed_by, confirmed_at, created_at, updated_at,
    NULL::text AS mmsi,                   -- indirect identifier
    NULL::text AS call_sign               -- indirect identifier
FROM public.boats;

-- Grant the read-only role access to the masked schema. Guarded so the
-- file still applies on a DB where the role has not been bootstrapped
-- (fresh migrate-check / test DB) — the dev-query feature simply won't
-- work there until the role exists.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT USAGE ON SCHEMA dev_masked TO brygge_dev_ro';
        EXECUTE 'GRANT SELECT ON ALL TABLES IN SCHEMA dev_masked TO brygge_dev_ro';
    END IF;
END
$$;
