-- +goose Up
CREATE TABLE applications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_id    UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    list       TEXT NOT NULL CHECK (list IN ('blacklist', 'whitelist')),
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (room_id, name, list)
);
CREATE INDEX idx_applications_room ON applications(room_id);

ALTER TABLE applications ENABLE ROW LEVEL SECURITY;
CREATE POLICY applications_isolation ON applications
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- +goose Down
DROP TABLE applications;
