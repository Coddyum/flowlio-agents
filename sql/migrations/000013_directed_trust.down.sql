-- DESTRUCTIVE, AND NOT IN THE WAY A ROLLBACK USUALLY IS: this one does not merely lose a column,
-- it loses AUTHORISATIONS THAT WERE DELIBERATELY DECLARED, and it cannot do otherwise.
--
-- A pair says "both directions or neither". A graph holding `web → core` WITHOUT `core → web` has
-- no representation on the other side of this file. There are exactly two ways to render it, and
-- both are wrong:
--
--   - widen it into the pair CORE↔WEB, which GRANTS core the right to question web — an
--     authorisation nobody typed, arriving silently, in the one table whose whole point is that
--     nothing is authorised unless it was written down;
--   - drop it, which REVOKES web's right to question core — the customer declared it, and after the
--     rollback their agent gets `not found` with nothing anywhere saying why.
--
-- THIS FILE DROPS. An allow-list may only ever fail closed: a rollback that hands out a channel is
-- a security regression applied by an operator who believed they were undoing one. The loss is
-- loud — it surfaces as a refusal on the first attempt — where the widening would have been silent
-- and permanent.
--
-- WHAT SURVIVES: every pair whose two arrows both exist, that is every graph that was declared
-- before 000013 and never edited under the directed model. Such an installation rolls back with no
-- loss at all.
--
-- WHAT DOES NOT: every one-directional arrow, and there is no record of it anywhere else — not in
-- an event, not in a log. WRITE THE GRAPH DOWN BEFORE RUNNING THIS: `flowlio trust list --team X`
-- prints it with its directions, and it is the only copy that will exist afterwards.
--
-- MANDATORY ORDER — same as 000007's down file, and for the same reason: redeploy the binary from
-- BEFORE 000013 BEFORE running this. The later code reads from_project_id/to_project_id; leaving it
-- running against the reverted columns fails every create_issue on `column "from_project_id" does
-- not exist` (42703), that is in 500, not in 404.

-- Step 1 — drop every arrow whose mirror is missing. This is the loss described above, and it comes
-- FIRST so that step 2 cannot mistake a one-directional arrow for the canonical half of a pair.
--
-- The subquery reads the snapshot taken before this statement, so both halves of a genuine pair see
-- each other and neither is removed. A statement that deleted as it went would take one half, then
-- find the other orphaned, and empty the graph.
DELETE FROM project_trust t
WHERE NOT EXISTS (
    SELECT 1 FROM project_trust m
    WHERE m.team_id         = t.team_id
      AND m.from_project_id = t.to_project_id
      AND m.to_project_id   = t.from_project_id
);

-- Step 2 — collapse each surviving pair back to its single canonical row. What is left after step 1
-- is exclusively mirrored arrows, so this drops exactly one row per pair and loses nothing further.
DELETE FROM project_trust WHERE from_project_id > to_project_id;

-- The self-edge CHECK goes before the ordering CHECK is restored: `from < to` implies inequality,
-- so keeping both would be one constraint stating what the other already refuses, and a second
-- error path for one illegal shape.
ALTER TABLE project_trust DROP CONSTRAINT project_trust_not_self;

ALTER TABLE project_trust RENAME COLUMN from_project_id TO low_project_id;
ALTER TABLE project_trust RENAME COLUMN to_project_id   TO high_project_id;

-- The NOT NULL constraint names go back with their columns, so a rollback leaves a schema dump
-- byte-identical to the one 000012 produced. A rollback that leaves a trace is a rollback nobody
-- can verify.
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_from_project_id_not_null TO project_trust_low_project_id_not_null;
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_to_project_id_not_null   TO project_trust_high_project_id_not_null;

-- Restored last, and it is what proves the two DELETEs above were complete: if a mirror or a
-- one-directional arrow had survived, this statement raises 23514 and the whole rollback rolls
-- back. The constraint is the check on the data, not a comment about it.
ALTER TABLE project_trust
    ADD CONSTRAINT project_trust_ordered CHECK (low_project_id < high_project_id);

ALTER TABLE project_trust RENAME CONSTRAINT project_trust_from_fk TO project_trust_low_fk;
ALTER TABLE project_trust RENAME CONSTRAINT project_trust_to_fk   TO project_trust_high_fk;

ALTER INDEX project_trust_to_idx RENAME TO project_trust_high_idx;
