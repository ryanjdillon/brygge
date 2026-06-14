-- DIL-365 Phase 1a: read-only role used by the /admin/dev/query endpoint.
--
-- Role creation + role-membership grant + ALTER DEFAULT PRIVILEGES require
-- CREATEROLE / superuser privileges that the `brygge` app role does NOT
-- have. Those operations live in:
--   - prod:    nix/host.nix systemd.services.brygge-dev-query-role
--              (runs as the `postgres` user via peer auth, before
--              brygge-migrate.service)
--   - local:   `just dev-role-bootstrap`
--
-- This migration only does table-level grants — which `brygge` CAN do
-- because it owns the tables. The grants are guarded by an IF EXISTS
-- check so the migration still succeeds on a fresh DB where the role
-- hasn't been bootstrapped yet (the dev-query feature simply won't
-- work until bootstrap runs).
--
-- Sensitive table: audit_log is REVOKE'd so the role can't enumerate
-- or erase its own evidence (see docs/developer/reference/invariants.md
-- "Query role cannot SELECT its own evidence").

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON ALL TABLES IN SCHEMA public TO brygge_dev_ro';
        EXECUTE 'REVOKE ALL ON TABLE audit_log FROM brygge_dev_ro';
    ELSE
        RAISE NOTICE 'brygge_dev_ro role missing — skipping dev-query grants. '
                     'Provision via brygge-dev-query-role.service (prod) or '
                     '"just dev-role-bootstrap" (local). The dev-query feature '
                     'will return 500 until the role exists.';
    END IF;
END
$$;
