-- Tracks per-mailbox ACL reconciler state for the role-gated shared
-- inbox (DIL-275/276). `desired_hash` is computed from user_roles +
-- the tfvars spec; `applied_hash` is what Stalwart last accepted. When
-- they match, Reconcile() is a no-op. last_error is non-null only
-- after a failed apply; the next successful cycle clears it.
CREATE TABLE mailbox_sync_state (
    address      TEXT PRIMARY KEY,
    desired_hash TEXT NOT NULL,
    applied_hash TEXT,
    last_synced  TIMESTAMPTZ,
    last_error   TEXT
);
