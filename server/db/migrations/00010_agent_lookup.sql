-- +goose Up
-- The narrow alternative to an unscoped SELECT policy on computers. SECURITY
-- DEFINER runs this as the table owner, who is not subject to RLS, but the
-- function returns a single uuid and nothing else — so the only thing reachable
-- without an account scope is the answer to "whose token is this?". Everything
-- the agent handler goes on to read happens inside inAccount like any other
-- query.
--
-- search_path is pinned because a SECURITY DEFINER function that resolves
-- unqualified names through the caller's search_path is a privilege-escalation
-- primitive.
CREATE FUNCTION account_for_agent_token(hash TEXT) RETURNS UUID
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT c.account_id FROM public.computers c WHERE c.token_hash = hash
$$;

REVOKE ALL ON FUNCTION account_for_agent_token(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION account_for_agent_token(TEXT) TO guardian_app;

-- +goose Down
DROP FUNCTION account_for_agent_token(TEXT);
