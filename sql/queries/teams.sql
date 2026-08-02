-- name: CreateTeam :one
INSERT INTO teams (slug, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetTeamByID :one
SELECT * FROM teams WHERE id = $1;

-- name: GetTeamBySlug :one
SELECT * FROM teams WHERE slug = $1;

-- name: ListTeams :many
SELECT * FROM teams ORDER BY created_at;
