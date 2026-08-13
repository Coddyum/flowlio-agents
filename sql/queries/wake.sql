-- The wake watermark, persisted (FLWL-90). The probe answers from memory in steady state (D55); these
-- two queries touch Postgres only on the two rare paths that must survive a cold engine — reading the
-- watermark once when the cache is cold, and writing it when the probe decides on a launch.

-- SaveWakeWatermark records the head the probe last decided on for a project, so the re-wake
-- suppression survives a spun-down or restarted engine. GREATEST keeps it monotonic: a lagging
-- replica or a concurrent probe can never drag the watermark below a head already decided on, so no
-- standing work is re-woken by a stale write.
-- name: SaveWakeWatermark :exec
INSERT INTO wake_watermarks (team_id, project_id, head)
VALUES (@team_id, @project_id, @head)
ON CONFLICT (team_id, project_id) DO UPDATE
SET head       = GREATEST(wake_watermarks.head, EXCLUDED.head),
    updated_at = now();

-- WakeWatermark reads the durable wake watermark of a project, coalesced to 0 when no decision has
-- ever been persisted — exactly the cold-start value the probe expects, so a project the probe has
-- never decided on evaluates its standing work once and then goes quiet.
-- name: WakeWatermark :one
SELECT coalesce((SELECT head FROM wake_watermarks
                 WHERE team_id = @team_id AND project_id = @project_id), 0)::bigint AS head;
