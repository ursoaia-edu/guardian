-- name: RecordEvent :exec
INSERT INTO events (account_id, room_id, computer_id, type, payload)
VALUES ($1, $2, $3, $4, $5);

-- name: ListRecentEvents :many
SELECT * FROM events ORDER BY created_at DESC, id DESC LIMIT $1;

-- name: ListEventsBefore :many
-- The next page. Keyed on (created_at, id) rather than an offset: events are
-- inserted while somebody is paging through them, and OFFSET would silently
-- repeat or skip rows as the feed grows underneath the reader. The row
-- comparison matches the index on (account_id, created_at DESC).
SELECT * FROM events
WHERE (created_at, id) < (sqlc.arg(before_time)::timestamptz, sqlc.arg(before_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
