-- Saved-but-not-submitted responses ("drafts").
--
-- When a survey has allow_save enabled, a respondent can save progress before
-- finishing. That writes a normal response row flagged incomplete = 1, and does
-- NOT consume the link's one-time token (see used_tokens) — so the same link can
-- be reopened, on any device, and resumed from the server copy.
--
-- On final submit the SAME row is updated in place: its answers are replaced,
-- incomplete flips to 0, and only then is the used_tokens marker written. That
-- keeps exactly one response row per link, so saving never double-counts.
--
-- incomplete means "saved, not submitted" — a fully answered survey the
-- respondent chose to save (rather than submit) is still incomplete. Existing
-- rows predate the feature and are complete (the DEFAULT 0 backfills them).
ALTER TABLE responses ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0;

-- Look up a link's open draft on resume: (subject, subject_time) -> draft row.
CREATE INDEX IF NOT EXISTS idx_responses_draft
  ON responses(subject, subject_time, incomplete);

-- Analytics exclude drafts by default, so scope scans by set + flag.
CREATE INDEX IF NOT EXISTS idx_responses_incomplete
  ON responses(definition_id, incomplete);
