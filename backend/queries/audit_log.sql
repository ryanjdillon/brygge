-- name: InsertAuditLog :one
INSERT INTO audit_log (
    club_id, actor_id, actor_ip, action, resource, resource_id, details
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListAuditLogByResource :many
SELECT * FROM audit_log
WHERE resource = $1 AND resource_id = $2
ORDER BY created_at DESC;

-- name: ListAuditLogByClub :many
SELECT * FROM audit_log
WHERE club_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
