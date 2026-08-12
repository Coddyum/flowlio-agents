-- Restoring NOT NULL requires that no NULL remains: backfill any row an engine wrote without a notify
-- target to its concerned project before tightening the guard again.
UPDATE events SET notify_project_id = project_id WHERE notify_project_id IS NULL;

ALTER TABLE events ALTER COLUMN notify_project_id SET NOT NULL;
