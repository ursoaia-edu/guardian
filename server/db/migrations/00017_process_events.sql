-- +goose Up
-- The blocking log. Deliberately NOT rows in `events`: that table records
-- deliberate human acts, is read as a narrative and is kept 180 days. This one
-- is machine output — a misconfigured whitelist on one classroom produces
-- thousands of rows an hour — and it gets its own retention, its own endpoint
-- and its own tab. Sharing a table would drown the audit feed in noise and
-- force one retention policy onto two kinds of data with different value.
CREATE TABLE process_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID NOT NULL REFERENCES accounts(id)  ON DELETE CASCADE,
    computer_id UUID NOT NULL REFERENCES computers(id) ON DELETE CASCADE,
    -- The room the machine was in AT THE MOMENT OF THE KILL, not the one it is
    -- in now. Moving a machine between rooms must not rewrite its history, and
    -- the room tab's query must not join through a column that has since
    -- changed.
    room_id     UUID          REFERENCES rooms(id)     ON DELETE SET NULL,
    process     TEXT NOT NULL,
    -- Why this was killed, which is the question the log exists to answer.
    -- 'locked' is written by the server, never sent by an agent: an agent
    -- cannot tell a locked machine from a whitelist that allows nothing, since
    -- both arrive on the wire as "whitelist, empty list".
    -- 'overflow' marks entries the agent had to drop; see agent/blocklog.go.
    reason      TEXT NOT NULL CHECK (reason IN ('blacklist', 'whitelist', 'locked', 'overflow')),
    count       INTEGER NOT NULL DEFAULT 1 CHECK (count > 0),
    first_at    TIMESTAMPTZ NOT NULL,
    last_at     TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_process_events_account_time  ON process_events(account_id, created_at DESC);
CREATE INDEX idx_process_events_room_time     ON process_events(room_id, created_at DESC);
CREATE INDEX idx_process_events_computer_time ON process_events(computer_id, created_at DESC);

ALTER TABLE process_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY process_events_isolation ON process_events
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- The last batch this machine's agent shipped. A sync whose response is lost
-- is retried, and the server has already committed the batch; without this
-- stamp the log double-counts, and a log that inflates its own numbers is
-- worse than no log. TEXT rather than UUID because the agent module depends on
-- golang.org/x/sys and nothing else, and it is opaque to the server anyway.
ALTER TABLE computers ADD COLUMN last_event_batch TEXT;

-- +goose Down
ALTER TABLE computers DROP COLUMN last_event_batch;
DROP TABLE process_events;
