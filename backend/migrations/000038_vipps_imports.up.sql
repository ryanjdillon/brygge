CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE vipps_imports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id      UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    msn          TEXT NOT NULL DEFAULT '',
    period_start DATE,
    period_end   DATE,
    imported_by  UUID NOT NULL REFERENCES users(id),
    row_count    INT  NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_vipps_imports_club ON vipps_imports(club_id);

CREATE TABLE vipps_import_rows (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vipps_import_id       UUID NOT NULL REFERENCES vipps_imports(id) ON DELETE CASCADE,
    club_id               UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    row_hash              CHAR(64) NOT NULL,
    row_type              TEXT NOT NULL,
    tx_at                 TIMESTAMPTZ,
    booking_date          DATE,
    amount                NUMERIC(12,2) NOT NULL DEFAULT 0,
    fee                   NUMERIC(12,2) NOT NULL DEFAULT 0,
    net_amount            NUMERIC(12,2) NOT NULL DEFAULT 0,
    customer_name         TEXT NOT NULL DEFAULT '',
    customer_phone_masked TEXT NOT NULL DEFAULT '',
    message               TEXT NOT NULL DEFAULT '',
    psp_ref               TEXT NOT NULL DEFAULT '',
    order_id              TEXT NOT NULL DEFAULT '',
    settlement_number     TEXT NOT NULL DEFAULT '',
    payout_account        TEXT NOT NULL DEFAULT '',
    scheduled_payout_date DATE,
    msn                   TEXT NOT NULL DEFAULT '',
    journal_entry_id      UUID REFERENCES journal_entries(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_vipps_import_rows_import     ON vipps_import_rows(vipps_import_id);
CREATE INDEX idx_vipps_import_rows_settlement ON vipps_import_rows(club_id, settlement_number) WHERE settlement_number <> '';
CREATE INDEX idx_vipps_import_rows_unmatched  ON vipps_import_rows(vipps_import_id) WHERE journal_entry_id IS NULL;
CREATE UNIQUE INDEX idx_vipps_import_rows_dedup ON vipps_import_rows(club_id, row_hash);
