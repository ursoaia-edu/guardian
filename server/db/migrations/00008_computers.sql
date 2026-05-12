-- +goose Up
CREATE TABLE computers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_id       UUID REFERENCES rooms(id) ON DELETE SET NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    machine_guid  TEXT NOT NULL,
    hostname      TEXT NOT NULL DEFAULT '',
    os_name       TEXT NOT NULL DEFAULT '',
    os_build      TEXT NOT NULL DEFAULT '',
    arch          TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    hardware      JSONB NOT NULL DEFAULT '{}'::jsonb,
    runtime       JSONB NOT NULL DEFAULT '{}'::jsonb,
    token_hash    TEXT NOT NULL,
    blocked       BOOLEAN NOT NULL DEFAULT false,
    enrolled_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ,
    UNIQUE (account_id, machine_guid)
);
CREATE UNIQUE INDEX idx_computers_token_hash ON computers(token_hash);
CREATE INDEX idx_computers_room ON computers(room_id);

ALTER TABLE computers ENABLE ROW LEVEL SECURITY;
CREATE POLICY computers_isolation ON computers
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- +goose Down
DROP TABLE computers;
