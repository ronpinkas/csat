-- Human-friendly name for each survey set, surfaced in the multi-survey
-- dashboard + editor pickers. The JSON definition also carries it as `name`;
-- this column is the denormalized copy so listing surveys doesn't parse JSON.
ALTER TABLE survey_definitions ADD COLUMN name TEXT NOT NULL DEFAULT '';
