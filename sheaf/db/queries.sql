-- name: InsertSearch :exec
INSERT INTO searches (query, created_at_unix_ms)
VALUES (?, ?);
