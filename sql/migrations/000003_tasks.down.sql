DROP TABLE IF EXISTS task_notes;
DROP TABLE IF EXISTS tasks;

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_id_team_unique;

DROP TYPE IF EXISTS task_priority;
DROP TYPE IF EXISTS task_status;
