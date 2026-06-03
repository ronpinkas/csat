-- 0002_indexes: indexes for date-range scans, aggregates, and housekeeping sweeps.

CREATE INDEX idx_responses_submitted_at ON responses(submitted_at);
CREATE INDEX idx_responses_sub_metrics  ON responses(submitted_at, csat, ces, resolution);
CREATE INDEX idx_sessions_expires       ON sessions(expires_at);
CREATE INDEX idx_invites_expires        ON invites(expires_at);
