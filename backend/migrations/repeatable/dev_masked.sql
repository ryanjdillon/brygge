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

CREATE VIEW dev_masked.clubs AS
SELECT
    id, slug, name, description, latitude, longitude,
    municipality_codes, postal_codes,
    NULL::jsonb AS config,               -- free-form; may hold anything
    required_volunteer_hours, created_at, updated_at,
    org_number, address, bank_account,   -- club's own, printed on fakturas
    NULL::bytea AS faktura_logo_data,
    faktura_logo_mime, website_url,
    NULL::text AS chairman_email,         -- board PII
    NULL::text AS treasurer_email,
    NULL::text AS secretary_email,
    NULL::text AS harbor_master_email,
    NULL::text AS phone,
    vhf_channel, harbor_approach, harbor_depth, harbor_vhf,
    harbor_cta_title, harbor_cta_description,
    motorhome_power, motorhome_facilities, motorhome_checkin,
    motorhome_rules, motorhome_cta_title, motorhome_cta_description,
    NULL::bytea AS site_logo_data,
    site_logo_mime,
    NULL::text AS vice_chairman_email,    -- board PII
    default_language,
    feature_bookings, feature_projects, feature_calendar, feature_commerce,
    feature_accounting, feature_feedback,
    NULL::text AS feedback_linear_api_key,    -- secret
    NULL::text AS feedback_linear_team_id,    -- secret
    NULL::text AS feedback_linear_triage_id,  -- secret
    NULL::text AS anthropic_api_key,          -- secret
    kid_check_method, vipps_clearing_account, vipps_fee_account,
    vipps_per_charge_enabled, vipps_max_amount,
    vipps_auto_assign, vipps_auto_assign_tie_category, bank_auto_assign,
    exemption_expense_account,
    online_max_amount, payment_fee_account, stripe_clearing_account,
    stripe_payout_descriptor, stripe_suspense_account, stripe_dispute_account
FROM public.clubs;

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
    credits_invoice_id,
    online_paid_at, online_paid_provider, online_payment_opt_in,
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
    invoice_id, auto_assign_rule,
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
    refund_paired_with, reseen_at,
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

CREATE VIEW dev_masked.payment_webhook_events AS
SELECT
    id, club_id, provider, event_id, event_name, reference,
    NULL::jsonb AS payload,               -- may carry payer name/email
    received_at
FROM public.payment_webhook_events;

CREATE VIEW dev_masked.payment_refunds AS
SELECT
    id, club_id, attempt_id, provider, amount, currency, provider_refund_id,
    status,
    NULL::text AS reason,                 -- staff-typed free text
    created_by, journal_entry_id, created_at, updated_at,
    provider_payment_ref, charge_ref,
    failure_reason,                       -- the provider's reason code
    request_key, outcome,
    length(reason) AS reason_length
FROM public.payment_refunds;

CREATE VIEW dev_masked.stripe_balance_transactions AS
SELECT
    id, club_id, balance_transaction_id, reporting_category, stripe_type,
    amount_ore, fee_ore, net_ore, fee_details, currency, created_at, available_on,
    source_id, charge_id, payment_intent_id,
    attempt_id, order_id, payout_id, journal_entry_id, invoice_id,
    status, review_reason, dismissed_at, dismissed_by,
    NULL::text AS dismissed_reason,       -- staff-typed free text
    inserted_at, updated_at,
    origin_txn_id, refunded_ore, recollected_at, refund_request_key,
    length(dismissed_reason) AS dismissed_reason_length
FROM public.stripe_balance_transactions;

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

-- Accounting / structural tables. Their numbers and keys are safe, but
-- their free-text columns are not: bank-import journal entries copy the
-- bank text verbatim ("Innbetaling KID … Fra: <payer> …"), and rules,
-- notes and line descriptions are typed by people who may name members.
-- So free text is NULLed here too; only tables with no free-text columns
-- are allowlisted raw (migration 000097).

CREATE VIEW dev_masked.journal_entries AS
SELECT
    id, club_id, fiscal_period_id, entry_number, entry_date,
    NULL::text AS description,
    status, source, source_id, source_table,
    NULL::text AS attachment_url,
    created_by, posted_by, posted_at, voided_by, voided_at,
    created_at, updated_at,
    substring(description from '[0-9]{6,}') AS description_kid,
    length(description)                     AS description_length,
    (attachment_url IS NOT NULL)            AS has_attachment
FROM public.journal_entries;

CREATE VIEW dev_masked.journal_lines AS
SELECT
    id, journal_entry_id, account_id, debit, credit,
    NULL::text AS description,
    mva_amount, mva_eligible
FROM public.journal_lines;

CREATE VIEW dev_masked.payments AS
SELECT
    id, club_id, user_id, type, amount, currency, vipps_reference, status,
    due_date,
    NULL::text AS description,
    paid_at, created_at, recipient_id
FROM public.payments;

CREATE VIEW dev_masked.invoice_lines AS
SELECT
    id, invoice_id,
    NULL::text AS description,
    quantity, unit_price, line_total, account_id,
    NULL::text AS sub_description,
    price_item_id, category, boat_id, exemption_role
FROM public.invoice_lines;

CREATE VIEW dev_masked.account_mapping_rules AS
SELECT
    id, club_id,
    NULL::text AS name,
    priority, match_field,
    NULL::text AS match_value,            -- may match on a payer's name
    match_operator, debit_account_id, credit_account_id, mva_eligible,
    is_active, created_at
FROM public.account_mapping_rules;

CREATE VIEW dev_masked.vipps_message_rules AS
SELECT
    id, club_id,
    NULL::text AS pattern,                -- may match on a payer's name
    account_code, priority, created_at
FROM public.vipps_message_rules;

CREATE VIEW dev_masked.slips AS
SELECT
    id, club_id, number, section, length_m, width_m, depth_m, status,
    map_x, map_y, created_at, updated_at,
    NULL::text AS notes,
    location
FROM public.slips;

CREATE VIEW dev_masked.dock_fingers AS
SELECT
    id, club_id, geometry, position, created_at, updated_at,
    NULL::text AS notes
FROM public.dock_fingers;

-- ── Keyed name tokens (BRY-343) ─────────────────────────────────────────
-- Lets the role tell that a bank payer, a Vipps customer, an invoice's
-- member snapshot and a member are the same person without seeing the
-- name: each name becomes the set of HMAC(key, "wordA wordB") over its
-- unordered word pairs, truncated to 12 hex. Match with array overlap:
--   SELECT … FROM bank_row_payer_tokens b JOIN user_name_tokens u
--     ON b.payer_pair_toks && u.name_pair_toks
-- Pairs (not single words) so a token is near-unique per person and gives
-- no usable frequency signal, while still matching across word order,
-- middle names and double surnames. Spouses/joint accounts are linked by
-- household_tok (address), not by a shared surname.
--
-- Rules that keep this safe:
-- * The key (dev_private.token_key, migration 000098) is unreadable by the
--   role and read here only through a scalar subquery. Never via a
--   function: EXECUTE inside a view is checked against the caller, so a
--   function either fails for the role or becomes an oracle for tokenising
--   arbitrary text; an IMMUTABLE accessor also gets constant-folded into
--   EXPLAIN VERBOSE output.
-- * Only tokenise names the agent cannot write. orders.guest_name is
--   excluded: the public QR pay endpoint stores any caller-supplied name
--   before payment, which would make it a chosen-plaintext oracle.
-- * Normalisation mirrors accounting.normalizeName: lowercase, Norwegian
--   and Latin diacritics folded (æ→ae, ø→o, å→a …), "." and "," dropped,
--   anything else non-letter becomes a space, one-letter initials dropped,
--   first four distinct words kept.
DO $do$
DECLARE
    pairs CONSTANT text := $t$(
        WITH w AS (
            SELECT u.w
            FROM unnest(regexp_split_to_array(trim(regexp_replace(regexp_replace(
                     replace(translate(lower(%1$s),
                             'øåäöüéèêáàâíìóòôúùñç', 'oaaoueeeaaaiiooouunc'), 'æ', 'ae'),
                     '[.,]', '', 'g'), '[^a-z]+', ' ', 'g')), ' '))
                 WITH ORDINALITY AS u(w, ord)
            WHERE length(u.w) > 1
            GROUP BY u.w
            ORDER BY min(u.ord)
            LIMIT 4
        )
        SELECT array_agg(t ORDER BY t) FROM (
            SELECT DISTINCT left(encode(public.hmac(
                       convert_to(a.w || ' ' || b.w, 'UTF8'),
                       (SELECT k.key FROM dev_private.token_key k), 'sha256'), 'hex'), 12) AS t
            FROM w a JOIN w b ON a.w < b.w
        ) s
    )$t$;
BEGIN
    EXECUTE format($v$
        CREATE VIEW dev_masked.user_name_tokens AS
        SELECT u.id AS user_id, u.club_id,
               %s AS name_pair_toks,
               CASE WHEN nullif(trim(u.address_line), '') IS NOT NULL THEN
                   left(encode(public.hmac(convert_to(trim(regexp_replace(
                       replace(translate(lower(u.address_line || ' ' || coalesce(u.postal_code, '')),
                               'øåäöüéèêáàâíìóòôúùñç', 'oaaoueeeaaaiiooouunc'), 'æ', 'ae'),
                       '[^a-z0-9]+', ' ', 'g')), 'UTF8'),
                       (SELECT k.key FROM dev_private.token_key k), 'sha256'), 'hex'), 12)
               END AS household_tok
        FROM public.users u
    $v$, format(pairs, $e$concat_ws(' ', u.first_name, u.last_name)$e$));

    EXECUTE format($v$
        CREATE VIEW dev_masked.invoice_name_tokens AS
        SELECT i.id AS invoice_id, i.club_id, i.user_id, i.recipient_id,
               %s AS member_pair_toks,
               %s AS recipient_contact_pair_toks
        FROM public.invoices i
    $v$, format(pairs, $e$i.member_name$e$), format(pairs, $e$i.recipient_contact_person$e$));

    -- Payer: the "Fra: <name> Betalt:" prefix when present, else the
    -- counterpart, ignoring the club's own name (intra-bank transfers).
    EXECUTE format($v$
        CREATE VIEW dev_masked.bank_row_payer_tokens AS
        SELECT b.id AS bank_import_row_id, b.club_id,
               %s AS payer_pair_toks
        FROM public.bank_import_rows b
        JOIN public.clubs c ON c.id = b.club_id
    $v$, format(pairs, $e$coalesce(
            nullif(substring(b.description from '^Fra:\s*(.+?)\s+Betalt:'), ''),
            CASE WHEN lower(b.counterpart) = lower(c.name) THEN NULL ELSE nullif(b.counterpart, '') END)$e$));

    EXECUTE format($v$
        CREATE VIEW dev_masked.vipps_row_customer_tokens AS
        SELECT v.id AS vipps_import_row_id, v.club_id,
               %s AS customer_pair_toks
        FROM public.vipps_import_rows v
    $v$, format(pairs, $e$v.customer_name$e$));

    EXECUTE format($v$
        CREATE VIEW dev_masked.billing_recipient_tokens AS
        SELECT r.id AS recipient_id, r.club_id,
               %s AS contact_pair_toks
        FROM public.billing_recipients r
    $v$, format(pairs, $e$r.contact_person$e$));
END
$do$;

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
