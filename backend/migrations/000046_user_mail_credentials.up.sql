-- Per-user Stalwart principal credentials (DIL-321). One row per
-- Brygge user that has been provisioned a Stalwart account. The
-- password is encrypted at rest with the TOTP_ENCRYPTION_KEY so a
-- DB-only leak doesn't expose live mailbox credentials.
--
-- Lazy provisioning: row created on first successful magic-link
-- login. Eager provisioning: row created when any board-mailbox
-- role is granted, so the reconciler's next pass can populate
-- `shareWith` immediately.
CREATE TABLE user_mail_credentials (
    user_id              UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    jmap_user            TEXT NOT NULL UNIQUE,
    jmap_password_encrypted BYTEA NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
