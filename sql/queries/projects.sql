-- CreateProject inserts the repo AND opens its trust edges towards every repo already in the team,
-- in ONE statement.
--
-- WHY THE EDGES ARE WRITTEN AND NOT IMPLIED. "All the repos of a team trust each other" is the
-- default, and a default applied as an implicit rule is a graph that says one thing while the
-- product shows another: flowlio.me draws the canvas FROM THIS TABLE, so an unwritten default would
-- render as "no link" over a channel that is in fact open. The property the product owes is
-- "no edge, no trust — always, without exception", and it only holds if the edges exist as rows.
-- The customer then cuts what they do not want, with `flowlio trust deny`.
--
-- THIS DOES NOT REOPEN THE BACKFILL 000007 REFUSED. That migration declined to write a full mesh
-- over the repos that ALREADY existed, because such a graph would have been true for zero seconds
-- and nobody prunes a graph that never lied. Here nothing is rewritten after the fact: the edges are
-- born with the repo, at the instant the customer states its existence, and they describe the state
-- they create.
--
-- WHY ONE STATEMENT AND NOT TWO. The workspace store has no Transactor, so two calls would be two
-- transactions: a crash between them leaves a repo that exists and can talk to nobody — the exact
-- failure card 12 was opened for, made intermittent instead of systematic. `linked` is a
-- data-modifying CTE, so it commits or rolls back with the project itself. Same shape as CreateIssue
-- (issues.sql), for the same reason.
--
-- `linked` IS NEVER READ by the main query, and that is not an oversight: Postgres executes a
-- data-modifying CTE exactly once and to completion whether or not anything references it. Verified
-- on a throwaway base rather than assumed — the statement below inserted 4 edges for a third repo
-- while the main query selected from `created` alone.
--
-- NO SELF-EDGE IS EVER ATTEMPTED. Every CTE of a statement shares the snapshot taken before that
-- statement, so the scan of `projects` in `linked` does NOT see the row `created` is inserting:
-- p.id is never c.id, hence the two arrows below never have the same end twice. Verified the same
-- way — inside the statement the scan counted 2 projects where the table held 3 afterwards. This one
-- is load-bearing: were the row visible, the edge would be (x, x), project_trust_not_self would
-- raise 23514 and the WHOLE creation would fail. No guard is added on top, because a guard here
-- would hide the day that assumption breaks instead of making it loud.
--
-- The composite FKs resolve against the project inserted in the same statement: they are checked at
-- the end of the statement, by which point the row exists.
--
-- NO `ON CONFLICT`, deliberately. A duplicate is unreachable: `created` yields one row, `projects`
-- yields distinct ids, and the new id is fresh so no edge can already name it — in either direction.
-- Two CONCURRENT creations in one team do not conflict either — neither statement sees the other's
-- project, so NEITHER arrow between the two newcomers is written. That window fails CLOSED, which is
-- the only direction an allow-list may fail; `flowlio trust allow` reopens it in one command per
-- direction.
--
-- TWO ARROWS PER PEER, since 000013 made the edge DIRECTED. The default the product owes is "the
-- repos of a team may question each other", and under a directed table that sentence is two rows:
-- the newcomer may open a question at the peer, and the peer at the newcomer. Writing only one of
-- them would ship a default nobody chose — half the repos of every team silently unable to raise an
-- issue, and the refusal indistinguishable from a repo that does not exist.
--
-- The LATERAL VALUES emits both directions from ONE scan and ONE reference to `created`. A UNION ALL
-- of two SELECTs would read the same peers twice and, worse, leave the two directions as two pieces
-- of text that can drift apart: this shape cannot write one arrow without writing the other.
-- name: CreateProject :one
WITH created AS (
    INSERT INTO projects (team_id, key, name)
    VALUES ($1, $2, $3)
    RETURNING *
),
linked AS (
    INSERT INTO project_trust (team_id, from_project_id, to_project_id)
    SELECT c.team_id, e.from_id, e.to_id
    FROM created c
    JOIN projects p ON p.team_id = c.team_id
    CROSS JOIN LATERAL (VALUES (c.id, p.id), (p.id, c.id)) AS e (from_id, to_id)
    RETURNING 1
)
SELECT * FROM created;

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
