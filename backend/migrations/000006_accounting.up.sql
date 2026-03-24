BEGIN;

-- Enum types
CREATE TYPE account_type AS ENUM ('asset', 'liability', 'revenue', 'expense');
CREATE TYPE journal_status AS ENUM ('draft', 'posted', 'voided');
CREATE TYPE fiscal_period_status AS ENUM ('open', 'closed', 'locked');
CREATE TYPE transaction_source AS ENUM ('manual', 'bank_import', 'payment_sync', 'invoice_sync', 'vipps');
CREATE TYPE mva_eligibility AS ENUM ('eligible', 'ineligible', 'partial', 'not_applicable');

-- Fiscal periods (regnskapsperioder)
CREATE TABLE fiscal_periods (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    year        INT NOT NULL,
    start_date  DATE NOT NULL,
    end_date    DATE NOT NULL,
    status      fiscal_period_status NOT NULL DEFAULT 'open',
    closed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    closed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (club_id, year),
    CONSTRAINT fiscal_period_date_range CHECK (end_date > start_date)
);

CREATE INDEX idx_fiscal_periods_club ON fiscal_periods(club_id);

-- Chart of accounts (kontoplan)
CREATE TABLE accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    code            TEXT NOT NULL,
    name            TEXT NOT NULL,
    account_type    account_type NOT NULL,
    parent_code     TEXT,
    is_system       BOOLEAN NOT NULL DEFAULT false,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    mva_eligible    mva_eligibility NOT NULL DEFAULT 'not_applicable',
    description     TEXT NOT NULL DEFAULT '',
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (club_id, code)
);

CREATE INDEX idx_accounts_club ON accounts(club_id);
CREATE INDEX idx_accounts_type ON accounts(club_id, account_type);

-- Journal entries (bilag)
CREATE TABLE journal_entries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    fiscal_period_id UUID NOT NULL REFERENCES fiscal_periods(id),
    entry_number    INT NOT NULL,
    entry_date      DATE NOT NULL,
    description     TEXT NOT NULL,
    status          journal_status NOT NULL DEFAULT 'draft',
    source          transaction_source NOT NULL DEFAULT 'manual',
    source_id       TEXT,
    source_table    TEXT,
    attachment_url  TEXT,
    created_by      UUID NOT NULL REFERENCES users(id),
    posted_by       UUID REFERENCES users(id),
    posted_at       TIMESTAMPTZ,
    voided_by       UUID REFERENCES users(id),
    voided_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (club_id, fiscal_period_id, entry_number)
);

CREATE INDEX idx_journal_entries_club ON journal_entries(club_id);
CREATE INDEX idx_journal_entries_period ON journal_entries(fiscal_period_id);
CREATE INDEX idx_journal_entries_date ON journal_entries(club_id, entry_date);
CREATE INDEX idx_journal_entries_source ON journal_entries(source, source_id) WHERE source_id IS NOT NULL;

-- Journal lines (posteringslinjer)
CREATE TABLE journal_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journal_entry_id    UUID NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    account_id          UUID NOT NULL REFERENCES accounts(id),
    debit               NUMERIC(12,2) NOT NULL DEFAULT 0,
    credit              NUMERIC(12,2) NOT NULL DEFAULT 0,
    description         TEXT NOT NULL DEFAULT '',
    mva_amount          NUMERIC(12,2) NOT NULL DEFAULT 0,
    mva_eligible        mva_eligibility NOT NULL DEFAULT 'not_applicable',
    CONSTRAINT journal_line_one_side CHECK (
        (debit > 0 AND credit = 0) OR (credit > 0 AND debit = 0)
    )
);

CREATE INDEX idx_journal_lines_entry ON journal_lines(journal_entry_id);
CREATE INDEX idx_journal_lines_account ON journal_lines(account_id);

-- Bank statement imports
CREATE TABLE bank_imports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    filename        TEXT NOT NULL,
    format          TEXT NOT NULL DEFAULT 'csv',
    imported_by     UUID NOT NULL REFERENCES users(id),
    row_count       INT NOT NULL DEFAULT 0,
    matched_count   INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bank_imports_club ON bank_imports(club_id);

CREATE TABLE bank_import_rows (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bank_import_id      UUID NOT NULL REFERENCES bank_imports(id) ON DELETE CASCADE,
    row_date            DATE NOT NULL,
    description         TEXT NOT NULL,
    amount              NUMERIC(12,2) NOT NULL,
    balance             NUMERIC(12,2),
    reference           TEXT NOT NULL DEFAULT '',
    kid_number          TEXT NOT NULL DEFAULT '',
    counterpart         TEXT NOT NULL DEFAULT '',
    journal_entry_id    UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    auto_matched        BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bank_import_rows_import ON bank_import_rows(bank_import_id);
CREATE INDEX idx_bank_import_rows_kid ON bank_import_rows(kid_number) WHERE kid_number != '';
CREATE INDEX idx_bank_import_rows_unmatched ON bank_import_rows(bank_import_id) WHERE journal_entry_id IS NULL;

-- Auto-categorization rules
CREATE TABLE account_mapping_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id             UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    priority            INT NOT NULL DEFAULT 0,
    match_field         TEXT NOT NULL,
    match_value         TEXT NOT NULL,
    match_operator      TEXT NOT NULL DEFAULT 'eq',
    debit_account_id    UUID REFERENCES accounts(id),
    credit_account_id   UUID REFERENCES accounts(id),
    mva_eligible        mva_eligibility NOT NULL DEFAULT 'not_applicable',
    is_active           BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_account_mapping_rules_club ON account_mapping_rules(club_id);

-- Momskompensasjon reports
CREATE TABLE mva_compensation_reports (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id                 UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    fiscal_period_id        UUID NOT NULL REFERENCES fiscal_periods(id),
    model                   TEXT NOT NULL DEFAULT 'simplified',
    total_operating_costs   NUMERIC(12,2) NOT NULL DEFAULT 0,
    eligible_costs          NUMERIC(12,2) NOT NULL DEFAULT 0,
    ineligible_costs        NUMERIC(12,2) NOT NULL DEFAULT 0,
    compensation_amount     NUMERIC(12,2) NOT NULL DEFAULT 0,
    status                  TEXT NOT NULL DEFAULT 'draft',
    submitted_at            TIMESTAMPTZ,
    generated_by            UUID REFERENCES users(id),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (club_id, fiscal_period_id)
);

CREATE INDEX idx_mva_comp_club ON mva_compensation_reports(club_id);

COMMIT;
