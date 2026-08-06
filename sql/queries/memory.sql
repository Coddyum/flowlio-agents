-- memory — what a repository remembers about itself. M5 (FLWL-7).
--
-- SCOPE RULE OF THIS FILE: `team_id` AND `project_id`, on EVERY statement without exception. Same
-- rule as tasks.sql, issues.sql and ref.sql — the repository's first scope rule, the one that makes
-- another project's row UNFINDABLE rather than merely forbidden.
--
-- There is no OverviewMemories and there must never be one. The team-wide read that `overview.sql`
-- grants itself over tasks and issues has a justification memories do not share: a supervisor reads
-- a debt to act on it. A memory is a repository talking to its own future sessions, and opening it
-- to a third party turns a private note into a channel — which is exactly the design the card
-- dropped on 2026-08-05.
--
-- AN ENTRY IS NEVER UPDATED AND NEVER DELETED. It is SUPERSEDED, which is a write on the OLD row's
-- `superseded_by` and an insert of the new one. That is why no UpdateMemory exists here, and why
-- adding one would quietly remove the only thing this table offers over a markdown file: the
-- ability to answer "why was it like that" as well as "why is it like this".

-- CreateMemory inserts an entry.
--
-- The insert is fed by a SELECT on `projects`, exactly like CreateTaskNote is fed by one on
-- `tasks`: if the pair (project, team) does not exist, no row is produced, nothing is inserted, and
-- the caller gets ErrNotFound. The scope is carried by the statement, not by a prior check some
-- caller could forget.
-- name: CreateMemory :one
INSERT INTO memories (team_id, project_id, slug, kind, title, body_md)
SELECT p.team_id, p.id, @slug, @kind::memory_kind, @title, @body_md
FROM projects p
WHERE p.id = @project_id AND p.team_id = @team_id
RETURNING id, slug, kind, title, body_md, created_at, updated_at;

-- MemoryBySlug reads one entry of the caller's project.
-- name: MemoryBySlug :one
SELECT m.id, m.slug, m.kind, m.title, m.body_md, m.created_at, m.updated_at,
       -- Both ends of the supersession chain, resolved to SLUGS. An agent never handles a UUID,
       -- and "superseded" without saying BY WHAT would strip the field of the one thing it is for.
       --
       -- coalesce to the empty string, and the cast that goes with it: a bare subquery is NULLABLE,
       -- so sqlc 1.30 types the column `string` and every Scan on an entry still in force — that is,
       -- the nominal case — fails on the NULL. Same trap, same fix and same reason as
       -- OverviewLastSeen. Empty means "in force"; no valid slug can be confused with it, since
       -- memories_slug_shape demands at least one character.
       coalesce((SELECT n.slug FROM memories n WHERE n.id = m.superseded_by), '')::text AS superseded_by,
       -- What this entry replaced. The pointer lives on the OLD row, so reading it forward takes a
       -- correlated subquery, and it can name several entries: a rewrite commonly retires more than
       -- one.
       --
       -- string_agg AND NOT an array, and the reason is a hard constraint rather than taste: sqlc
       -- maps `text[]` onto `pq.StringArray`, which would pull github.com/lib/pq into a repository
       -- whose stack is pgx and whose rule is to add no dependency that was not asked for. The comma
       -- is a safe separator HERE and only here — `memories_slug_shape` restricts a slug to
       -- [A-Za-z0-9_-], so no slug can ever contain one. Loosen that CHECK and this splits wrong.
       coalesce((SELECT string_agg(o.slug, ',' ORDER BY o.slug)
                 FROM memories o WHERE o.superseded_by = m.id), '')::text AS supersedes
FROM memories m
WHERE m.team_id = @team_id AND m.project_id = @project_id AND m.slug = @slug;

-- ListMemories returns the entries still in force, most recent first.
--
-- `include_superseded` exists because the history is the point of the table, and it is a parameter
-- rather than a second query so the two readings can never drift apart. It defaults to false in the
-- service: a session picking a repository up wants what is TRUE, not what once was.
--
-- The `kind` filter is nullable: sqlc.narg, so "every kind" is expressible without a second query.
--
-- Bounded by @max_rows, like every list of this repository. An index of memories that grows without
-- limit fills the context of the agent that reads it on startup — which is the one call this
-- feature makes on every single session.
-- name: ListMemories :many
SELECT m.id, m.slug, m.kind, m.title, m.body_md, m.created_at, m.updated_at,
       -- Both ends of the supersession chain, resolved to SLUGS. An agent never handles a UUID,
       -- and "superseded" without saying BY WHAT would strip the field of the one thing it is for.
       --
       -- coalesce to the empty string, and the cast that goes with it: a bare subquery is NULLABLE,
       -- so sqlc 1.30 types the column `string` and every Scan on an entry still in force — that is,
       -- the nominal case — fails on the NULL. Same trap, same fix and same reason as
       -- OverviewLastSeen. Empty means "in force"; no valid slug can be confused with it, since
       -- memories_slug_shape demands at least one character.
       coalesce((SELECT n.slug FROM memories n WHERE n.id = m.superseded_by), '')::text AS superseded_by,
       -- What this entry replaced. The pointer lives on the OLD row, so reading it forward takes a
       -- correlated subquery, and it can name several entries: a rewrite commonly retires more than
       -- one.
       --
       -- string_agg AND NOT an array, and the reason is a hard constraint rather than taste: sqlc
       -- maps `text[]` onto `pq.StringArray`, which would pull github.com/lib/pq into a repository
       -- whose stack is pgx and whose rule is to add no dependency that was not asked for. The comma
       -- is a safe separator HERE and only here — `memories_slug_shape` restricts a slug to
       -- [A-Za-z0-9_-], so no slug can ever contain one. Loosen that CHECK and this splits wrong.
       coalesce((SELECT string_agg(o.slug, ',' ORDER BY o.slug)
                 FROM memories o WHERE o.superseded_by = m.id), '')::text AS supersedes,
       (count(*) OVER ())::bigint AS total
FROM memories m
WHERE m.team_id = @team_id
  AND m.project_id = @project_id
  AND (@include_superseded::boolean OR m.superseded_by IS NULL)
  AND (sqlc.narg('kind')::memory_kind IS NULL OR m.kind = sqlc.narg('kind')::memory_kind)
ORDER BY m.created_at DESC, m.slug
LIMIT @max_rows::int;

-- SearchMemories ranks the entries matching a query, best first. Postgres FTS, no embedding and no
-- model call: the "no AI in the product" rule holds.
--
-- websearch_to_tsquery and not plainto_tsquery: it accepts what a human or an agent actually types
-- — quoted phrases, OR, negation with a leading dash — and, crucially, it NEVER raises on malformed
-- input. `to_tsquery` would turn a stray parenthesis into a 500 on a search box.
--
-- ts_rank_cd over ts_rank: it accounts for the distance between the matched terms, so an entry
-- whose title carries both words outranks one that mentions them ten paragraphs apart. The weights
-- are the ones set by the generated column — title A, body B.
--
-- The scope predicates come FIRST and are not negotiable: the GIN index answers the match, the
-- project predicate answers who is allowed to see it, and only the second one is a security
-- boundary.
-- name: SearchMemories :many
SELECT m.id, m.slug, m.kind, m.title, m.body_md, m.created_at, m.updated_at,
       -- Both ends of the supersession chain, resolved to SLUGS. An agent never handles a UUID,
       -- and "superseded" without saying BY WHAT would strip the field of the one thing it is for.
       --
       -- coalesce to the empty string, and the cast that goes with it: a bare subquery is NULLABLE,
       -- so sqlc 1.30 types the column `string` and every Scan on an entry still in force — that is,
       -- the nominal case — fails on the NULL. Same trap, same fix and same reason as
       -- OverviewLastSeen. Empty means "in force"; no valid slug can be confused with it, since
       -- memories_slug_shape demands at least one character.
       coalesce((SELECT n.slug FROM memories n WHERE n.id = m.superseded_by), '')::text AS superseded_by,
       -- What this entry replaced. The pointer lives on the OLD row, so reading it forward takes a
       -- correlated subquery, and it can name several entries: a rewrite commonly retires more than
       -- one.
       --
       -- string_agg AND NOT an array, and the reason is a hard constraint rather than taste: sqlc
       -- maps `text[]` onto `pq.StringArray`, which would pull github.com/lib/pq into a repository
       -- whose stack is pgx and whose rule is to add no dependency that was not asked for. The comma
       -- is a safe separator HERE and only here — `memories_slug_shape` restricts a slug to
       -- [A-Za-z0-9_-], so no slug can ever contain one. Loosen that CHECK and this splits wrong.
       coalesce((SELECT string_agg(o.slug, ',' ORDER BY o.slug)
                 FROM memories o WHERE o.superseded_by = m.id), '')::text AS supersedes,
       ts_rank_cd(m.search, websearch_to_tsquery('english', @query)) AS rank,
       (count(*) OVER ())::bigint AS total
FROM memories m
WHERE m.team_id = @team_id
  AND m.project_id = @project_id
  AND (@include_superseded::boolean OR m.superseded_by IS NULL)
  AND (sqlc.narg('kind')::memory_kind IS NULL OR m.kind = sqlc.narg('kind')::memory_kind)
  AND m.search @@ websearch_to_tsquery('english', @query)
ORDER BY rank DESC, m.created_at DESC
LIMIT @max_rows::int;

-- SupersedeMemory marks an entry as replaced by another.
--
-- Both endpoints are scoped: `id` is the old entry, read under the caller's project, and
-- `superseded_by` is the new one — which the composite foreign key `memories_supersedes_fk` already
-- forces into the same project. Belt and braces, and deliberately: the key is a schema guarantee
-- and the predicate a query guarantee, and this repository's doctrine is that a defence resting on
-- a constraint written in another file is not a defence.
--
-- `superseded_by IS NULL` in the WHERE makes it a one-way move. Replaying it returns zero rows
-- rather than rewriting history: an entry has exactly one successor, and the second attempt is
-- either a mistake or a race.
-- name: SupersedeMemory :one
UPDATE memories
SET superseded_by = @superseded_by, updated_at = now()
WHERE team_id       = @team_id
  AND project_id    = @project_id
  AND id            = @id
  AND superseded_by IS NULL
RETURNING id, slug;

-- MemoryIndex is what the MCP handshake injects, and it is why the rest of this file is bounded.
--
-- Titles only, no bodies: it is paid once per session, in the agent's context, before its first
-- message. A body would turn the index into the whole memory and there would be nothing left to
-- read on demand.
--
-- Entries still in force only. An index listing superseded decisions would teach an agent things
-- that stopped being true, which is the exact failure this feature exists to prevent.
-- name: MemoryIndex :many
SELECT slug, kind, title
FROM memories
WHERE team_id = @team_id
  AND project_id = @project_id
  AND superseded_by IS NULL
ORDER BY kind, created_at DESC
LIMIT @max_rows::int;

-- ChargeProjectMemoryBytes debits the memory storage quota. Same shape, same reasons and same
-- single-UPDATE atomicity as ChargeProjectNoteBytes (projects.sql): read, compare and write in one
-- statement, so two concurrent entries cannot both read the same total and both get through.
--
-- A SEPARATE COUNTER from note_bytes, and not a shared one. The two surfaces have different
-- lifetimes and different bounds: a note thread is a journal that grows with every session, a
-- memory is a small durable index. Sharing a counter would let a chatty journal refuse the very
-- decision entry a session needs to write in order to explain itself.
-- name: ChargeProjectMemoryBytes :one
UPDATE projects
SET memory_bytes = memory_bytes + @bytes::bigint
WHERE id      = @project_id
  AND team_id = @team_id
  AND memory_bytes + @bytes::bigint <= @quota::bigint
RETURNING memory_bytes;
