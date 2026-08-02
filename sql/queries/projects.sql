-- name: CreateProject :one
INSERT INTO projects (team_id, key, name)
VALUES ($1, $2, $3)
RETURNING *;

-- Toute lecture est scopée par team_id : le scope fait partie de la query, pas du handler.

-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1 AND team_id = $2;

-- name: GetProjectByKey :one
SELECT * FROM projects WHERE team_id = $1 AND key = $2;

-- name: ListProjectsByTeam :many
SELECT * FROM projects WHERE team_id = $1 ORDER BY key;

-- ClaimNextNumber réserve le prochain identifiant lisible du projet (FRNT-34).
-- Le UPDATE ... RETURNING sérialise les appels concurrents sur la ligne du projet.
-- name: ClaimNextNumber :one
UPDATE projects
SET next_number = next_number + 1,
    updated_at  = now()
WHERE id = $1 AND team_id = $2
RETURNING next_number - 1 AS claimed_number;
