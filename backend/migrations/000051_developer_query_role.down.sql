-- Reverses the table-level grants from the .up migration.
-- Role-membership and ALTER DEFAULT PRIVILEGES are owned by the
-- brygge-dev-query-role NixOS unit and are not touched here.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'REVOKE SELECT ON ALL TABLES IN SCHEMA public FROM brygge_dev_ro';
    END IF;
END
$$;
