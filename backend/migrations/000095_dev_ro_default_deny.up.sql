-- Default-deny for the brygge_dev_ro read-only role (BRY-286).
--
-- Migration 000051 granted the role SELECT on ALL tables (default-allow),
-- and the role-bootstrap set ALTER DEFAULT PRIVILEGES so every future table
-- auto-granted too. That exposed every table added since — credentials
-- (sessions, magic_links, webauthn_credentials, …), name→user maps
-- (vipps_customer_aliases) and more. Flip to default-deny: the role may read
-- only the PII-masking dev_masked.* views (granted by the repeatable file
-- migrations/repeatable/dev_masked.sql) and an explicit allowlist of
-- verified non-PII base tables.
--
-- A new table is therefore invisible to the role until it is explicitly
-- classified — the drift guard. Tables holding PII/secrets (users, clubs,
-- orders, invoices, boats, billing_recipients, the bank/vipps import rows,
-- vipps_webhook_events, slip_shares) are reached only through their masked
-- view; everything unlisted is denied.
--
-- Guarded on role existence so a cluster without the bootstrap still migrates.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        -- Stop future tables from auto-granting SELECT to the role.
        EXECUTE 'ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public '
             || 'REVOKE SELECT ON TABLES FROM brygge_dev_ro';
        -- Remove the blanket SELECT granted in 000051.
        EXECUTE 'REVOKE SELECT ON ALL TABLES IN SCHEMA public FROM brygge_dev_ro';
        -- Allowlist: non-PII financial / structural tables the role reads raw.
        -- (PII/secret tables are reached only via dev_masked.* views.)
        -- Unqualified names resolve via search_path — public in prod and
        -- migrate-check, the per-test schema under the isolated test harness.
        EXECUTE 'GRANT SELECT ON '
             || 'accounts, account_mapping_rules, bank_imports, '
             || 'dock_fingers, docks, fiscal_periods, invoice_lines, '
             || 'journal_entries, journal_lines, mva_compensation_reports, '
             || 'order_lines, order_sources, payments, price_items, '
             || 'product_variants, products, slip_assignments, slips, '
             || 'vipps_imports, vipps_message_rules, waiting_list_entries '
             || 'TO brygge_dev_ro';
    END IF;
END
$$;
