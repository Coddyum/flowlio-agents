-- 000010_dependency_origin — which writing surface opened the edge, so that only that surface can
-- take it back.
--
-- Two surfaces open a blocking edge, and they do NOT share a lifecycle. `block_task` is an act:
-- nothing undoes it but `unblock_task`. The `#blocked-by @KEY-N until #done` line of a description
-- is text: it disappears the moment anyone rewrites the body, and nothing distinguishes a
-- deliberate removal from a careless copy-paste.
--
-- Without this column the two lifecycles collide, and both answers are wrong. Releasing whenever
-- the line goes missing lets a rewritten description lift a block an agent decided on. Keeping the
-- edge makes the description lie about what the database holds.
--
-- With it, each surface owns what it opened: a body edit releases only what a body edit created,
-- and an edge opened through the API is unreachable from any description (D47).
--
-- Text and a CHECK rather than an enum: the set is closed at two values and will not grow — a
-- dedicated type would only add a migration to the day a third surface appears, which is the day
-- the rule itself has to be reopened anyway.
--
-- Existing rows default to 'api', and that default is the truth rather than a guess: `block_task`
-- was the only writer that could open an edge before this migration.
ALTER TABLE task_dependencies
    ADD COLUMN origin text NOT NULL DEFAULT 'api'
        CONSTRAINT task_dependencies_origin_known CHECK (origin IN ('api', 'body'));
