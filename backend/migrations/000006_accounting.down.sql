BEGIN;

DROP TABLE IF EXISTS mva_compensation_reports;
DROP TABLE IF EXISTS account_mapping_rules;
DROP TABLE IF EXISTS bank_import_rows;
DROP TABLE IF EXISTS bank_imports;
DROP TABLE IF EXISTS journal_lines;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS fiscal_periods;

DROP TYPE IF EXISTS mva_eligibility;
DROP TYPE IF EXISTS transaction_source;
DROP TYPE IF EXISTS fiscal_period_status;
DROP TYPE IF EXISTS journal_status;
DROP TYPE IF EXISTS account_type;

COMMIT;
