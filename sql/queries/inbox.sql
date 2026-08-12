-- check_inbox renvoie un ÉTAT COURANT, pas un flux. Aucun seau ne dépend du curseur : le curseur
-- ne sert qu'au drapeau `new`. Un événement manqué dégrade un new:true en new:false, jamais une
-- ligne en absence de ligne.
--
-- Le journal n'est jamais lu par un prédicat propre : il est atteint par EXISTS sur un sujet déjà
-- scopé. Il n'existe donc aucune query capable de lire l'activité d'un projet tiers.

-- InboxCursor reads the token cursor AND the head of what is addressed to this project, in one call.
-- The head is captured BEFORE the buckets are computed: any event created during the call stays `new`
-- on the next round.
--
-- The head is a PER-PROJECT relevance head, not the team's activity: max(id) among the events whose
-- notify_project_id is this project — the ones that should actually wake it. A repo answering an issue
-- writes an event addressed to the OTHER party, so its own answer never lifts its own head, and the
-- probe stops waking it for its own writes.
--
-- A NULL notify_project_id was written by an engine that predates this column (the hosted image lags,
-- D29): it carries no target, so it is treated as addressed to everyone rather than dropped. Erring
-- towards a wake is the safe side — a missed wake leaves a real answer unseen, a spurious one costs a
-- cheap empty probe.
-- name: InboxCursor :one
SELECT
    coalesce((SELECT c.last_event_id FROM token_cursors c WHERE c.token_id = @token_id), 0)::bigint
        AS last_event_id,
    coalesce((SELECT max(e.id) FROM events e
              WHERE e.team_id = @team_id
                AND (e.notify_project_id = @project_id::uuid OR e.notify_project_id IS NULL)), 0)::bigint
        AS head_event_id;

-- Seau 1 — needs_answer : quelqu'un est bloqué sur moi.
-- Dans ce seau, le dernier message est toujours celui de l'auteur : ma propre réponse ferait
-- passer l'issue en `answered` et la sortirait du seau.
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

-- Seau 2 — answered : j'étais bloqué, je ne le suis plus. Le dernier message est la réponse.
-- La ref porte la clé du DESTINATAIRE (p.key), pas la mienne : c'est ce que l'agent doit
-- réutiliser dans answer_issue.
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

-- Seau 3 — in_progress : une tâche restée là signale une session interrompue, à reprendre avant
-- d'en ouvrir une nouvelle. Pas de drapeau `new` : c'est mon propre travail.
-- name: ListInProgressTasks :many
SELECT t.number, t.title, t.priority, t.updated_at
FROM tasks t
WHERE t.team_id     = @team_id
  AND t.project_id  = @project_id
  AND t.status      = 'in_progress'
  AND t.archived_at IS NULL
ORDER BY t.updated_at DESC
LIMIT @max_rows::int;

-- Seau 4 — unblocked : j'étais bloquée par une autre tâche du repo, plus maintenant.
--
-- C'est le seau qui répond au manque d'origine : une tâche débloquée qui ne dit rien ne change
-- rien. Comme les trois autres, il est recalculé et non rejoué — la trace durable est
-- `released_at` sur l'arête, pas un événement à consommer une fois.
--
-- Le parcours part des ARÊTES du projet et remonte vers les tâches : un repo a peu d'arêtes et
-- beaucoup de tâches, l'inverse scannerait tout le backlog actif à chaque check_inbox.
--
-- `status IN ('todo','blocked')` est la condition de sortie du seau : reprendre la tâche
-- (in_progress), la finir ou l'archiver l'en retire. `blocked` y reste parce qu'une tâche que
-- l'agent avait bloquée LUI-MÊME ne revient pas à `todo` toute seule — on la notifie quand même,
-- sinon la notification dépendrait de qui a posé le blocage.
-- name: ListUnblockedTasks :many
SELECT t.number, t.title, t.priority, t.status,
       EXISTS (
           SELECT 1 FROM events e
           WHERE e.subject_type = 'task' AND e.subject_id = t.id AND e.id > @last_event_id
       ) AS is_new
FROM (
    SELECT dep.task_id, max(dep.released_at) AS released_at
    FROM task_dependencies dep
    WHERE dep.project_id = @project_id
      AND dep.released_at IS NOT NULL
    GROUP BY dep.task_id
) d
JOIN tasks t ON t.id = d.task_id AND t.team_id = @team_id AND t.project_id = @project_id
WHERE t.archived_at IS NULL
  AND t.status IN ('todo', 'blocked')
  AND NOT EXISTS (
      SELECT 1 FROM task_dependencies pending
      WHERE pending.task_id = t.id AND pending.released_at IS NULL
  )
ORDER BY d.released_at DESC
LIMIT @max_rows::int;

-- GREATEST empêche le curseur de reculer si deux check_inbox concurrents du même token se
-- croisent. Aucune transaction n'est nécessaire : le pire cas est un drapeau `new` perdu.
-- name: AdvanceInboxCursor :exec
INSERT INTO token_cursors (token_id, last_event_id)
VALUES (@token_id, @last_event_id)
ON CONFLICT (token_id) DO UPDATE
SET last_event_id = GREATEST(token_cursors.last_event_id, EXCLUDED.last_event_id),
    updated_at    = now();

-- InboxProjectKey résout la clé du projet du token, nécessaire pour composer les références
-- lisibles (CORE-34) de ses propres tâches et des issues qui lui sont adressées.
-- Scopée par team_id comme toute lecture de projet, même si l'identifiant vient déjà du token.
-- name: InboxProjectKey :one
SELECT key FROM projects WHERE id = $1 AND team_id = $2;
