-- +goose Up
-- current_setting(..., true) returns NULL rather than erroring when the GUC is
-- unset, so a connection that forgot to scope itself sees nothing at all.
CREATE FUNCTION current_account_id() RETURNS UUID
LANGUAGE sql STABLE AS $$
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
-- accounts they may enter. Both are keyed on a user id the caller already
-- proved. Postgres ORs permissive policies, so an unscoped connection reads
-- these two tables freely while every scoped one stays confined by the
-- isolation policies above. This exception applies to these two tables ONLY —
-- nothing else in the schema is ever readable without a scope.
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (current_account_id() IS NULL);
CREATE POLICY accounts_insert ON accounts
    FOR INSERT WITH CHECK (current_account_id() IS NULL);
CREATE POLICY account_members_prescope ON account_members
    FOR SELECT USING (current_account_id() IS NULL);
CREATE POLICY account_members_insert ON account_members
    FOR INSERT WITH CHECK (current_account_id() IS NULL);

-- +goose Down
DROP POLICY account_members_insert ON account_members;
DROP POLICY account_members_prescope ON account_members;
DROP POLICY accounts_insert ON accounts;
DROP POLICY accounts_prescope ON accounts;
DROP POLICY account_members_isolation ON account_members;
ALTER TABLE account_members DISABLE ROW LEVEL SECURITY;
DROP POLICY accounts_isolation ON accounts;
ALTER TABLE accounts DISABLE ROW LEVEL SECURITY;
DROP FUNCTION current_account_id();
