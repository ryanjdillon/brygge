-- name: InsertAuditLog :one
INSERT INTO audit_log (
    club_id, user_id, action, entity_type, entity_id,
    old_data, new_data
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListAuditLogByEntity :many
SELECT * FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC;

-- name: ListAuditLogByClub :many
SELECT * FROM audit_log
WHERE club_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
