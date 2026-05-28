-- name: ListAccessibleAccounts :many
-- Account membership and room grants both lead to an account. The strongest
-- role wins, so a user who is an admin and also a room member is an admin.
SELECT account_id, role FROM (
    SELECT m.account_id AS account_id, m.role AS role
    FROM account_members m WHERE m.user_id = $1
    UNION
    SELECT rm.account_id AS account_id, 'member'::text AS role
    FROM room_members rm WHERE rm.user_id = $1
) accessible
ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END;

-- name: ListRoomsForMember :many
SELECT r.* FROM rooms r
JOIN room_members rm ON rm.room_id = r.id
WHERE rm.user_id = $1
ORDER BY r.created_at;

-- name: IsRoomMember :one
SELECT EXISTS (SELECT 1 FROM room_members WHERE room_id = $1 AND user_id = $2);

-- name: ListComputersForMember :many
SELECT c.* FROM computers c
JOIN room_members rm ON rm.room_id = c.room_id
WHERE rm.user_id = $1
ORDER BY COALESCE(NULLIF(c.display_name, ''), c.hostname);

-- name: GetComputerForMember :one
SELECT c.* FROM computers c
JOIN room_members rm ON rm.room_id = c.room_id
WHERE c.id = $1 AND rm.user_id = $2;

-- name: AddRoomMember :exec
INSERT INTO room_members (room_id, account_id, user_id, role)
VALUES ($1, $2, $3, 'member')
ON CONFLICT (room_id, user_id) DO NOTHING;

-- name: ListRoomMembers :many
SELECT u.id, u.email, u.name FROM room_members rm
JOIN users u ON u.id = rm.user_id
WHERE rm.room_id = $1
ORDER BY u.email;

-- name: DeleteRoomMember :execrows
DELETE FROM room_members WHERE room_id = $1 AND user_id = $2;
