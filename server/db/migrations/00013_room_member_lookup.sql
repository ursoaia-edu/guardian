-- +goose Up
-- A guest's account is discovered from their room grants before any account
-- scope exists, exactly as account_members is read in migration 00003. The
-- query is keyed on a user id the session already proved.
CREATE POLICY room_members_prescope ON room_members
    FOR SELECT USING (current_account_id() IS NULL);

-- +goose Down
DROP POLICY room_members_prescope ON room_members;
