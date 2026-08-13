-- 000017_wake_watermark — the wake watermark, made durable (FLWL-90).
--
-- The probe suppresses a re-wake by remembering the head it last decided on: the wake watermark
-- (FLWL-85/86). That watermark lived ONLY in the engine's in-process cache. Correct for a process
-- that stays up; wrong for a hosted engine that Render spins down between polls (and may run as
-- several replicas, each with its own cache). Every cold start read the watermark as 0 and
-- re-decided ALL standing work, so any open or answered issue re-woke the repo on every poll — a
-- perpetual void wake that no amount of FLWL-85/86 could stop, because both rest on that one
-- in-memory scalar. Persisting it makes the suppression survive a cold cache: a restarted probe
-- reads the real last-decided head instead of 0.
--
-- Keyed per (team, project), matching the per-project relevance head the watermark is compared with.
-- Additive and non-destructive. The probe writes this row only on the has-work path and reads it only
-- on a cold cache, so the zero-SQL idle steady state (D55) is untouched. No backfill: a missing row
-- reads as 0, which is exactly the pre-000017 cold-start value — the first post-deploy probe of a
-- project decides its standing work once and writes the row, then goes quiet.
--
-- The FK mirrors events_notify_fk: (project_id, team_id) against the projects(id, team_id) unique
-- key, cascading so a removed project takes its watermark with it.
CREATE TABLE wake_watermarks (
    team_id    uuid        NOT NULL,
    project_id uuid        NOT NULL,
    head       bigint      NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, project_id),
    CONSTRAINT wake_watermarks_project_fk
        FOREIGN KEY (project_id, team_id) REFERENCES projects(id, team_id) ON DELETE CASCADE
);
