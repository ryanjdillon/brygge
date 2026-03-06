-- name: CreateEvent :one
INSERT INTO events (
    club_id, title, description, location,
    start_time, end_time, tag, is_public, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetEventByID :one
SELECT * FROM events
WHERE id = $1 AND club_id = $2;

-- name: ListPublicEvents :many
SELECT * FROM events
WHERE club_id = $1 AND is_public = true
  AND end_time >= now()
ORDER BY start_time;

-- name: ListAllEvents :many
SELECT * FROM events
WHERE club_id = $1
  AND start_time >= COALESCE(sqlc.narg('after')::timestamptz, '1970-01-01'::timestamptz)
  AND start_time <= COALESCE(sqlc.narg('before')::timestamptz, '9999-12-31'::timestamptz)
ORDER BY start_time;

-- name: UpdateEvent :one
UPDATE events SET
    title       = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    location    = COALESCE(sqlc.narg('location'), location),
    start_time  = COALESCE(sqlc.narg('start_time'), start_time),
    end_time    = COALESCE(sqlc.narg('end_time'), end_time),
    tag         = COALESCE(sqlc.narg('tag'), tag),
    is_public   = COALESCE(sqlc.narg('is_public'), is_public),
    updated_at  = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: DeleteEvent :exec
DELETE FROM events
WHERE id = $1 AND club_id = $2;
