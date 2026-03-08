-- Add missing indexes on foreign key columns for join/delete performance

-- project_events
CREATE INDEX IF NOT EXISTS idx_project_events_project ON project_events(project_id);
CREATE INDEX IF NOT EXISTS idx_project_events_event ON project_events(event_id);

-- boats
CREATE INDEX IF NOT EXISTS idx_boats_model ON boats(boat_model_id);

-- waiting_list_entries
CREATE INDEX IF NOT EXISTS idx_waiting_list_boat ON waiting_list_entries(boat_id);

-- slip_assignments
CREATE INDEX IF NOT EXISTS idx_slip_assignments_boat ON slip_assignments(boat_id);

-- shopping_list_items
CREATE INDEX IF NOT EXISTS idx_shopping_list_items_task ON shopping_list_items(task_id);

-- resource_cancellation_policies
CREATE INDEX IF NOT EXISTS idx_cancellation_policies_resource ON resource_cancellation_policies(resource_id);

-- slip_share_rebates
CREATE INDEX IF NOT EXISTS idx_slip_share_rebates_booking ON slip_share_rebates(booking_id);

-- order_lines
CREATE INDEX IF NOT EXISTS idx_order_lines_product ON order_lines(product_id);
CREATE INDEX IF NOT EXISTS idx_order_lines_variant ON order_lines(variant_id);

-- push_subscriptions
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions(user_id);

-- deletion_requests
CREATE INDEX IF NOT EXISTS idx_deletion_requests_user ON deletion_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_deletion_requests_club_status ON deletion_requests(club_id, status);

-- notification_preferences (composite for lookup)
CREATE INDEX IF NOT EXISTS idx_notif_prefs_user_club ON notification_preferences(user_id, club_id);

-- forum_messages (for room listing with ordering)
CREATE INDEX IF NOT EXISTS idx_forum_messages_room ON forum_messages(room_id, created_at DESC);
