-- 000001_init.down.sql
-- Drop everything in reverse dependency order

BEGIN;

DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS revoked_tokens;
DROP TABLE IF EXISTS legal_documents;
DROP TABLE IF EXISTS deletion_requests;
DROP TABLE IF EXISTS user_consents;
DROP TABLE IF EXISTS notification_config;
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS map_markers;
DROP TABLE IF EXISTS order_lines;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS product_variants;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS price_items;
DROP TABLE IF EXISTS broadcasts;
DROP TABLE IF EXISTS matrix_rooms;
DROP TABLE IF EXISTS votes;
DROP TABLE IF EXISTS feature_requests;
DROP TABLE IF EXISTS shopping_list_items;
DROP TABLE IF EXISTS shopping_lists;
DROP TABLE IF EXISTS task_participants;
DROP TABLE IF EXISTS project_events;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS document_comments;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS club_settings;
DROP TABLE IF EXISTS slip_share_rebates;
DROP TABLE IF EXISTS slip_shares;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS resource_cancellation_policies;
DROP TABLE IF EXISTS resource_units;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS waiting_list_entries;
DROP TABLE IF EXISTS slip_fees;
DROP TABLE IF EXISTS slip_assignments;
DROP TABLE IF EXISTS slips;
DROP TABLE IF EXISTS boats;
DROP TABLE IF EXISTS boat_models;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS clubs;

DROP TYPE IF EXISTS order_status;
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
