-- name: CreateUser :one
INSERT INTO users (
    club_id, email, password_hash, vipps_sub,
    first_name, last_name, phone, address_line, postal_code, city, is_local
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND club_id = $2;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND club_id = $2;

-- name: GetUserByVippsSub :one
SELECT * FROM users
WHERE vipps_sub = $1 AND club_id = $2;

-- name: UpdateUser :one
UPDATE users SET
    first_name    = COALESCE(sqlc.narg('first_name'), first_name),
    last_name     = COALESCE(sqlc.narg('last_name'), last_name),
    phone         = COALESCE(sqlc.narg('phone'), phone),
    address_line  = COALESCE(sqlc.narg('address_line'), address_line),
    postal_code   = COALESCE(sqlc.narg('postal_code'), postal_code),
    city          = COALESCE(sqlc.narg('city'), city),
    is_local      = COALESCE(sqlc.narg('is_local'), is_local),
    local_override_by = sqlc.narg('local_override_by'),
    password_hash = COALESCE(sqlc.narg('password_hash'), password_hash),
    vipps_sub     = COALESCE(sqlc.narg('vipps_sub'), vipps_sub),
    updated_at    = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: ListUsersByClub :many
SELECT * FROM users
WHERE club_id = $1
ORDER BY last_name, first_name;

-- name: DeleteUser :exec
DELETE FROM users
WHERE id = $1 AND club_id = $2;
