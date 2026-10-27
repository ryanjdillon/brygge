ALTER TABLE harbor_payment_targets DROP CONSTRAINT IF EXISTS harbor_payment_targets_short_code_format;
DROP INDEX IF EXISTS idx_harbor_targets_club_short_code;
ALTER TABLE harbor_payment_targets DROP COLUMN IF EXISTS short_code;
