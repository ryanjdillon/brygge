-- Revert to default-allow (the pre-BRY-286 state established by 000051).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON ALL TABLES IN SCHEMA public TO brygge_dev_ro';
        EXECUTE 'REVOKE ALL ON TABLE audit_log FROM brygge_dev_ro';
        EXECUTE 'ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public '
             || 'GRANT SELECT ON TABLES TO brygge_dev_ro';
    END IF;
END
$$;
