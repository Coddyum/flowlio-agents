-- Toute lecture et toute écriture porte team_id ET project_id : le scope est DANS la query.
-- Une tâche d'un autre projet est introuvable, pas seulement interdite — et il n'existe aucune
-- query de tâche sans scope, donc aucun appelant ne peut en oublier un.

-- name: CreateTask :one
INSERT INTO tasks (team_id, project_id, number, title, body_md, status, priority, deadline)
VALUES (@team_id, @project_id, @number, @title, @body_md, @status, @priority, sqlc.narg('deadline'))
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks
WHERE team_id = @team_id AND project_id = @project_id AND number = @number;

-- ListTasks sert le backlog du projet courant. Les archivées sont exclues par défaut : un agent
-- qui demande son travail en cours ne doit pas payer en tokens l'historique du repo.
-- name: ListTasks :many
SELECT * FROM tasks
WHERE team_id = @team_id
  AND project_id = @project_id
  AND (@include_archived::boolean OR archived_at IS NULL)
  AND (sqlc.narg('status')::task_status IS NULL OR status = sqlc.narg('status')::task_status)
ORDER BY number DESC
LIMIT @max_rows::int;

-- UpdateTask est un patch : un champ absent (NULL) laisse la valeur en place.
-- `clear_deadline` existe parce que NULL signifie déjà « ne change pas » — sans ce drapeau, il
-- serait impossible d'effacer une échéance.
-- Une tâche archivée n'est pas modifiable : la clause archived_at IS NULL la rend introuvable
-- pour cette query, ce qui remonte le même ErrNotFound qu'un numéro inexistant.
-- name: UpdateTask :one
UPDATE tasks
SET title      = COALESCE(sqlc.narg('title'), title),
    body_md    = COALESCE(sqlc.narg('body_md'), body_md),
    status     = COALESCE(sqlc.narg('status')::task_status, status),
    priority   = COALESCE(sqlc.narg('priority')::task_priority, priority),
    deadline   = CASE
                     WHEN @clear_deadline::boolean THEN NULL
                     ELSE COALESCE(sqlc.narg('deadline'), deadline)
                 END,
    updated_at = now()
WHERE team_id = @team_id
  AND project_id = @project_id
  AND number = @number
  AND archived_at IS NULL
RETURNING *;

-- Rejouer l'archivage remonte ErrNotFound : la query ne cible que les tâches encore actives.
-- name: ArchiveTask :one
UPDATE tasks
SET archived_at = now(),
    updated_at  = now()
WHERE team_id = @team_id
  AND project_id = @project_id
  AND number = @number
  AND archived_at IS NULL
RETURNING *;

-- L'insertion d'une note passe par un SELECT scopé sur la tâche : impossible d'écrire dans le
-- fil d'une tâche d'un autre projet, même en connaissant son identifiant.
-- name: CreateTaskNote :one
INSERT INTO task_notes (task_id, body_md)
SELECT t.id, @body_md
FROM tasks t
WHERE t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.number = @number
  AND t.archived_at IS NULL
RETURNING *;

-- name: ListTaskNotes :many
SELECT n.* FROM task_notes n
JOIN tasks t ON t.id = n.task_id
WHERE t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.number = @number
ORDER BY n.created_at, n.id;
