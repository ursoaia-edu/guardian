-- name: RecordProcessEvent :exec
INSERT INTO process_events
    (account_id, computer_id, room_id, process, reason, count, first_at, last_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetLastEventBatch :exec
-- Stamped in the same transaction as the batch it acknowledges, so a partially
-- recorded batch cannot exist.
UPDATE computers SET last_event_batch = $2 WHERE id = $1;

-- name: ListRoomProcessEvents :many
-- One query rather than a family of them: five optional filters would
-- otherwise be thirty-two statements. Each filter is inert when its argument
-- is NULL. The cursor is the same (created_at, id) row comparison the events
-- feed uses -- keyed on the pair rather than an offset, because rows arrive
-- while somebody is paging and OFFSET would silently repeat or skip them.
SELECT pe.id, pe.computer_id, pe.room_id, pe.process, pe.reason, pe.count,
       pe.first_at, pe.last_at, pe.created_at,
       COALESCE(NULLIF(c.display_name, ''), c.hostname)::text AS computer_name
FROM process_events pe
JOIN computers c ON c.id = pe.computer_id
WHERE pe.room_id = sqlc.arg(room_id)::uuid
  AND (sqlc.narg(process)::text     IS NULL OR pe.process     = sqlc.narg(process)::text)
  AND (sqlc.narg(reason)::text      IS NULL OR pe.reason      = sqlc.narg(reason)::text)
  AND (sqlc.narg(computer)::uuid    IS NULL OR pe.computer_id = sqlc.narg(computer)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR pe.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR pe.created_at <= sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(before_time)::timestamptz IS NULL
       OR (pe.created_at, pe.id) < (sqlc.narg(before_time)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY pe.created_at DESC, pe.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListComputerProcessEvents :many
-- Its own statement rather than the room query with a computer filter: a
-- machine in no room has room_id NULL on every row, and the room query can
-- never match it.
SELECT pe.id, pe.computer_id, pe.room_id, pe.process, pe.reason, pe.count,
       pe.first_at, pe.last_at, pe.created_at,
       COALESCE(NULLIF(c.display_name, ''), c.hostname)::text AS computer_name
FROM process_events pe
JOIN computers c ON c.id = pe.computer_id
WHERE pe.computer_id = sqlc.arg(computer_id)::uuid
  AND (sqlc.narg(process)::text     IS NULL OR pe.process = sqlc.narg(process)::text)
  AND (sqlc.narg(reason)::text      IS NULL OR pe.reason  = sqlc.narg(reason)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR pe.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR pe.created_at <= sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(before_time)::timestamptz IS NULL
       OR (pe.created_at, pe.id) < (sqlc.narg(before_time)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY pe.created_at DESC, pe.id DESC
LIMIT sqlc.arg(row_limit);
