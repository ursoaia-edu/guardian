-- +goose Up
-- Closes the composed pre-scope exposure documented in specs/server.md.
--
-- Each pre-scope policy was individually justified against the table it sits
-- on: registration must create the first account before a scope exists, and a
-- session must discover which accounts it may enter before one can be set. But
-- they keyed on NOTHING. Composed, an unscoped connection could read every
-- customer's account name, membership and room-sharing graph across the whole
-- fleet in a single join — not one account's worth, all of them.
--
-- The fix is a second GUC, app.user_id, set exactly where app.account_id
-- cannot be yet: it narrows each pre-scope hole to the calling user's own
-- rows. A handler that reads one of these tables and forgets to scope itself
-- now returns nothing, which is how every other table in this schema already
-- behaves.
CREATE FUNCTION current_user_id() RETURNS UUID
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
    SELECT NULLIF(current_setting('app.user_id', true), '')::uuid
$$;

-- An account is readable before a scope exists only by the user who owns it,
-- which is exactly what registration's INSERT ... RETURNING needs and nothing
-- more. Every other read of this table happens inside inAccount and is
-- governed by accounts_isolation.
DROP POLICY accounts_prescope ON accounts;
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (current_account_id() IS NULL AND owner_user_id = current_user_id());

-- The insert is narrowed for the same reason it exists at all: registration is
-- the one caller, and it creates an account for the user it just created. With
-- this, no code path can create an account owned by somebody else even by
-- accident.
DROP POLICY accounts_insert ON accounts;
CREATE POLICY accounts_insert ON accounts
    FOR INSERT WITH CHECK (current_account_id() IS NULL AND owner_user_id = current_user_id());

-- Both membership tables answer one question before a scope exists: "which
-- accounts may this user enter?" (ListAccessibleAccounts). That question is
-- always asked about a user id the session has already proved, so the policy
-- can say so.
DROP POLICY account_members_prescope ON account_members;
CREATE POLICY account_members_prescope ON account_members
    FOR SELECT USING (current_account_id() IS NULL AND user_id = current_user_id());

DROP POLICY room_members_prescope ON room_members;
CREATE POLICY room_members_prescope ON room_members
    FOR SELECT USING (current_account_id() IS NULL AND user_id = current_user_id());

-- Deliberately NOT narrowed, and both are still documented in specs/server.md:
--
--   binding_tokens_lookup — enrollment is keyed by a token digest and has no
--   user at all. The rows it exposes are SHA-256 digests of 32 random bytes,
--   which cannot be used without the plaintext.
--
--   users — has no RLS, because it is not a multi-tenant table and three
--   legitimate paths read a row that is not the caller's own: login (before
--   any user id is known), inviting somebody to a room by email, and listing
--   a room's members. Narrowing it needs SECURITY DEFINER lookups for those
--   three, which is a separate change.

-- +goose Down
DROP POLICY room_members_prescope ON room_members;
CREATE POLICY room_members_prescope ON room_members
    FOR SELECT USING (current_account_id() IS NULL);
DROP POLICY account_members_prescope ON account_members;
CREATE POLICY account_members_prescope ON account_members
    FOR SELECT USING (current_account_id() IS NULL);
DROP POLICY accounts_insert ON accounts;
CREATE POLICY accounts_insert ON accounts
    FOR INSERT WITH CHECK (current_account_id() IS NULL);
DROP POLICY accounts_prescope ON accounts;
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (current_account_id() IS NULL);
DROP FUNCTION current_user_id();
