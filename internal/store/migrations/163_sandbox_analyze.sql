-- After a bulk change of the sandbox tables (a copy writes hundreds of
-- thousands of base rows), the planner needs fresh statistics or a revert
-- can pick a plan that takes minutes. Only the tables' owner may analyze
-- them, so this function runs as the owner and touches nothing else.
CREATE FUNCTION apexx_analyze_sandbox() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    ANALYZE sandbox_base;
    ANALYZE sandbox_publish_rows;
END $$;
REVOKE ALL ON FUNCTION apexx_analyze_sandbox() FROM PUBLIC;
