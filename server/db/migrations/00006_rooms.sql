-- +goose Up
CREATE TABLE rooms (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    mode               TEXT NOT NULL DEFAULT 'blacklist'
                       CHECK (mode IN ('blacklist', 'whitelist')),
    protection_enabled BOOLEAN NOT NULL DEFAULT false,
    power_allowed      BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rooms_account ON rooms(account_id);

CREATE TABLE room_members (
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('member')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX idx_room_members_user ON room_members(user_id);

ALTER TABLE rooms ENABLE ROW LEVEL SECURITY;
CREATE POLICY rooms_isolation ON rooms
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

ALTER TABLE room_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY room_members_isolation ON room_members
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- +goose Down
DROP TABLE room_members;
DROP TABLE rooms;
