-- name: ListComputers :many
SELECT * FROM computers ORDER BY COALESCE(NULLIF(display_name, ''), hostname);

-- name: CountComputers :one
SELECT count(*) FROM computers;

-- name: UpdateComputer :one
UPDATE computers SET
    display_name = COALESCE(sqlc.narg(display_name), display_name),
    room_id      = CASE WHEN sqlc.arg(set_room)::boolean
                        THEN sqlc.narg(room_id) ELSE room_id END,
    blocked      = COALESCE(sqlc.narg(blocked), blocked)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetComputerByGUID :one
SELECT * FROM computers WHERE account_id = $1 AND machine_guid = $2;

-- name: LockAccountComputerLimit :one
-- FOR UPDATE, and read before the seats are counted: without the lock, two
-- installers enrolling at the same moment both read count = limit - 1, both
-- decide there is room, and the account ends up over its plan. The lock is
-- taken on the account row rather than the computers table because that is
-- the thing being rationed, and it serialises only enrollments of the same
-- account.
SELECT computer_limit FROM accounts WHERE id = $1 FOR UPDATE;

-- name: UpsertComputerByGUID :one
-- Reinstalling an agent on a known machine updates the row and rotates its
-- token instead of adding a duplicate to the pool.
INSERT INTO computers (
    account_id, machine_guid, hostname, os_name, os_build, arch,
    agent_version, hardware, token_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(hardware), '{}'::jsonb), $8)
ON CONFLICT (account_id, machine_guid) DO UPDATE SET
    hostname      = EXCLUDED.hostname,
    os_name       = EXCLUDED.os_name,
    os_build      = EXCLUDED.os_build,
    arch          = EXCLUDED.arch,
    agent_version = EXCLUDED.agent_version,
    hardware      = EXCLUDED.hardware,
    token_hash    = EXCLUDED.token_hash,
    enrolled_at   = now()
RETURNING *;

-- name: GetComputerByTokenHash :one
SELECT * FROM computers WHERE token_hash = $1;

-- name: TouchComputer :exec
UPDATE computers SET last_seen_at = now(), runtime = $2 WHERE id = $1;
