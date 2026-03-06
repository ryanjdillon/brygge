-- 000001_init.down.sql
-- Drop everything in reverse dependency order

BEGIN;

DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS matrix_rooms;
DROP TABLE IF EXISTS votes;
DROP TABLE IF EXISTS feature_requests;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS document_comments;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS waiting_list_entries;
DROP TABLE IF EXISTS slip_fees;
DROP TABLE IF EXISTS slip_assignments;
DROP TABLE IF EXISTS slips;
DROP TABLE IF EXISTS boats;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS clubs;

DROP TYPE IF EXISTS feature_request_status;
DROP TYPE IF EXISTS task_priority;
DROP TYPE IF EXISTS task_status;
DROP TYPE IF EXISTS document_visibility;
DROP TYPE IF EXISTS event_tag;
DROP TYPE IF EXISTS booking_status;
DROP TYPE IF EXISTS resource_type;
DROP TYPE IF EXISTS payment_status;
DROP TYPE IF EXISTS payment_type;
DROP TYPE IF EXISTS waiting_list_status;
DROP TYPE IF EXISTS slip_status;
DROP TYPE IF EXISTS user_role;

COMMIT;
