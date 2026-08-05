DROP TABLE IF EXISTS task_dependencies;

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_id_project_unique;
