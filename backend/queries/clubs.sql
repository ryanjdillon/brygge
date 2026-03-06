-- name: CreateClub :one
INSERT INTO clubs (
    slug, name, description, latitude, longitude,
    municipality_codes, postal_codes, config
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetClubBySlug :one
SELECT * FROM clubs
WHERE slug = $1;

-- name: GetClubByID :one
SELECT * FROM clubs
WHERE id = $1;

-- name: UpdateClub :one
UPDATE clubs SET
    name               = COALESCE(sqlc.narg('name'), name),
    description        = COALESCE(sqlc.narg('description'), description),
    latitude           = COALESCE(sqlc.narg('latitude'), latitude),
    longitude          = COALESCE(sqlc.narg('longitude'), longitude),
    municipality_codes = COALESCE(sqlc.narg('municipality_codes'), municipality_codes),
    postal_codes       = COALESCE(sqlc.narg('postal_codes'), postal_codes),
    config             = COALESCE(sqlc.narg('config'), config),
    updated_at         = now()
WHERE id = $1
RETURNING *;
