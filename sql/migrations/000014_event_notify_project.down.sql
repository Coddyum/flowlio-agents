DROP INDEX IF EXISTS events_notify_idx;

ALTER TABLE events DROP CONSTRAINT IF EXISTS events_notify_fk;

ALTER TABLE events DROP COLUMN IF EXISTS notify_project_id;
