-- name: CreateProjectToken :one
INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
VALUES ('project', $1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateAdminToken :one
INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
VALUES ('admin', NULL, NULL, $1, $2, $3)
RETURNING *;

-- GetTokenByPrefix ne filtre ni sur revoked_at ni sur le secret : le service vérifie les deux
-- en temps constant, pour que « préfixe inconnu », « secret faux » et « token révoqué » soient
-- indiscernables de l'extérieur.
-- name: GetTokenByPrefix :one
SELECT * FROM tokens WHERE prefix = $1;

-- name: ListProjectTokens :many
SELECT * FROM tokens
WHERE scope = 'project' AND team_id = $1 AND project_id = $2
ORDER BY created_at;

-- name: CountTokens :one
SELECT count(*) FROM tokens;

-- name: TouchToken :exec
UPDATE tokens SET last_used_at = now() WHERE id = $1;

-- name: RevokeProjectToken :one
UPDATE tokens
SET revoked_at = now()
WHERE id = $1 AND team_id = $2 AND scope = 'project' AND revoked_at IS NULL
RETURNING *;
