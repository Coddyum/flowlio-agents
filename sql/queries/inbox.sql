-- check_inbox renvoie un ÉTAT COURANT, pas un flux. Aucun seau ne dépend du curseur : le curseur
-- ne sert qu'au drapeau `new`. Un événement manqué dégrade un new:true en new:false, jamais une
-- ligne en absence de ligne.
--
-- Le journal n'est jamais lu par un prédicat propre : il est atteint par EXISTS sur un sujet déjà
-- scopé. Il n'existe donc aucune query capable de lire l'activité d'un projet tiers.

-- InboxCursor lit le curseur du token ET la tête du journal de la team en une fois. La tête est
-- capturée AVANT le calcul des seaux : tout événement créé pendant l'appel restera `new` au
-- prochain tour.
-- name: InboxCursor :one
SELECT
    coalesce((SELECT c.last_event_id FROM token_cursors c WHERE c.token_id = @token_id), 0)::bigint
        AS last_event_id,
    coalesce((SELECT max(e.id) FROM events e WHERE e.team_id = @team_id), 0)::bigint
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

-- GREATEST empêche le curseur de reculer si deux check_inbox concurrents du même token se
-- croisent. Aucune transaction n'est nécessaire : le pire cas est un drapeau `new` perdu.
-- name: AdvanceInboxCursor :exec
INSERT INTO token_cursors (token_id, last_event_id)
VALUES (@token_id, @last_event_id)
ON CONFLICT (token_id) DO UPDATE
SET last_event_id = GREATEST(token_cursors.last_event_id, EXCLUDED.last_event_id),
    updated_at    = now();
