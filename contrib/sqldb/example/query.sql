-- name: GetAuthor :one
SELECT id, name, bio FROM authors WHERE id = ?;

-- name: ListAuthors :many
SELECT id, name, bio FROM authors ORDER BY name;

-- name: CreateAuthor :one
INSERT INTO authors (name, bio) VALUES (?, ?)
RETURNING id, name, bio;

-- name: DeleteAuthor :exec
DELETE FROM authors WHERE id = ?;
