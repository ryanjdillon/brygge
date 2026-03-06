-- name: GrantRole :one
INSERT INTO user_roles (user_id, club_id, role, granted_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, club_id, role) DO NOTHING
RETURNING *;

-- name: RevokeRole :exec
DELETE FROM user_roles
WHERE user_id = $1 AND club_id = $2 AND role = $3;

-- name: GetUserRoles :many
SELECT * FROM user_roles
WHERE user_id = $1 AND club_id = $2
ORDER BY granted_at;

-- name: HasRole :one
SELECT EXISTS (
    SELECT 1 FROM user_roles
    WHERE user_id = $1 AND club_id = $2 AND role = $3
) AS has_role;
