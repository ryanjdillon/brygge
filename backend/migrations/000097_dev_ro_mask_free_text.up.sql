-- Narrow the brygge_dev_ro allowlist (BRY-336). Migration 000095 granted
-- these tables raw on the theory that accounting/structural data holds no
-- PII, but each has free-text columns that can: bank-import journal
-- entries copy the bank text including the payer's name, and rules, notes
-- and line descriptions are typed by people. They are now read only via
-- their dev_masked views (migrations/repeatable/dev_masked.sql), which NULL
-- the free text. Rule: only tables with no free-text columns stay raw.
--
-- Unqualified names resolve via search_path (public in prod/migrate-check,
-- the per-test schema under the isolated harness).

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'REVOKE SELECT ON '
             || 'journal_entries, journal_lines, payments, invoice_lines, '
             || 'account_mapping_rules, vipps_message_rules, slips, dock_fingers '
             || 'FROM brygge_dev_ro';
    END IF;
END
$$;
