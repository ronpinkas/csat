-- Explicit "active/default" survey. When set, blank links + the dashboard/form
-- default to this set instead of the implicit newest one. At most one row should
-- be 1 at a time (defstore.SetDefault enforces this). Nothing pinned == today's
-- behavior (newest is default).
ALTER TABLE survey_definitions ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
