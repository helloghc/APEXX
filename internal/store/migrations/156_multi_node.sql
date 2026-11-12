-- Several app nodes behind the load balancer (rolling deploys, autoscaling).
--
-- job_leases: each background job runs on one node at a time. The node that
-- holds a lease renews it on every run and gives it back on shutdown, so a
-- deploy hands the work over at once; a node that dies loses it when it
-- expires.
CREATE TABLE job_leases (
    name        text PRIMARY KEY,
    holder      text NOT NULL,
    expires_at  timestamptz NOT NULL
);

-- login_failures: failed sign-in attempts per address and email (hashed),
-- shared by every node, so the limit is the same whichever node answers.
CREATE TABLE login_failures (
    key_hash    bytea NOT NULL,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_key ON login_failures (key_hash, at);
CREATE INDEX login_failures_at ON login_failures (at);
