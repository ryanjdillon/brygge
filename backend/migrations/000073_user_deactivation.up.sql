-- Member deactivation (BRY-203): a reversible "removed" state that hides a
-- member from active lists/counts while keeping the row (and its financial
-- history) intact. NULL = active; a timestamp = deactivated at that instant.
-- deactivated_by records the admin who did it for the audit trail. Actual
-- PII erasure happens later, only once the member's financial records clear
-- the bokføringsloven retention window (BRY-206/207).
ALTER TABLE users
  ADD COLUMN deactivated_at TIMESTAMPTZ,
  ADD COLUMN deactivated_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Most member reads filter to active members within a club; a partial index
-- keeps those lookups cheap and lets the planner skip deactivated rows.
CREATE INDEX idx_users_active
  ON users (club_id)
  WHERE deactivated_at IS NULL;
