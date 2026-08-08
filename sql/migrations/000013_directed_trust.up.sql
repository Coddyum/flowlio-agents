-- 000013_directed_trust — the trust edge becomes an ARROW: who may open a question AT whom.
--
-- WHAT 000007 GOT WRONG, AND WHY IT WAS REASONABLE AT THE TIME. That migration stored one row per
-- PAIR, normalised by least/greatest, and argued the channel is bidirectional by construction:
-- answering an issue brings the peer's text into the author's context, so a one-way arrow would
-- describe a flow that does not exist.
--
-- The argument holds for a THREAD and fails for the GESTURE that opens one. Opening a question is
-- not answering one. `web → core` means WEB MAY OPEN A QUESTION AT CORE, and nothing more: core
-- still answers inside the thread web opened — that is the same thread, not a new question — and
-- core still cannot open one of its own at web. Under the pair, "web may question core, core may
-- not question web" was not a state the table could hold, so the customer could not declare it.
--
-- WHAT FORCED THE CHANGE. The canvas of flowlio.me draws a card per repo with TWO HANDLES, one per
-- side, and a link drawn from one handle to the other. A pair cannot render that: the same link
-- would have to appear on both handles, and cutting one would cut the other. The domain has to
-- carry the distinction the surface already draws, or the surface lies about the database.
--
-- THE ALLOW-LIST DOES NOT MOVE. The ABSENCE of a row is still the refusal, and it is now absent per
-- DIRECTION. A directed model can only make the graph tighter than the pair it replaces, never
-- looser: the backfill below turns each existing pair into the two arrows it already meant, so no
-- installation gains an authorisation on the day it migrates.
--
-- THE REFUSAL IS UNCHANGED, AND THAT IS THE POINT. An unauthorised direction does not match the
-- EXISTS in CreateIssue: zero rows, sql.ErrNoRows, ErrNotFound, 404 — byte for byte the answer a
-- repo that does not exist gets. Direction is added to the graph WITHOUT adding a way to observe it
-- from the outside.

-- The ordering CHECK goes first: it is what makes the mirror rows below illegal.
--
-- Dropping it does NOT reopen the self-edge — project_trust_not_self, added at the end of this
-- file, closes that shape again. Between the two statements the table is only unguarded against
-- shapes nothing writes, inside a transaction no other session reads.
ALTER TABLE project_trust DROP CONSTRAINT project_trust_ordered;

-- Renames, not new columns: an ALTER ... RENAME COLUMN carries the primary key, both composite
-- foreign keys and the index along with it, so the ON DELETE CASCADE and the two-column FKs of
-- 000007 are preserved as they stand rather than rebuilt and hoped to be identical.
--
-- `low`/`high` were the names of an ORDER; `from`/`to` are the names of a DIRECTION. Keeping the old
-- names over a directed table would have been the cheapest way to let a future reader believe
-- least/greatest still applied.
ALTER TABLE project_trust RENAME COLUMN low_project_id  TO from_project_id;
ALTER TABLE project_trust RENAME COLUMN high_project_id TO to_project_id;

-- The NOT NULL constraints are renamed TOO, and this is not tidiness. Postgres 18 names them after
-- the column (`<table>_<column>_not_null`) and a column rename does NOT carry the name along: left
-- alone, `sql/schema/schema.sql` reads `from_project_id uuid CONSTRAINT
-- project_trust_low_project_id_not_null NOT NULL` — the exact stale vocabulary this migration
-- exists to remove, preserved in the one file that is supposed to be the readable truth of the
-- model.
--
-- The old names are deterministic: 000007 declared those two columns NOT NULL under those two
-- names on every installation, so there is no instance where these statements find nothing. If one
-- ever did, this migration fails inside its transaction and changes nothing — which is the right
-- failure for a doubt about a name.
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_low_project_id_not_null  TO project_trust_from_project_id_not_null;
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_high_project_id_not_null TO project_trust_to_project_id_not_null;

-- THE BACKFILL, AND WHY IT IS TWO ARROWS AND NOT ONE. A pair meant both directions — that is
-- exactly what 000007 wrote down: "the channel is bidirectional by construction". Converting a pair
-- to a single arrow would silently REVOKE one direction on every installed graph, and the loss
-- would surface as a `not found` indistinguishable from every other refusal — the least debuggable
-- failure this product can produce.
--
-- No ON CONFLICT is needed and none is written: the CHECK just dropped guaranteed from < to on
-- every existing row, so no mirror can already exist. The SELECT reads the snapshot taken before
-- this statement, so it never sees the rows it is inserting and cannot loop.
--
-- created_at is CARRIED OVER rather than defaulted: the two arrows are one declaration, made on one
-- day, and dating the mirror to the migration would tell the customer they opened something they
-- never typed.
INSERT INTO project_trust (team_id, from_project_id, to_project_id, created_at)
SELECT team_id, to_project_id, from_project_id, created_at
FROM project_trust;

-- The self-edge stays illegal, and it now needs its own CHECK: the ordering constraint used to
-- close it for free, and it is gone. Without this line `least = greatest` becomes an INSERTABLE
-- shape, and a project could be declared as trusting itself — a row that authorises nothing (the
-- issues path refuses self-addressing anyway) but that the canvas would draw as a loop.
--
-- It is a CHECK and not a convention, for the same reason as issues_not_self (000004:47): a shape
-- the database refuses cannot be written by a caller reaching past every layer above it.
ALTER TABLE project_trust
    ADD CONSTRAINT project_trust_not_self CHECK (from_project_id <> to_project_id);

-- The primary key keeps its NAME (project_trust_pkey) and gains its meaning: it is no longer
-- "one row per pair" but "one row per direction", (team_id, from, to). The name is load-bearing —
-- AllowTrust names it in `ON CONFLICT ON CONSTRAINT project_trust_pkey` to tell "created" from
-- "already allowed" — so it is deliberately NOT renamed.
--
-- The two foreign keys are renamed to match their columns. They are still the composite FKs of
-- 000007 against projects (id, team_id), still ON DELETE CASCADE: a cross-team arrow remains
-- IMPOSSIBLE TO INSERT, not merely absent from results, and a deleted team still takes its graph.
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_low_fk  TO project_trust_from_fk;
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_high_fk TO project_trust_to_fk;

-- The index that supports the second foreign key. The primary key covers from_project_id, so only
-- the inbound side needs one; without it a cascade on projects seq-scans the whole graph. No hot
-- read uses it: CreateIssue probes (team_id, from, to), which is the primary key, in full.
ALTER INDEX project_trust_high_idx RENAME TO project_trust_to_idx;
