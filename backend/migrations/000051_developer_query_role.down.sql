-- Reverses 000051_developer_query_role.up.sql.

-- Default-privilege grants must be revoked in matching shape before
-- the role can be dropped.
ALTER DEFAULT PRIVILEGES FOR ROLE brygge IN SCHEMA public
    REVOKE SELECT ON TABLES FROM brygge_dev_ro;

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM brygge_dev_ro;
REVOKE USAGE ON SCHEMA public FROM brygge_dev_ro;
REVOKE brygge_dev_ro FROM brygge;

DROP ROLE IF EXISTS brygge_dev_ro;
