-- Versioned survey definitions ("question sets"), edited through the admin UI
-- (no filesystem access needed). Every set is permanent and usable: a survey
-- link may name a specific set id, and blank links default to the latest set.
-- Editing publishes a new set; older sets remain mintable and viewable.
CREATE TABLE IF NOT EXISTS survey_definitions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  json       TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);

-- Each response records which set it was answered under, so analytics stay
-- coherent across edits (a response is forever interpreted by the questions its
-- respondent actually saw). NULL on pre-existing rows until the seed step
-- backfills them to the first set.
ALTER TABLE responses ADD COLUMN definition_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_responses_definition ON responses(definition_id);
