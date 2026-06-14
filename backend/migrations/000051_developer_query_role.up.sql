-- DIL-365 Phase 1a: read-only Postgres role used by the dev-query
-- endpoint. The endpoint does `SET LOCAL ROLE brygge_dev_ro` inside a
-- transaction so that even if an injection slips past the Go-side
-- syntactic validator, Postgres itself enforces SELECT-only and only
-- against tables we've explicitly granted.
--
-- Two tables are NOT grantable to this role:
--   * audit_log         — the role mustn't be able to enumerate or
--                         erase its own evidence. See
--                         docs/developer/reference/invariants.md
--                         "Token DB role cannot SELECT its own evidence".
--   * developer_tokens  — reserved for Phase 1b (DIL-365 sub-issue).
--                         When that table lands its migration must
--                         REVOKE SELECT from brygge_dev_ro.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        CREATE ROLE brygge_dev_ro NOLOGIN;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO brygge_dev_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO brygge_dev_ro;

-- Strip the sensitive tables. audit_log lives in public; if no
-- developer_tokens exists yet, the REVOKE on it is a harmless no-op.
REVOKE ALL ON TABLE audit_log FROM brygge_dev_ro;

-- Default privileges so future tables auto-grant SELECT to the role
-- (one less foot-gun for future migrations). Must be issued by the
-- role that will own those tables; brygge is the app role per
-- DATABASE_URL.
ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public
    GRANT SELECT ON TABLES TO brygge_dev_ro;

-- The app role must be allowed to SET LOCAL ROLE brygge_dev_ro inside
-- a transaction. GRANTing the role membership achieves this.
GRANT brygge_dev_ro TO brygge;
