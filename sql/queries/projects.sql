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
-- Le UPDATE ... RETURNING sérialise les appels concurrents sur la ligne du projet, et rollback
-- avec sa transaction : aucun trou dans la numérotation.
--
-- CONTRAINTE DE VERROUILLAGE — ne jamais ajouter ici l'écriture d'une colonne de clé (ni une
-- colonne couverte par un index unique). Tant que l'UPDATE ne touche que des colonnes non-clé,
-- Postgres prend un FOR NO KEY UPDATE, compatible avec le FOR KEY SHARE que l'INSERT d'issue
-- pose sur ses DEUX projets parents. Le jour où ce n'est plus vrai, deux agents symétriques
-- (FRNT→CORE et CORE→FRNT) s'interbloquent.
--
-- `updated_at` n'est volontairement PAS touché : créer une tâche ou une issue n'est pas modifier
-- le projet. L'y écrire ferait de projects.updated_at une « date du dernier objet créé ».
-- name: ClaimNextNumber :one
UPDATE projects
SET next_number = next_number + 1
WHERE id = $1 AND team_id = $2
RETURNING (next_number - 1)::bigint AS claimed_number;
