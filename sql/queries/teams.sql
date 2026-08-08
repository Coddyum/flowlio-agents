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

-- DeleteTeam removes a team and, by cascade, EVERYTHING that belongs to it. Card FLWL2-20.
--
-- NO GUARD, AND THAT IS THE DIFFERENCE WITH DeleteProject. Deleting one repo is refused while a
-- SIBLING repo holds a thread with it, because the sibling survives the deletion and would lose its
-- own words from its own side. Here nothing survives: `issues` carries a `team_id` and both of its
-- foreign keys are composite against `projects (id, team_id)`, so the two ends of every thread are
-- inside this team and go with it. There is no party left to be surprised, so there is nobody to
-- protect and no refusal to write. The customer deleting a project is deleting all of it, and the
-- screen that asks them is where the second thought belongs.
--
-- WHAT ACTUALLY GOES. `projects` and `tokens` reference `teams (id) ON DELETE CASCADE` — they are
-- the only two tables that name a team directly. Everything else hangs off `projects`, also in
-- cascade: tasks and their notes and their dependencies, issues and their messages, events,
-- memories, the trust edges, and the cursors that hang off the tokens. One statement, one
-- transaction: there is no window in which half a team exists.
--
-- RETURNING id IS NOT DECORATION. Without it a delete that matched nothing is indistinguishable
-- from one that removed a team, and the caller would answer 204 on a team that never existed. The
-- store turns "no row" into ErrNotFound.
-- name: DeleteTeam :one
DELETE FROM teams WHERE id = $1 RETURNING id;
