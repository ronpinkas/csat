-- 0002_indexes: indexes for date-range scans, per-question aggregates, and sweeps.

CREATE INDEX idx_responses_submitted_at ON responses(submitted_at);

CREATE INDEX idx_answers_response ON answers(response_id);
CREATE INDEX idx_answers_q_num    ON answers(question_key, num);
CREATE INDEX idx_answers_q_text   ON answers(question_key, text);

CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_invites_expires  ON invites(expires_at);
