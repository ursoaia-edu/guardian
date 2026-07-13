-- +goose Up
-- Migration 00014 narrowed the pre-scope read on accounts to the row's owner,
-- which is all registration needed. The cabinet needs one thing more: an
-- account switcher has to show a NAME for every account the user can act in,
-- including the ones they are an admin or a room guest of — and those are read
-- before any account scope exists, in the same query that lists them.
--
-- So the predicate widens from "the account I own" to "an account I can act
-- in", which is the same set ListAccessibleAccounts already returns. It stays
-- keyed on current_user_id(): a connection with no user scope still reads
-- nothing, and no user can see an account they have no claim on.
--
-- The subqueries are themselves RLS-filtered to this user by the two
-- membership policies, so they cannot widen the result either. Neither
-- referenced table has a policy that reads accounts, so there is no recursion.
DROP POLICY accounts_prescope ON accounts;
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (
        current_account_id() IS NULL
        AND current_user_id() IS NOT NULL
        AND (
            owner_user_id = current_user_id()
            OR id IN (SELECT account_id FROM account_members WHERE user_id = current_user_id())
            OR id IN (SELECT account_id FROM room_members WHERE user_id = current_user_id())
        )
    );

-- +goose Down
DROP POLICY accounts_prescope ON accounts;
CREATE POLICY accounts_prescope ON accounts
    FOR SELECT USING (current_account_id() IS NULL AND owner_user_id = current_user_id());
