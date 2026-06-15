-- +goose Up
-- current_setting(..., true) returns NULL rather than erroring when the GUC is
-- unset, so a connection that forgot to scope itself sees nothing at all.
-- search_path is pinned so this function cannot be redirected by a session
-- that has changed it; it sits inside every RLS predicate on these tables.
CREATE FUNCTION current_account_id() RETURNS UUID
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
    SELECT NULLIF(current_setting('app.account_id', true), '')::uuid
$$;

ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY accounts_isolation ON accounts
    USING (id = current_account_id())
    WITH CHECK (id = current_account_id());

ALTER TABLE account_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY account_members_isolation ON account_members
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());

-- Two operations legitimately happen before any account scope exists:
-- registration creates the very first account, and a signed-in user asks which
-- accounts they may enter. Postgres ORs permissive policies, so the three
-- policies below are a deliberate hole in the isolation above, on these two
-- tables. (Migration 00013 adds room_members for the same reason: a guest's
-- account is discovered from their room grants before any scope exists. If you
-- are reading this to decide whether a table may join them, the answer is no
-- unless authentication itself cannot proceed without it.) Be precise about how
-- big the hole is: they key on nothing. Any
-- connection that has not called inAccount() can read every row of both tables
-- and insert any account row. That is why the ONLY unscoped statements the
-- server is permitted to run against these tables are the ones in the
-- registration handler and in SessionAuth, and why both filter by a user id the
-- caller has already proved. Nothing else in the schema is readable without a
-- scope at all.
--
-- There is deliberately NO unscoped INSERT policy on account_members: an
-- unscoped membership insert would let any such code path make an arbitrary
-- user the owner of an arbitrary account, which is privilege escalation
-- written into the schema. Registration sets the account scope the moment it
-- has the new account's id and inserts the owner row scoped.
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (current_account_id() IS NULL);
CREATE POLICY accounts_insert ON accounts
    FOR INSERT WITH CHECK (current_account_id() IS NULL);
CREATE POLICY account_members_prescope ON account_members
    FOR SELECT USING (current_account_id() IS NULL);

-- +goose Down
DROP POLICY account_members_prescope ON account_members;
DROP POLICY accounts_insert ON accounts;
DROP POLICY accounts_prescope ON accounts;
DROP POLICY account_members_isolation ON account_members;
ALTER TABLE account_members DISABLE ROW LEVEL SECURITY;
DROP POLICY accounts_isolation ON accounts;
ALTER TABLE accounts DISABLE ROW LEVEL SECURITY;
DROP FUNCTION current_account_id();
