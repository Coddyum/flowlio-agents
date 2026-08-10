# DESIGN M3 — cross-project issues + event log

> Design note produced on 2026-08-02 by a fan-out of agents (five independent angles, two
> adversarial critiques, one synthesis), **before** any code was written. It complements
> `DESIGN-V1.md`, which remains v1's scope contract.
>
> Status: decisions **applied** by M3's implementation. This document is the reference for
> understanding WHY the model has this shape. Any gap found between this document and the code is
> fixed in the code, or documented here with its reason.


> Real state of the repository at the time of this note (verified): **M1 and M2 are committed**
> (`abae3e2`, `5021f58`). `internal/feature/task/**` ships with its `Transactor` (`store/tx.go`),
> `ClaimNextNumber` already returns an `int64` (the `::bigint` cast is present in
> `sql/queries/projects.sql`), `projects_id_team_unique UNIQUE (id, team_id)` has existed since
> `000003`, `requireProjectScope` is a local middleware of `task/module.go`, and **the MCP server
> already exists** (`cmd/flowlio/mcp.go`, `mcp_tools.go`, `mcp_call.go` — JSON-RPC 2.0 written by
> hand, zero dependencies, 6 tools). Any recommendation assuming otherwise is void and does not
> appear below.

---

## Settled decisions

| # | Decision | Consequence |
| - | -------- | ----------- |
| 1 | **`check_inbox` does NOT return an event stream.** It returns the **current actionable state** in three buckets: `needs_answer` (incoming `open` issues), `answered` (my outgoing issues that turned `answered`), `in_progress` (my `in_progress` tasks). | No notification can be lost: the state is recomputed on every call. A second call returns the same buckets — that is a property, not a defect: it prevents the false conclusion "nothing to do" after a context compaction. |
| 2 | **The cursor drives the `new` flag and nothing else.** It never conditions the presence of a row in a bucket. | The whole delivery-reliability machinery (`xid8`, `pg_snapshot_xmin` watermark, composite cursor, audience fan-out, `actor_token_id`, `DrainInbox` in one statement) is **removed**. A missed event costs a boolean, never an invisible issue. |
| 3 | **No `xid8`, no watermark, no `SELECT ... FOR UPDATE` on the cursor, no transaction in `inbox`.** | `models.go` stays free of `interface{}`. `check_inbox` = 5 independent queries, no serialisation, no `idle_in_transaction_session_timeout` to set. The `bigserial` sequence gap still exists and is **accepted and documented**: it degrades a `new: true` into a `new: false`, nothing else. |
| 4 | **`token_cursors.last_event_id` starts at `0`, with no seeding.** | Rotating a token (`RevokeProjectToken` + `CreateProjectToken`) loses nothing and replays nothing: the buckets are capped at 10, everything is simply marked `new`. The "seeding at the current watermark loses the unread issues" problem disappears by construction. |
| 5 | **`events` is written in the SAME transaction as the issue and its message, by `issue`'s store directly (`s.q.AppendEvent`).** No `internal/store/eventlog` port in M3. | `internal/store/` stays empty. One single event writer in M3: the DRY rule ("more than twice") does not fire. The port will be created when `task` starts emitting too (v2 / SSE). |
| 6 | **M3 emits events for issues only** (`issue.opened`, `issue.answered`, `issue.reopened`, `issue.closed`). `task` is not reopened. | No bucket depends on a task event (`in_progress` is derived from `tasks.status`). Emitting `task.*` events nobody reads would be dead weight. A backlog task for v2. |
| 7 | **`issues.project_id` = the recipient (owner of the issue and of the number), `issues.author_project_id` = the sender.** The number is drawn from the **recipient's** counter. | `CORE-41` opened by FRNT carries CORE's key. GitHub semantics, self-describing key. |
| 8 | **A canonical, literal visibility clause, repeated in EVERY issue query:** `i.team_id = @team_id AND (i.project_id = @project_id OR i.author_project_id = @project_id)`, with `@project_id` coming **exclusively** from `Principal.ProjectID`. | Never a service `if`. Never a `role` used as authorisation: `role` is an **additional restriction** laid on top of the complete clause. |
| 9 | **Every write carries its scope.** No issue query takes a bare `id`. The message and the state transition are **a single statement** (a modifying CTE). | No TOCTOU: impossible to write a message into an issue closed in the meantime, impossible to have a message without its transition. |
| 10 | **`closed` is terminal. BOTH participants may close.** The state is never a parameter: it is **computed in SQL** from the caller's role. | A message from the recipient → `answered`; a message from the author → `open` (chasing puts the recipient back in debt); `close=true` → `closed`. An agent cannot lie about the state it produces. Reopening = a new issue. Direct consequence: **no 403 on an issue key**, therefore no "UPDATE then re-read to choose between 403 and 404" path. |
| 11 | **`answer_issue` refuses an already-closed issue** (`AND i.state <> 'closed'` in the query), and `closed_at` is **never** overwritten (`CASE WHEN @close THEN now() ELSE i.closed_at END`). | Fixes the two bugs of the initial sketch: the silent resurrection of a closed issue, and the erasure of the closing date on every message. |
| 12 | **An issue's title is immutable.** No `update_issue`. | The recipient cannot requalify the request; the author cannot invalidate the answer after the fact. `issues.updated_at` only moves on a message or a transition — so it is an honest inbox sort. |
| 13 | **An issue to one's own project is refused**, by `CHECK (author_project_id <> project_id)` **and** by a service validation returning an explicit `400` ("a question to yourself is a task: use create_task"). | The `CHECK` alone would surface `23514` → `ErrConflict` → `409`, a misleading code. The CHECK stays as the net for any future write. It makes `incoming`/`outgoing` genuinely disjoint and the transition `CASE` total. |
| 14 | **COMPOSITE foreign keys `(project_id, team_id) REFERENCES projects (id, team_id)`** on `issues` (both project columns) and on `events`. | Reuses `000003`'s `tasks_project_fk` pattern identically. A cross-team issue is not filtered: it is **impossible to insert**. **No new `UNIQUE` constraint to create** — `projects_id_team_unique` already exists, and Postgres matches the set of columns, not their order. |
| 15 | **`ON DELETE CASCADE` on both of `issues`'s project FKs.** No `NO ACTION DEFERRABLE`. | v1 exposes neither `DELETE /projects` nor `DELETE /teams`: thirty lines of schema for an operation nothing can trigger. The known consequence (deleting the author project erases the thread at the recipient) is commented in the migration and becomes a backlog task, not a feature. |
| 16 | **`issue_messages` carries no `team_id`.** Insertion and reading go through a `SELECT`/CTE scoped on the issue. | An exact mirror of `task_notes` in `000003` ("a note is never read without going through its task, which carries the scope") and of `CreateTaskNote`. |
| 17 | **Never a `= ''` sentinel cast into an enum.** State filter = `sqlc.narg('state')::issue_state IS NULL OR i.state = sqlc.narg('state')::issue_state`. | `('' OR …)` on an enum produces an **intermittent** `22P02 invalid input value for enum`, depending on the plan: SQL guarantees no short-circuit on `OR`. The correct pattern is already in `sql/queries/tasks.sql`. |
| 18 | **One single `ClaimNextNumber` per transaction, taken FIRST**, and it must **never** touch a key column. | An `UPDATE` on a non-key column takes `FOR NO KEY UPDATE`, compatible with the `FOR KEY SHARE` that the issue `INSERT` takes on both its parent projects: two symmetric agents (FRNT→CORE and CORE→FRNT) do not deadlock. The real reason is written above the query. |
| 19 | **Remove `updated_at = now()` from `ClaimNextNumber`** (`sql/queries/projects.sql`), then `make sqlc`. | Creating a task or an issue is not modifying the project. Without that removal, `projects.updated_at` becomes "date of the last object created" and any future cache or sync logic on that column is wrong. No Go signature changes. |
| 20 | **The counter stays transactional. Migrating to a `SEQUENCE` is forbidden.** | An `UPDATE ... RETURNING` rolls back with its transaction: zero gaps. A gap in a numbering an agent can read is a signal that does not exist and that it will speculate about. Integration test: `BEGIN; claim; ROLLBACK;` ⇒ `next_number` unchanged. |
| 21 | **`ClaimNextNumber` is never exposed on its own in `issue`'s `Store` interface.** `CreateIssue` does resolution + claim + insert + message + event in a single `WithTx`, and the claim is merged into the insertion CTE. | There is no path able to increment a sibling project's counter without inserting anything. An unknown key in the team ⇒ 0 rows ⇒ **no number consumed**. |
| 22 | **`WithTx` refuses nesting loudly** (an `inTx bool` field, error `"nested transaction"`), in `issue` **and** as a fix on `task/store/tx.go`, which today passes `db: s.db`. | Silently joining the transaction (`return fn(s)`) trades a deadlock for an invisible partial commit: an inner call that fails and whose error is swallowed has its writes committed by the outer one. Opening a second connection waits on the lock the first holds on the `projects` row: a deadlock invisible in a single-threaded test. |
| 23 | **A uniqueness violation on `*_number_unique_per_project` is NOT a `409`.** `translate()` branches on `pgErr.ConstraintName` and surfaces an internal error (`500`) + an explicit log. Same fix in `task/store/errors.go`. | A `23505` on `number` means the counter is corrupt: that is a server defect, not a caller error. |
| 24 | **Three modules: `task`, `issue`, `inbox`.** No merge, and **no extraction of `httpx`/`pgerr` in M3**. | `task` and `issue` have different scope predicates (`project_id = $` vs `project_id = $ OR author_project_id = $`): putting them side by side in one `Store` is the exact configuration in which copy-paste leaks. Extracting plumbing is a cross-cutting refactor of shipped code, decoupled from the number of modules, to be decided outside M3 — `task/handler.go` and `workspace/handler.go` already diverge (`scope()` vs `principal()`+`teamFor()`, `"conflict"` vs `"already exists"`, different `maxBodyBytes`): ~40 genuinely common lines, not 200. |
| 25 | **`requireProjectScope` is copied into `issue/module.go` and `inbox/module.go`.** No change to `internal/core/auth` nor to `module.go`. | The pattern exists and is documented (`ARCHITECTURE.md`). Adding a `ProjectOnly` to `auth.Service` would open a critical file subject to human validation in order to deduplicate 12 already-written lines. An admin token gets `403`, not a `200 []`. |
| 26 | **`issue` and `inbox` read other domains' tables through their own scoped query files. Rule: a feature may READ another domain's table through a dedicated scoped query; it never WRITES outside its domain, except through a declared port.** | `issue` reads `projects` (`GetProjectByKey`) and writes `projects.next_number` through `ClaimNextNumber` — a debt already incurred by `task` since M2 and documented nowhere. `inbox` reads `issues`, `tasks` and `events`. To be recorded in `docs/ARCHITECTURE.md` § "Inter-module interfaces" (first entry), with the **exhaustive** authorised surface. No lint checks it: it is a human review. |
| 27 | **A uniform 404 on everything not resolvable inside the principal's team.** Unknown project key, project of another team, invisible issue, closed issue: the same body `{"error":"not found"}`. | Distinct codes = a cross-tenant enumeration oracle (the key space is `^[A-Z][A-Z0-9]{1,9}$`). Consistent with `authenticate.go`'s `decoyHash` and with the comment already present in `task/handler.go:60-62`. The distinction does not even have to be computed: the team-scoped query cannot produce it. |
| 28 | **Error messages are enriched in the MCP layer, never in `writeError`.** | The API keeps its generic messages. The MCP server, which knows its own token, rephrases for its agent — and never states anything the agent does not already know. Two enrichments: unknown `to_project` → the list of the team's valid keys; unresolvable ref → "either the object does not exist, or it is a task of another project". |
| 29 | **The parameter carrying `CORE-34` is called `ref` everywhere; the one carrying `CORE` is called `to_project`.** Renaming `key` → `ref` in the already-shipped MCP tools, same commit. | `projects.key` is `CORE`: keeping `key` for `CORE-34` makes one word name two things in a surface where `DisallowUnknownFields` turns a confusion into a `400` + retry. One concept, one name. Server-side normalisation: upper case, a bare number accepted and resolved against the token's project. |
| 30 | **`get_task` becomes `get(ref)`, resolving a task OR an issue**, with `kind` as the response's first field. Disambiguation happens **on the MCP side**, not on the API side. | The shared counter makes the distinction invisible to the caller: a ref read in a commit or in `check_inbox` has to be usable without knowing what it names. On the HTTP side, `GET /api/task/{number}` stays unchanged and **without a project parameter** — the property "no surface where a scope could be bypassed" (`ARCHITECTURE.md`) is preserved. |
| 31 | **The identity and the list of sibling projects are injected into `initialize`'s `instructions` field.** The `whoami` tool is removed. | `runMCP` already resolves `/whoami` at start-up and fails with a clear message when the token is not project-scoped (shipped behaviour, kept). `GET /api/workspace/projects` is added to it. Catching up on snapshot staleness: an "unknown to_project" error triggers a **re-fetch** of the list before composing the error message — the net does not feed from the same snapshot as the nominal path. |
| 32 | **A three-level verbosity policy, one single truncation constant (500).** Lists: no bodies. `check_inbox`: last message truncated to 500 + `truncated`. `get(ref)`: full bodies, thread capped at the last 10 + `messages_total`. Truncation **in SQL**, not in Go. | An inbox of three lines is handled without a single `get`. No UUID, no `created_at` in list rows (the sort is done server-side), no echo of input parameters in write responses. |

---

## DDL

### `sql/migrations/000004_issues.up.sql`

```sql
-- 000004_issues — cross-project questions, message thread, event log.
--
-- An issue is opened by project A towards project B, inside a team. It belongs to B (as a GitHub
-- issue belongs to the repo it is opened on) and draws its number from B's counter: tasks and
-- issues share the same sequence, so CORE-34 always names exactly one object.

CREATE TYPE issue_state AS ENUM ('open', 'answered', 'closed');
CREATE TYPE event_subject AS ENUM ('task', 'issue');

-- `team_id` is denormalised as on `tasks`, so that EVERY read carries its full tenancy scope in the
-- query, with no join. The two composite foreign keys guarantee that this denormalisation cannot
-- diverge: an issue whose team_id is not that of both its projects is impossible to insert,
-- whatever query writes it.
CREATE TABLE issues (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id           uuid        NOT NULL,
    -- Recipient: owner of the issue, and project whose counter supplied `number`.
    project_id        uuid        NOT NULL,
    -- Sender: the project asking the question.
    author_project_id uuid        NOT NULL,
    number            bigint      NOT NULL,
    title             text        NOT NULL,
    state             issue_state NOT NULL DEFAULT 'open',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    closed_at         timestamptz,

    CONSTRAINT issues_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    -- CASCADE on the author too: v1 exposes neither DELETE /projects nor DELETE /teams, so the only
    -- genuinely triggerable cascade is deleting a team, which takes everything with it.
    -- Known and accepted consequence: the day a DELETE /projects exists, deleting the author
    -- project will erase the thread at the recipient. To reopen then (archived_at on projects),
    -- not before.
    CONSTRAINT issues_author_project_fk FOREIGN KEY (author_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,

    CONSTRAINT issues_number_unique_per_project UNIQUE (project_id, number),
    CONSTRAINT issues_number_positive CHECK (number >= 1),
    CONSTRAINT issues_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT issues_title_length CHECK (char_length(title) <= 200),

    -- An issue to oneself would be both incoming and outgoing: it would break the partition of
    -- list_issues(role=) and could never reach `answered`, since the transition is deduced from the
    -- message's sender. A question to oneself is a task.
    CONSTRAINT issues_not_self CHECK (author_project_id <> project_id),

    -- closed_at and state cannot diverge: a closed issue has a date, an open one does not. Without
    -- this, a badly written UPDATE produces a "closed" issue with no date, or an open issue
    -- claiming to have one.
    CONSTRAINT issues_closed_at_shape CHECK ((state = 'closed') = (closed_at IS NOT NULL))
);

-- Two mirror indexes: the visibility predicate is an OR over two columns, which no single composite
-- index can serve. The planner does a BitmapOr of the two when the role is unspecified, a simple
-- index scan when it is given.
--
-- The project column comes first (like tasks_project_active_idx): these indexes ALSO serve the
-- maintenance of the two foreign keys during a team-deletion cascade, which would otherwise do a
-- full seq scan of `issues` per deleted project.
CREATE INDEX issues_incoming_idx ON issues (project_id, team_id, state, updated_at DESC);
CREATE INDEX issues_outgoing_idx ON issues (author_project_id, team_id, state, updated_at DESC);

-- Message thread, append-only. No team_id here: a message is never read without going through its
-- issue, which carries the scope — the same rule as task_notes.
CREATE TABLE issue_messages (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id          uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    author_project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    body_md           text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT issue_messages_body_not_blank CHECK (btrim(body_md) <> '')
);

CREATE INDEX issue_messages_thread_idx ON issue_messages (issue_id, created_at, id);
-- A foreign-key index, not a read one: without it, a cascade on projects scans the table.
CREATE INDEX issue_messages_author_idx ON issue_messages (author_project_id);

-- Append-only log, per team.
--
-- In v1 it serves exactly one purpose: computing check_inbox's `new` flag. The reference state is
-- ALWAYS issues.state / tasks.status — so a missed event never costs more than a `new: false`,
-- never an invisible issue. That is what allows NOT paying the price of exactly-once delivery
-- (an xid8 column, a snapshot watermark, a composite cursor).
--
-- The sequence gap is real and accepted: `id` is assigned at INSERT, not at COMMIT, so a slow
-- transaction can commit a smaller id after a reader has passed that point. The only effect is a
-- missing `new` flag. Do NOT "fix" this behaviour without first rereading decision #1: the fix
-- costs more than the defect.
--
-- No free text here: no title, no body, no denormalised ref. A row is ~100 bytes, bounded in size,
-- and check_inbox reads the titles from issues/tasks — which are joined anyway. Denormalising a ref
-- would force an extra GetProjectByID INSIDE the write transaction (Principal does not carry the
-- project key) for no gain.
CREATE TABLE events (
    id               bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id          uuid          NOT NULL,
    -- Project owning the SUBJECT (the one whose counter supplied the ref), not the audience: the
    -- inbox never reads `events` by this field, it reaches them by joining on an already-scoped
    -- subject. Reading events therefore has no unscoped surface.
    project_id       uuid          NOT NULL,
    actor_project_id uuid          NOT NULL,
    kind             text          NOT NULL,
    subject_type     event_subject NOT NULL,
    subject_id       uuid          NOT NULL,
    created_at       timestamptz   NOT NULL DEFAULT now(),

    CONSTRAINT events_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_actor_fk FOREIGN KEY (actor_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_kind_format CHECK (kind ~ '^[a-z_]+\.[a-z_]+$')
);

-- Serves the EXISTS of the `new` flag: without it, every inbox row scans the team's log.
CREATE INDEX events_subject_idx ON events (subject_id, id);
-- Serves the max(id) per team (head of the log) and, in v2, the SSE stream.
CREATE INDEX events_team_idx ON events (team_id, id);
-- Foreign-key index.
CREATE INDEX events_actor_idx ON events (actor_project_id);

-- Read cursor, per TOKEN and not per project: two agent sessions on the same repo each have their
-- own progress.
--
-- No foreign key towards events.id, deliberately: a future purge of the log must stay a simple
-- DELETE, with no cascade and no constraint violation. The cursor starts at 0 and is never seeded:
-- a fresh (or freshly rotated) token sees everything marked `new`, which is accurate, and replays
-- nothing since the buckets are bounded.
CREATE TABLE token_cursors (
    token_id      uuid        PRIMARY KEY REFERENCES tokens (id) ON DELETE CASCADE,
    last_event_id bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT token_cursors_last_event_id_positive CHECK (last_event_id >= 0)
);
```

### `sql/migrations/000004_issues.down.sql`

```sql
DROP TABLE IF EXISTS token_cursors;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS issue_messages;
DROP TABLE IF EXISTS issues;
DROP TYPE IF EXISTS event_subject;
DROP TYPE IF EXISTS issue_state;
```

---

## Critical scoped queries

### `sql/queries/projects.sql` — fix (decision #19)

```sql
-- ClaimNextNumber reserves the project's next readable identifier (FRNT-34).
-- The UPDATE ... RETURNING serialises concurrent calls on the project's row, and rolls back with
-- its transaction: no gap in the numbering.
--
-- LOCKING CONSTRAINT — never add the write of a key column here (nor of a column covered by a
-- unique index). As long as the UPDATE only touches non-key columns, Postgres takes a
-- FOR NO KEY UPDATE, compatible with the FOR KEY SHARE that the issue INSERT takes on BOTH its
-- parent projects. The day that stops being true, two symmetric agents (FRNT→CORE and CORE→FRNT)
-- deadlock.
--
-- `updated_at` is deliberately NOT touched: creating a task or an issue is not modifying the
-- project. Writing it here would turn projects.updated_at into a "date of the last object created".
-- name: ClaimNextNumber :one
UPDATE projects
SET next_number = next_number + 1
WHERE id = $1 AND team_id = $2
RETURNING (next_number - 1)::bigint AS claimed_number;
```

### `sql/queries/issues.sql`

```sql
-- The scope is IN the query, without exception. The canonical visibility clause is
--   team_id = @team_id AND (project_id = @project_id OR author_project_id = @project_id)
-- where @project_id comes EXCLUSIVELY from Principal.ProjectID. `team_id` always appears there,
-- even though it is redundant with the project: it is defence in depth in case the project ever
-- came from a bad resolution.
--
-- Neither author nor recipient ⇒ zero rows, strictly indistinguishable from a non-existent number.
-- There is no 403 on an issue key, so there is no oracle for enumerating a sibling repo's backlog
-- through answer_issue("CORE-1"), ("CORE-2"), …

-- CreateIssue resolves the recipient, reserves its number and inserts the issue in ONE statement.
--
-- An unknown key — or a known one belonging to another team — does not match the CTE, so the INSERT
-- produces nothing AND no number is consumed. That is what prevents advancing a third-party
-- project's counter by guessing it, and what makes "does not exist" and "outside the team"
-- indistinguishable without the code having to care.
--
-- This statement must be the FIRST of its transaction: it is the only long-lived row lock of the
-- write path, and there must be exactly one (see ClaimNextNumber).
-- name: CreateIssue :one
WITH claimed AS (
    UPDATE projects p
    SET next_number = p.next_number + 1
    WHERE p.team_id = @team_id AND p.key = @to_project_key
    RETURNING p.id AS project_id, (p.next_number - 1)::bigint AS number
)
INSERT INTO issues (team_id, project_id, author_project_id, number, title, state)
SELECT @team_id, c.project_id, @author_project_id, c.number, @title, 'open'
FROM claimed c
RETURNING *;

-- name: AppendFirstMessage :one
INSERT INTO issue_messages (issue_id, author_project_id, body_md)
VALUES (@issue_id, @author_project_id, @body_md)
RETURNING *;

-- GetIssueByRef resolves CORE-34 for a given caller. The project is named by its KEY, never by a
-- UUID: an agent does not handle any, so it cannot inject one.
-- name: GetIssueByRef :one
SELECT i.*, p.key AS project_key, a.key AS author_project_key
FROM issues i
JOIN projects p ON p.id = i.project_id        AND p.team_id = i.team_id
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
WHERE i.team_id = @team_id
  AND p.key     = @project_key
  AND i.number  = @number
  AND (i.project_id = @caller_project_id OR i.author_project_id = @caller_project_id);

-- The thread is scoped by joining on its issue: impossible to read the messages of an issue one
-- cannot see, even knowing its identifier.
-- name: ListIssueMessages :many
SELECT m.body_md, m.created_at, ap.key AS author_key
FROM issue_messages m
JOIN issues i    ON i.id  = m.issue_id
JOIN projects ap ON ap.id = m.author_project_id
WHERE i.team_id = @team_id
  AND i.id      = @issue_id
  AND (i.project_id = @caller_project_id OR i.author_project_id = @caller_project_id)
ORDER BY m.created_at, m.id;

-- One single query for the three role cases: three queries would be three occasions to re-secure.
-- `role` is NEVER an authorisation — it is a restriction laid on top of the complete visibility
-- clause, which stays unconditional.
-- State filter through sqlc.narg and never through a '' sentinel cast into an enum: SQL guarantees
-- no short-circuit on OR, and ''::issue_state raises a 22P02 depending on the chosen plan.
-- name: ListIssues :many
SELECT i.number, i.state, i.title, i.updated_at,
       p.key AS project_key,
       a.key AS author_project_key,
       (i.project_id = @project_id) AS incoming
FROM issues i
JOIN projects p ON p.id = i.project_id        AND p.team_id = i.team_id
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
WHERE i.team_id = @team_id
  AND (i.project_id = @project_id OR i.author_project_id = @project_id)
  AND (NOT @only_incoming::boolean OR i.project_id        = @project_id)
  AND (NOT @only_outgoing::boolean OR i.author_project_id = @project_id)
  AND (sqlc.narg('state')::issue_state IS NULL OR i.state = sqlc.narg('state')::issue_state)
  AND (@include_closed::boolean OR i.state <> 'closed')
ORDER BY i.updated_at DESC, i.number DESC
LIMIT @max_rows::int;

-- AnswerIssue appends a message AND applies the state transition in ONE statement.
--
-- Two separate statements would let this through: the caller posts their message, the counterpart
-- closes the issue, the transition no longer matches — the message exists inside a closed issue,
-- updated_at has not moved, and the inbox (derived from the state) will never show it. A written
-- answer that disappears.
--
-- The state is never a parameter: it is computed from WHO speaks. An agent cannot lie about the
-- state it produces.
--   - close = true              → closed (both participants may close, it is terminal)
--   - message from the recipient → answered
--   - message from the author    → open (chasing puts the recipient back in debt)
--
-- `AND i.state <> 'closed'` is not negotiable: without it, answering a closed issue resurrects it.
-- `closed_at` is never overwritten with NULL: a CASE ... ELSE NULL would erase the closing date on
-- every message.
-- name: AnswerIssue :one
WITH target AS (
    UPDATE issues i
    SET state = CASE
                    WHEN @close::boolean            THEN 'closed'
                    WHEN i.project_id = @project_id THEN 'answered'
                    ELSE                                 'open'
                END::issue_state,
        closed_at  = CASE WHEN @close::boolean THEN now() ELSE i.closed_at END,
        updated_at = now()
    WHERE i.team_id    = @team_id
      AND i.project_id = @target_project_id
      AND i.number     = @number
      AND (i.project_id = @project_id OR i.author_project_id = @project_id)
      AND i.state <> 'closed'
    RETURNING i.id, i.number, i.state
),
appended AS (
    INSERT INTO issue_messages (issue_id, author_project_id, body_md)
    SELECT t.id, @project_id, @body_md FROM target t
    RETURNING issue_id
)
SELECT t.id, t.number, t.state FROM target t;

-- name: AppendEvent :exec
INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
VALUES (@team_id, @project_id, @actor_project_id, @kind, @subject_type, @subject_id);
```

### `sql/queries/inbox.sql`

```sql
-- check_inbox returns a CURRENT STATE, not a stream. No bucket depends on the cursor: the cursor
-- only serves the `new` flag. A missed event degrades a new:true into a new:false, never a row into
-- the absence of a row.
--
-- The log is never read by a predicate of its own: it is reached by an EXISTS on an already-scoped
-- subject. There is therefore no query able to read a third-party project's activity.

-- InboxCursor reads the token's cursor AND the head of the team's log in one go. The head is
-- captured BEFORE the buckets are computed: any event created during the call stays `new` for the
-- next round.
-- name: InboxCursor :one
SELECT
    coalesce((SELECT c.last_event_id FROM token_cursors c WHERE c.token_id = @token_id), 0)::bigint
        AS last_event_id,
    coalesce((SELECT max(e.id) FROM events e WHERE e.team_id = @team_id), 0)::bigint
        AS head_event_id;

-- Bucket 1 — needs_answer: somebody is blocked on me.
-- In this bucket the last message is always the author's: my own reply would move the issue to
-- `answered` and take it out of the bucket.
-- name: ListIncomingOpenIssues :many
SELECT i.number, i.title, i.updated_at,
       a.key AS peer_key,
       coalesce(left(m.body_md, 500), '')::text AS excerpt,
       coalesce(char_length(m.body_md) > 500, false)::boolean AS truncated,
       EXISTS (
           SELECT 1 FROM events e
           WHERE e.subject_type = 'issue' AND e.subject_id = i.id AND e.id > @last_event_id
       ) AS is_new
FROM issues i
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
LEFT JOIN LATERAL (
    SELECT im.body_md FROM issue_messages im
    WHERE im.issue_id = i.id
    ORDER BY im.created_at DESC, im.id DESC
    LIMIT 1
) m ON true
WHERE i.team_id    = @team_id
  AND i.project_id = @project_id
  AND i.state      = 'open'
ORDER BY i.updated_at DESC
LIMIT @max_rows::int;

-- Bucket 2 — answered: I was blocked, I am not any more. The last message is the reply.
-- The ref carries the RECIPIENT's key (p.key), not mine: that is what the agent has to reuse in
-- answer_issue.
-- name: ListOutgoingAnsweredIssues :many
SELECT i.number, i.title, i.updated_at,
       p.key AS peer_key,
       coalesce(left(m.body_md, 500), '')::text AS excerpt,
       coalesce(char_length(m.body_md) > 500, false)::boolean AS truncated,
       EXISTS (
           SELECT 1 FROM events e
           WHERE e.subject_type = 'issue' AND e.subject_id = i.id AND e.id > @last_event_id
       ) AS is_new
FROM issues i
JOIN projects p ON p.id = i.project_id AND p.team_id = i.team_id
LEFT JOIN LATERAL (
    SELECT im.body_md FROM issue_messages im
    WHERE im.issue_id = i.id
    ORDER BY im.created_at DESC, im.id DESC
    LIMIT 1
) m ON true
WHERE i.team_id           = @team_id
  AND i.author_project_id = @project_id
  AND i.state             = 'answered'
ORDER BY i.updated_at DESC
LIMIT @max_rows::int;

-- Bucket 3 — in_progress: a task left there signals an interrupted session, to be picked back up
-- before opening a new one. No `new` flag: this is my own work.
-- name: ListInProgressTasks :many
SELECT t.number, t.title, t.priority, t.updated_at
FROM tasks t
WHERE t.team_id     = @team_id
  AND t.project_id  = @project_id
  AND t.status      = 'in_progress'
  AND t.archived_at IS NULL
ORDER BY t.updated_at DESC
LIMIT @max_rows::int;

-- GREATEST keeps the cursor from going backwards if two concurrent check_inbox calls on the same
-- token cross. No transaction is needed: the worst case is a lost `new` flag.
-- name: AdvanceInboxCursor :exec
INSERT INTO token_cursors (token_id, last_event_id)
VALUES (@token_id, @last_event_id)
ON CONFLICT (token_id) DO UPDATE
SET last_event_id = GREATEST(token_cursors.last_event_id, EXCLUDED.last_event_id),
    updated_at    = now();
```

---

## Go interfaces

Strict `handler → service → store` flow. No `internal/feature/<other>` import. `*sql.DB` never goes
past the store layer. Every file below carries a `// SOMMAIRE` block from 2 top-level declarations
onwards.

### `internal/feature/issue/store/store.go` — CONTRACT ONLY

```go
package store

var (
	ErrNotFound = errors.New("issue store: not found")
	ErrConflict = errors.New("issue store: conflict")
)

// Ref names an object by its readable key, from a given caller's point of view.
// CallerProjectID comes from Principal.ProjectID and is never read from a request.
type Ref struct {
	TeamID          uuid.UUID
	CallerProjectID uuid.UUID
	ProjectKey      string // the ref's prefix: CORE in CORE-34
	Number          int64
}

type Issue struct {
	ID               uuid.UUID
	TeamID           uuid.UUID
	ProjectID        uuid.UUID // recipient
	AuthorProjectID  uuid.UUID // sender
	ProjectKey       string
	AuthorProjectKey string
	Number           int64
	Title            string
	State            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ClosedAt         *time.Time
}

type Message struct {
	AuthorKey string
	Body      string
	CreatedAt time.Time
}

// NewIssue: the recipient is named by its KEY, never by a UUID. Resolution, number reservation and
// insertion are a single SQL statement.
type NewIssue struct {
	TeamID          uuid.UUID
	AuthorProjectID uuid.UUID
	ToProjectKey    string
	Title           string
	Body            string
}

type Filter struct {
	TeamID        uuid.UUID
	ProjectID     uuid.UUID
	OnlyIncoming  bool
	OnlyOutgoing  bool
	State         string // empty = no filter
	IncludeClosed bool
	Limit         int32
}

// Answer carries a message and, possibly, the closure. The resulting state is NOT a field: it is
// computed in SQL from ProjectID (who speaks), and the store returns it.
type Answer struct {
	Ref   Ref
	Body  string
	Close bool
}

// Event is a row of the log. ProjectID is the project owning the subject.
type Event struct {
	TeamID         uuid.UUID
	ProjectID      uuid.UUID
	ActorProjectID uuid.UUID
	Kind           string
	SubjectType    string // "issue"
	SubjectID      uuid.UUID
}

// Store is the contract the service consumes.
//
// Every method carries the caller's complete scope. There is no unscoped read or write in this
// contract, so no caller can forget one. ClaimNextNumber is NOT in it: reserving a number is never
// an operation addressable on its own, otherwise there would be a path able to advance a sibling
// project's counter without inserting anything.
type Store interface {
	// WithTx runs fn inside a transaction, on a store that shares it. Refuses nesting with an
	// explicit error: silently joining the transaction would let the writes of an inner call whose
	// error was swallowed be committed.
	WithTx(ctx context.Context, fn func(Store) error) error

	// ProjectIDByKey resolves a project key inside the caller's team. A key from another team is
	// not findable, not forbidden.
	ProjectIDByKey(ctx context.Context, teamID uuid.UUID, key string) (uuid.UUID, error)

	CreateIssue(ctx context.Context, in NewIssue) (Issue, error)
	AppendFirstMessage(ctx context.Context, issueID, authorProjectID uuid.UUID, body string) error

	IssueByRef(ctx context.Context, ref Ref) (Issue, error)
	ListIssues(ctx context.Context, f Filter) ([]Issue, error)
	ListMessages(ctx context.Context, teamID, callerProjectID, issueID uuid.UUID) ([]Message, error)

	// Answer inserts the message and applies the state transition in a single statement.
	Answer(ctx context.Context, in Answer) (Issue, error)

	// AppendEvent writes into the log. Called exclusively inside a WithTx: an event without its
	// issue would notify an object that does not exist.
	AppendEvent(ctx context.Context, e Event) error
}

type store struct {
	q    *database.Queries
	db   *sql.DB
	inTx bool
}

func New(q *database.Queries, db *sql.DB) Store { return &store{q: q, db: db} }
```

### `internal/feature/issue/store/tx.go`

```go
// WithTx runs fn inside a transaction and commits only if fn succeeds.
//
// Nesting is refused, not absorbed. Opening a second transaction would take another connection from
// the pool, which would wait on the lock this one holds on the projects row (ClaimNextNumber): a
// deadlock invisible in a unit test as in a single-threaded integration test. And silently joining
// the existing one would have the outer transaction commit the partial writes of an inner call
// whose error was swallowed.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.inTx {
		return errors.New("issue store: nested transaction")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("issue store: opening transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no effect after a successful Commit

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("issue store: commit: %w", err)
	}
	return nil
}
```

> The same fix to be applied identically in `internal/feature/task/store/tx.go`, which today passes
> `db: s.db` with no guard and is therefore re-entrant on a second connection (line 22).

### `internal/feature/issue/service/service.go` — CONTRACT ONLY

```go
var (
	ErrInvalidInput = errors.New("issue: invalid input")
	ErrNotFound     = errors.New("issue: not found")
	ErrConflict     = errors.New("issue: conflict")
)

// Service carries the cross-project questions.
//
// TeamID and ProjectID come from the token's Principal, never from the request body: that is what
// makes acting on another project's behalf impossible.
type Service interface {
	CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error)
	ListIssues(ctx context.Context, in ListIssuesInput) ([]Issue, error)
	GetIssue(ctx context.Context, in RefInput) (IssueDetail, error)
	Answer(ctx context.Context, in AnswerInput) (Issue, error)
}

type service struct{ store store.Store }

func New(st store.Store) Service { return &service{store: st} }

// CreateIssueInput — the recipient is a KEY. TeamID/AuthorProjectID carry `json:"-"`: they cannot
// be set from the body.
type CreateIssueInput struct {
	TeamID          uuid.UUID `json:"-"`
	AuthorProjectID uuid.UUID `json:"-"`

	ToProject string `json:"to_project"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

// RefInput names CORE-34 for a caller. ProjectKey is the ref's prefix, and it is NOT a free choice:
// the service refuses a key naming neither the caller's project nor a project of its team, and the
// query would refuse it anyway.
type RefInput struct {
	TeamID     uuid.UUID `json:"-"`
	ProjectID  uuid.UUID `json:"-"`
	ProjectKey string    `json:"-"`
	Number     int64     `json:"-"`
}

type ListIssuesInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Role  string `json:"role"`  // "", "incoming", "outgoing"
	State string `json:"state"` // "", "open", "answered", "closed"
	Limit int    `json:"limit"`
}

// AnswerInput — Close closes the issue. The resulting state is not expressible: it is deduced.
type AnswerInput struct {
	Ref   RefInput `json:"-"`
	Body  string   `json:"body"`
	Close bool     `json:"close"`
}

// Issue is the API view. Ref is the complete readable key, composed in the service: it is the ONLY
// producer of a key in the feature, no concatenation anywhere else.
type Issue struct {
	Ref       string     `json:"ref"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Role      string     `json:"role"` // "incoming" | "outgoing"
	Peer      string     `json:"peer"` // key of the project at the other end
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

type Message struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type IssueDetail struct {
	Issue
	Messages      []Message `json:"messages"`
	MessagesTotal int       `json:"messages_total"`
}
```

### `internal/feature/inbox/store/store.go` — CONTRACT ONLY

```go
// Scope carries the complete scope of one inbox read. There is no alternative constructor:
// TeamID/ProjectID come from the Principal, and so does TokenID.
type Scope struct {
	TokenID   uuid.UUID
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Limit     int32
}

type Cursor struct {
	LastEventID int64
	HeadEventID int64
}

type IssueLine struct {
	Number    int64
	Title     string
	Peer      string
	Excerpt   string
	Truncated bool
	New       bool
	UpdatedAt time.Time
}

type TaskLine struct {
	Number    int64
	Title     string
	Priority  string
	UpdatedAt time.Time
}

// Store reads a project's actionable state. No Transactor: check_inbox's consistency depends on no
// atomicity — the cursor only drives a display flag.
type Store interface {
	Cursor(ctx context.Context, sc Scope) (Cursor, error)
	IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error)
	Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error
}

type store struct{ q *database.Queries }

// New does not receive a *sql.DB: inbox never opens a transaction.
func New(q *database.Queries) Store { return &store{q: q} }
```

### `internal/feature/inbox/service/service.go` — CONTRACT ONLY

```go
type Service interface {
	Check(ctx context.Context, in CheckInput) (Inbox, error)
}

type CheckInput struct {
	TokenID    uuid.UUID `json:"-"`
	TeamID     uuid.UUID `json:"-"`
	ProjectID  uuid.UUID `json:"-"`
	ProjectKey string    `json:"-"` // composed by the handler from the resolved Principal
}

type IssueLine struct {
	Ref       string    `json:"ref"`
	Title     string    `json:"title"`
	Peer      string    `json:"peer"`
	Excerpt   string    `json:"excerpt"`
	Truncated bool      `json:"truncated,omitempty"`
	New       bool      `json:"new"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// Inbox is the current state, not a stream. The `more` fields say what did not fit in the bucket.
type Inbox struct {
	Project     string      `json:"project"`
	NeedsAnswer []IssueLine `json:"needs_answer"`
	Answered    []IssueLine `json:"answered"`
	InProgress  []TaskLine  `json:"in_progress"`
	More        More        `json:"more,omitempty"`
}

type More struct {
	NeedsAnswer int `json:"needs_answer,omitempty"`
	Answered    int `json:"answered,omitempty"`
	InProgress  int `json:"in_progress,omitempty"`
}
```

### Routing

```go
// internal/feature/issue/module.go — middleware bound ONCE, requireProjectScope copied.
r.Handle("POST /{$}",                    project(m.h.CreateIssue))
r.Handle("GET /{$}",                     project(m.h.ListIssues))
r.Handle("GET /{project}/{number}",      project(m.h.GetIssue))
r.Handle("POST /{project}/{number}/answer", project(m.h.Answer))

// internal/feature/inbox/module.go
r.Handle("GET /{$}", project(m.h.Check))

// cmd/api/main.go — buildModules()
issue.NewModule(base),   // store.New(cfg.DB, cfg.RawDB)
inbox.NewModule(base),   // store.New(cfg.DB)  — no RawDB, no transaction
```

---

## M3's MCP surface

**Eight** tools since FLWL-15 (nine at M3's delivery: see the note on `add_task_note` below). The
budget is re-injected on **every turn** of every session: any addition is paid indefinitely.

| Tool | Parameters |
| ----- | ---------- |
| `list_tasks` | `status?` ∈ todo\|in_progress\|blocked\|done, `limit?` (default 50, max 200), `archived?` |
| `get` | **`ref`** (required) — `CORE-34`, or a bare number resolved inside the token's project |
| `create_task` | `title` (required), `body?`, `status?`, `priority?`, `deadline?` (RFC 3339) |
| `update_task` | **`ref`** (required), `title?`, `body?`, `status?`, `priority?`, `deadline?`, `clear_deadline?`, `note?`, `archive?` |
| `create_issue` | `to_project` (required), `title` (required), `body` (required) |
| `list_issues` | `role?` ∈ incoming\|outgoing (omitted = both), `state?` ∈ open\|answered\|closed (omitted = open + answered), `limit?` (default 20, max 100) |
| `answer_issue` | `ref` (required), `body` (required), `close?` (default false) |
| `check_inbox` | **no parameter** |

**Non-negotiable behaviours of the surface:**

- `get(ref)` returns `kind: "task"|"issue"` as its first field. Resolution happens **on the MCP
  side**: if the prefix is my own key, try `GET /api/task/{n}` then `GET /api/issue/{myKey}/{n}` (the
  shared counter makes `CORE-34` ambiguous for CORE's agent: it can be its task or an incoming
  issue); otherwise `GET /api/issue/{key}/{n}`. `task`'s HTTP API stays without a project parameter.
- `get` is the **only** tool returning full bodies. Thread capped at the last 10 messages +
  `messages_total`.
- `create_issue` refuses `to_project` = my own project, with a message redirecting to `create_task`.
  `to_project` is upper-cased before resolution. Minimal response: `{ref, to_project, state}`.
- `check_inbox` returns `{project, needs_answer[], answered[], in_progress[], more{}}`. 10 rows per
  bucket. Excerpts at 500 characters + `truncated`. `in_progress` carries no `new`.
- `check_inbox`'s description, to be written word for word: *"What is waiting for you: incoming
  questions to handle, your questions that have been answered, your tasks in progress. The reference
  state remains `list_issues` / `list_tasks`."*
- `answer_issue`: `body` is mandatory even to close — closing without a reason tells the counterpart
  nothing.
- No UUID crosses the MCP layer. Corollary: remove `Project.ID` from the response of
  `GET /api/workspace/projects` — it contradicts the comment in `workspace/service/service.go` and
  has no consumer (`cmd/flowlio/commands.go` only prints `Key` and `Name`).

### Removed from the announced surface (`docs/DESIGN-V1.md:131-143`)

| Tool | Reason |
| ----- | ------ |
| `whoami` | Called once per session, constant content over the token's life. `runMCP` already resolves `/whoami` at start-up: `GET /projects` is added to it and the whole thing is injected into `initialize.instructions` ("You are the agent of project CORE (omiros-core), team omiros. Sibling projects: WEB, API. A reference reads KEY-NUMBER."). Zero schema, zero turns, the information is in the context before the first message. |
| `close_issue` | Merged into `answer_issue(close=true)`. The majority case is "I answer and that settles it": two tools = two turns for one act. Discoverability covered by `answer_issue`'s description. |
| `get_task` | Replaced by `get(ref)`. The shared counter deliberately hides from the agent whether `CORE-34` is a task or an issue: two typed tools would fail half the time when the agent only has the ref (read in `check_inbox`, a commit, an issue message). |
| `get_issue` | **Never added** — absorbed by `get(ref)`. |
| `list_projects` | **Never added** — the list of sibling projects lives in `instructions`, and refreshes on an "unknown to_project" error. |
| `archive_task` | Already absorbed in M2 by `update_task(archive=true)`. |
| `update_issue` | **Never added** — an issue's title is immutable (decision #12). |

`add_task_note` was **kept in M3**, then **folded into `update_task` as a `note?` field** (FLWL-15,
2026-08-02) — the backlog task announced here. What the fold cost, and why it was worth it:

| | Before | After |
| --- | --- | --- |
| Tools exposed | 9 | **8** |
| "move to done and say why" | 2 calls, 2 transactions | **1 call, 1 transaction** |
| State "status changed, reason lost" | reachable | **impossible** |

The gain is not only a schema saved. Two separate writes left the state in which the `done` is in the
database and the note fell through: the next session read a `done` that nothing explained. The note
therefore travels INSIDE the patch, written by `service.UpdateTask` in a `WithTx` with it.

> Consequence on the API side: the route `POST /api/task/{number}/notes` **no longer exists**, and
> neither do `service.AddNote` / `handler.AddNote`. There is a single write path towards a task's
> thread, used identically by the CLI (`flowlio task note`) and by MCP. `store.AddNote` survives: it
> is what `UpdateTask` calls inside the transaction.

### Shape of the write returns (FLWL-15)

Every write returns `{ref, <object>}`, and nothing else:

```
create_task  → {"ref": "CORE-34", "task":  {…}}
update_task  → {"ref": "CORE-34", "task":  {…}}
create_issue → {"ref": "FRNT-12", "issue": {…}}
answer_issue → {"ref": "FRNT-12", "issue": {…}}
list_tasks   → [{"ref": "CORE-7", "task": {…}}, …]
get          → {"kind": "task"|"issue", "ref": …, "task"|"issue": {…}}
```

Before, an agent had to guess where to read the reference depending on the tool it had just called:
under `key` for a task, inside the object for an issue. `list_issues` keeps `[]Issue` with no
envelope — every row already carries its `ref`, and enveloping it would duplicate it on every row of
a listing.

---

## Traps not to miss at implementation time

1. **The log's sequence gap.** `events.id` is assigned at `INSERT`, not at `COMMIT`: a slow
   transaction can commit a smaller id after a reader has passed it. **This is accepted**, and it is
   only acceptable because the cursor drives the `new` flag and nothing else. The day somebody makes
   the presence of an inbox row depend on the cursor, they reintroduce a silent loss of issues. Write
   that sentence into the migration.
2. **`AND i.state <> 'closed'` and `closed_at`.** Without the guard, `answer_issue` resurrects a
   closed issue and it reappears indefinitely in the counterpart's bucket. And
   `closed_at = CASE ... ELSE NULL` erases the closing date on every message: it is
   `ELSE i.closed_at`.
3. **Message and transition = a single statement.** Two statements let through a message written
   into an issue closed in the meantime: `updated_at` does not move, the derived inbox never shows
   it, an answer disappears.
4. **Never a `''` sentinel cast into an enum.** `(@state::text = '' OR state = @state::issue_state)`
   raises a `22P02` **intermittently** depending on the plan: SQL does not short-circuit `OR`. Use
   `sqlc.narg(...)::issue_state IS NULL`, a pattern already present in `sql/queries/tasks.sql`.
5. **`ClaimNextNumber` must never write a key column.** As long as that holds,
   `FOR NO KEY UPDATE` and the `FOR KEY SHARE` of the INSERT's FKs are compatible and two symmetric
   agents do not deadlock. It is not "one claim per transaction" that protects, contrary to what one
   believes when reading it.
6. **`WithTx` re-entrancy.** Refuse it loudly. Neither `db: s.db` (a second connection → deadlock on
   the `projects` row) nor `return fn(s)` (silent partial commit). The fix to be carried to
   `task/store/tx.go` as well.
7. **`translate()` and `23505`.** A violation of `issues_number_unique_per_project` means the counter
   is corrupt: `500` + an explicit log, not `409 conflict`. Branch on `pgErr.ConstraintName`, in
   `issue` **and** in `task` (today `23505`, `23514` and `23503` are all three mapped onto
   `ErrConflict`).
8. **"Does not exist" and "forbidden" are never distinguished** on an issue key nor on a project key.
   The access predicate is in the `WHERE`, never a service `if`: zero rows in both cases, same code,
   same message, same latency. No 403 on `answer_issue` (both participants may close, so that path
   does not exist). Any disambiguation happens **on the MCP side only**, from what the caller can
   already read.
9. **`issue_messages` without `team_id`.** Insertion and reading go through the issue, as
   `CreateTaskNote` goes through its task. Do not "fix" this by adding a `team_id`: that would be a
   second source of truth to keep consistent.
10. **`sql/schema/schema.sql` to be re-dumped** (`make schema`) and **`000004_issues.down.sql` to be
    written**. CLAUDE.md § Database: the schema is the source of truth, updated after every
    migration. sqlc reads `sql/migrations/` directly: an incomplete migration breaks `make sqlc`.
11. **`// SOMMAIRE` on every new file with ≥ 2 declarations**, with the **final** line numbers. The
    `PostToolUse` hook blocks with exit 2.
12. **File sizes.** `task/store/task.go` is already 201 lines for 6 methods. `issue`'s store will
    have 8 + messages + events: split it from the start into `issue.go` / `message.go` / `event.go` /
    `project.go`, do not wait for `scripts/check-file-size.sh` (300) to block.
13. **`instructions`: degradation.** `runMCP` already fails if `/whoami` fails — keep that behaviour
    (a clear message on **stderr**, never stdout). But the list of sibling projects must be
    **re-fetched** when composing the "unknown `to_project`" error: if the net feeds from the
    start-up snapshot, the nominal path and its recovery fall together for the whole life of the
    process.
14. **`stdout` belongs to the MCP protocol.** One stray `Println` in the new tool code breaks the
    agent's session.
15. **Idempotence.** A `create_issue` whose response is lost (timeout, killed session) will be
    replayed by the agent: a duplicated issue and a burnt number, while dense numbering is an
    invariant. The defect pre-exists on `create_task`; M3 doubles it on the product's most expensive
    path. **Out of M3's scope** — create the backlog task, do not improvise it.
16. **Undelivered DESIGN-V1 debts, to be tracked and not handled in M3**: rate limiting on token
    resolution (`DESIGN-V1.md` § Security, absent from the code), purging the event log (when it
    comes: purging by age **alone** would make a dormant project lose everything it has not read —
    the bound is the most-lagging cursor).
17. **Documentation to update in the same commit as the migration**: `docs/ARCHITECTURE.md`
    § Domains (two lines: `issue`, `inbox`, with their scopes), § Inter-module interfaces (**first
    entry**, decision #26, to be validated with Maxence), and `docs/DESIGN-V1.md` § MCP surface +
    § Schema (`events` no longer has the shape announced on line 102, and the tool list on lines
    131-143 changes).

---

## Questions left for the human

One, plus a procedural validation.

1. **Who may close an issue?** Settled here: **both participants**, `closed` terminal, reopening = a
   new issue. This removes every 403 path on an issue key, therefore every oracle surface, and
   matches the GitHub mental model. The alternative (the author alone, with the recipient having
   `answered` to signal they have done their part) protects the author against a recipient closing an
   awkward question, at the price of a conditional 403 to be written in a strict order (scoped UPDATE
   → re-read → 403 otherwise 404). **If Maxence does not rule, decision #10 applies.**

2. **A procedural validation, not a decision:** `docs/ARCHITECTURE.md` § "Inter-module interfaces"
   says "None for now" and the rule requires validating every entry with the human. M3 writes the
   first one — which is not a `FeatureRegistry` but the rule of shared access to tables (decision
   #26). It formalises a debt **already incurred by M2** (`task/store/task.go:39` writes into
   `projects` through `ClaimNextNumber`) and that no lint sees, `check-cross-feature-imports.sh`
   scanning Go imports only.
