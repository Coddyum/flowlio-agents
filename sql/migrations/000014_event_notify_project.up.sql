-- The wake probe compared a single team-wide journal head against the token cursor: any event by any
-- repo bumped it, so a repo that answered an issue woke itself on the next probe (its own event sat
-- above its cursor). notify_project_id records WHICH project an event should wake — the same party the
-- push transport already signals (create -> recipient, answer -> the other party, task unblock -> the
-- repo itself). The probe head then becomes per-project relevance instead of team-wide activity.
--
-- Backfilled to project_id for the rows already there: those events are all below every live cursor,
-- so the value only has to satisfy NOT NULL and the FK, never to wake anyone. New writes carry the
-- real notify target. The column is NOT NULL so the warm-cache path never has to reason about an
-- unaddressed event.
ALTER TABLE events ADD COLUMN notify_project_id uuid;

UPDATE events SET notify_project_id = project_id;

ALTER TABLE events ALTER COLUMN notify_project_id SET NOT NULL;

ALTER TABLE events ADD CONSTRAINT events_notify_fk
    FOREIGN KEY (notify_project_id, team_id) REFERENCES projects(id, team_id) ON DELETE CASCADE;

-- The exact shape the per-project head query reads: max(id) for one team and one notify target.
CREATE INDEX events_notify_idx ON events (team_id, notify_project_id, id);
