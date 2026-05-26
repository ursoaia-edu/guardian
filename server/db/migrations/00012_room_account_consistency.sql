-- +goose Up
-- A computer may only point at a room belonging to its own account. This is the
-- database-level counterpart to the GetRoom guard in handlePatchComputer: RLS
-- cannot express it, because the policy on computers tests that row's own
-- account_id and never looks at the room it references.
--
-- room_id is nullable and the default MATCH SIMPLE semantics leave the
-- constraint unenforced when any referenced column is NULL, so an unassigned
-- computer is unaffected.
ALTER TABLE rooms ADD CONSTRAINT rooms_account_id_id_key UNIQUE (account_id, id);

ALTER TABLE computers
    ADD CONSTRAINT computers_room_same_account
    FOREIGN KEY (account_id, room_id) REFERENCES rooms (account_id, id)
    ON DELETE SET NULL;

-- The single-column FK is now redundant: the composite one already guarantees
-- the room exists, and keeping both would fire two lookups per write.
ALTER TABLE computers DROP CONSTRAINT computers_room_id_fkey;

-- +goose Down
ALTER TABLE computers
    ADD CONSTRAINT computers_room_id_fkey
    FOREIGN KEY (room_id) REFERENCES rooms (id) ON DELETE SET NULL;
ALTER TABLE computers DROP CONSTRAINT computers_room_same_account;
ALTER TABLE rooms DROP CONSTRAINT rooms_account_id_id_key;
