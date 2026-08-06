-- 000011_project_note_bytes — a project carries the cumulative size of its note thread.
--
-- WHY A COUNTER AND NOT A COUNT. The quota this column serves (FLWL-70, part 5) is checked on the
-- write path of the product's most frequent call: an agent journals its progress on every session.
-- Summing `octet_length(body_md)` over a project's notes on each insert is O(n) on a table that
-- only grows, and the enforcement would get slower exactly as the thing it protects gets bigger.
-- The counter makes the check a single row update, and `ClaimNextNumber` already establishes the
-- pattern of a counter living on the project row.
--
-- LOCK ORDER. Charging takes FOR NO KEY UPDATE on the project row, like ClaimNextNumber, and both
-- paths take it BEFORE touching tasks. Two concurrent transactions therefore lock projects then
-- tasks, never the reverse — which is the property that keeps them from deadlocking.
--
-- THE COUNTER ONLY EVER GROWS, and that is honest rather than sloppy: no note is ever deleted by
-- the product, and what is being bounded is STORAGE, which a host pays for whether or not a row is
-- still interesting.
--
-- Non-destructive: an added column and a backfill of that column alone. No existing value is read
-- back or overwritten.

ALTER TABLE projects ADD COLUMN note_bytes bigint NOT NULL DEFAULT 0;

-- Backfill: a project created before this migration already carries a thread, and starting it at
-- zero would hand it a fresh quota that its history has already partly spent.
UPDATE projects p
SET note_bytes = coalesce((
    SELECT sum(octet_length(n.body_md))
    FROM task_notes n
    JOIN tasks t ON t.id = n.task_id
    WHERE t.project_id = p.id
), 0);
