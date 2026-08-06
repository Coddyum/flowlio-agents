-- name: CreateProject :one
INSERT INTO projects (team_id, key, name)
VALUES ($1, $2, $3)
RETURNING *;

-- Every read is scoped by team_id: the scope belongs to the query, not to the handler.

-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1 AND team_id = $2;

-- name: GetProjectByKey :one
SELECT * FROM projects WHERE team_id = $1 AND key = $2;

-- name: ListProjectsByTeam :many
SELECT * FROM projects WHERE team_id = $1 ORDER BY key;

-- ClaimNextNumber reserves the project's next readable identifier (FRNT-34).
-- The UPDATE ... RETURNING serialises concurrent calls on the project row, and rolls back with its
-- transaction: no gap is ever left in the numbering.
--
-- LOCKING CONSTRAINT — never add the write of a key column here (nor of a column covered by a
-- unique index). For as long as the UPDATE only touches non-key columns, Postgres takes a
-- FOR NO KEY UPDATE, which is compatible with the FOR KEY SHARE that an issue INSERT puts on BOTH
-- of its parent projects. The day that stops being true, two symmetrical agents (FRNT→CORE and
-- CORE→FRNT) deadlock.
--
-- `updated_at` is deliberately NOT touched: creating a task or an issue is not modifying the
-- project. Writing it here would turn projects.updated_at into a "date of the last object created".
-- name: ClaimNextNumber :one
UPDATE projects
SET next_number = next_number + 1
WHERE id = $1 AND team_id = $2
RETURNING (next_number - 1)::bigint AS claimed_number;

-- ChargeProjectNoteBytes debits the note thread's write quota, and REFUSES the debit that would
-- cross the bound. FLWL-70, part 5.
--
-- WHAT THIS QUERY OVERTURNS. `CreateTaskNote` carries, in so many words, the opposite decision:
-- "the WRITE is NOT bounded". It also named what would reopen the question — "hosted mode, where
-- storage is billed to a third party". That happened: D25 makes hosted THIS engine, co-deployed,
-- one shared installation across paying customers. An agent stuck in a loop no longer fills its
-- own database; it fills a host's, and the host pays for it.
--
-- The original argument stands and is not contradicted: "an agent writing a lot is an agent in
-- trouble, not an attacker". That is why the bound is HIGH — it does not arbitrate between a
-- verbose agent and a terse one, it stops a loop. A working agent never meets it.
--
-- THE PREDICATE IS IN THE QUERY, not in the service, and no other shape holds: read then compared
-- in Go, two concurrent notes read the same total and both get through. Here the read, the
-- comparison and the write are the SAME UPDATE, hence serialised by the row lock Postgres takes on
-- the project.
--
-- ZERO ROWS MEANS THE QUOTA, and nothing else: `project_id` comes from the authenticated token, so
-- the row necessarily exists. The service turns that zero into a refusal, never into "project not
-- found".
--
-- `note_bytes + @bytes` is repeated in the SET and in the WHERE. Postgres evaluates the WHERE
-- against the row BEFORE the update: without the repetition, the comparison would bear on the old
-- total and let through the very note that crosses the bound.
-- name: ChargeProjectNoteBytes :one
UPDATE projects
SET note_bytes = note_bytes + @bytes::bigint
WHERE id      = @project_id
  AND team_id = @team_id
  AND note_bytes + @bytes::bigint <= @quota::bigint
RETURNING note_bytes;
