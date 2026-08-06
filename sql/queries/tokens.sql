-- name: CreateProjectToken :one
INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
VALUES ('project', $1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateAdminToken :one
INSERT INTO tokens (scope, team_id, project_id, name, prefix, secret_hash)
VALUES ('admin', NULL, NULL, $1, $2, $3)
RETURNING *;

-- GetTokenByPrefix filters neither on revoked_at nor on the secret: the service checks both in
-- constant time, so that "unknown prefix", "wrong secret" and "revoked token" are indistinguishable
-- from the outside.
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

-- RevokeAdminTokens revokes EVERY live administration token at once, which is the first half of a
-- rotation.
--
-- Revoking is what makes it a rotation rather than a second key cut for the same lock: the token
-- being replaced is, by hypothesis, the one that was lost — leaving it live would leave on the
-- installation a credential nobody controls any more.
--
-- No identifier is named, and none could be: the server only ever kept a hash, so whoever runs the
-- rotation has no way to designate the token they are replacing. That is the whole reason this
-- query exists.
-- name: RevokeAdminTokens :execrows
UPDATE tokens
SET revoked_at = now()
WHERE scope = 'admin' AND revoked_at IS NULL;
