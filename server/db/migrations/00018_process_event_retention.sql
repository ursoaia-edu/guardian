-- +goose Up
-- Retention for the blocking log. Same escape hatch, same reasoning as
-- purge_old_events in 00016: process_events carries an RLS policy keyed on
-- app.account_id, so a DELETE without a scope matches nothing, and a purge
-- that looped over every account would need to enumerate them. SECURITY
-- DEFINER runs as the table owner, and the only thing this function can do is
-- delete rows older than the interval it is given, returning a count. It
-- cannot read a single row out.
--
-- search_path is pinned because a SECURITY DEFINER function that resolves
-- unqualified names through the caller's search_path is a privilege-escalation
-- primitive.
-- +goose StatementBegin
CREATE FUNCTION purge_old_process_events(older_than INTERVAL) RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    purged BIGINT;
BEGIN
    DELETE FROM public.process_events WHERE created_at < now() - older_than;
    GET DIAGNOSTICS purged = ROW_COUNT;
    RETURN purged;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION purge_old_process_events(INTERVAL) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION purge_old_process_events(INTERVAL) TO guardian_app;

-- +goose Down
DROP FUNCTION purge_old_process_events(INTERVAL);
