ALTER TABLE accounts ADD COLUMN mva_partial_ratio NUMERIC(5,2) NOT NULL DEFAULT 1.00;

-- Set default ratio for the electricity account (shared between clubhouse and dock)
-- Clubs can adjust this via the UI
COMMENT ON COLUMN accounts.mva_partial_ratio IS 'For partial MVA eligibility: fraction of costs eligible for momskompensasjon (0.00-1.00)';
