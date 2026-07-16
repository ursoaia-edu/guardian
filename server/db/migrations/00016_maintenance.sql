-- +goose Up
-- Retention for the activity feed.
--
-- The application role cannot do this itself: events carries an RLS policy
-- keyed on app.account_id, so a DELETE without a scope matches nothing, and a
-- purge that had to loop over every account would need to enumerate them —
-- which is exactly the fleet-wide read migration 00014 removed. A SECURITY
-- DEFINER function is the same narrow escape hatch account_for_agent_token
-- uses: it runs as the table owner, but the only thing it can do is delete
-- rows older than the interval it is given, for everyone, returning a count.
-- It cannot read a single row out.
--
-- search_path is pinned because a SECURITY DEFINER function that resolves
-- unqualified names through the caller's search_path is a privilege-escalation
-- primitive.
-- +goose StatementBegin
CREATE FUNCTION purge_old_events(older_than INTERVAL) RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    purged BIGINT;
BEGIN
    DELETE FROM public.events WHERE created_at < now() - older_than;
    GET DIAGNOSTICS purged = ROW_COUNT;
    RETURN purged;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION purge_old_events(INTERVAL) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION purge_old_events(INTERVAL) TO guardian_app;

-- +goose Down
DROP FUNCTION purge_old_events(INTERVAL);
