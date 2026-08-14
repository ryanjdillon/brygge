-- Passkey step-up (BRY-250). The admin step-up gate keys freshness off
-- sessions.totp_verified_at, which now means "last step-up by ANY factor" —
-- both a TOTP code and a passkey assertion stamp it. This column records which
-- factor was used, for /session/me and audit. No behavioural change on its own.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS mfa_method TEXT;
