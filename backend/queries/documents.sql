-- name: CreateDocument :one
INSERT INTO documents (
    club_id, title, filename, s3_key,
    content_type, size_bytes, visibility, uploaded_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetDocumentByID :one
SELECT * FROM documents
WHERE id = $1 AND club_id = $2;

-- name: ListDocumentsByClub :many
SELECT * FROM documents
WHERE club_id = $1
  AND (visibility = 'member' OR visibility = sqlc.arg('max_visibility')::document_visibility)
ORDER BY created_at DESC;

-- name: DeleteDocument :exec
DELETE FROM documents
WHERE id = $1 AND club_id = $2;

-- name: CreateDocumentComment :one
INSERT INTO document_comments (
    document_id, user_id, club_id, body
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: ListDocumentComments :many
SELECT dc.*, u.full_name AS author_name
FROM document_comments dc
JOIN users u ON u.id = dc.user_id
WHERE dc.document_id = $1 AND dc.club_id = $2
ORDER BY dc.created_at;
