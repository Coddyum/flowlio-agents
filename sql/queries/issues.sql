-- RÈGLE DE SCOPE DE CE FICHIER : team_id ET project_id, comme dans `tasks.sql`. C'est la première
-- des deux règles du dépôt ; la seconde — team_id SEUL, en lecture, derrière AdminOnly — vit
-- exclusivement dans `overview.sql`. Aucune query team-seule n'entre ici. Tableau complet des
-- quatre situations (dont `projects.sql` et `teams.sql`, hors des deux règles) :
-- docs/ARCHITECTURE.md § The repository's two scoping rules.
--
-- Le scope est DANS la query, sans exception. La clause de visibilité canonique est
--   team_id = @team_id AND (project_id = @project_id OR author_project_id = @project_id)
-- où @project_id vient EXCLUSIVEMENT de Principal.ProjectID. `team_id` y figure toujours, même
-- s'il est redondant avec le projet : c'est la défense en profondeur si le projet provenait un
-- jour d'une mauvaise résolution.
--
-- Ni auteur ni destinataire ⇒ zéro ligne, strictement indiscernable d'un numéro inexistant. Il
-- n'existe aucun 403 sur une clé d'issue, donc aucun oracle permettant d'énumérer le backlog
-- d'un repo frère par answer_issue("CORE-1"), ("CORE-2"), …

-- CreateIssue résout le destinataire, réserve son numéro et insère l'issue en UNE instruction.
--
-- Une clé inconnue — ou connue mais appartenant à une autre team — ne matche pas la CTE, donc
-- l'INSERT ne produit rien ET aucun numéro n'est consommé. C'est ce qui empêche de faire avancer
-- le compteur d'un projet tiers en le devinant, et ce qui rend « inexistant » et « hors team »
-- indistinguables sans que le code ait à s'en préoccuper.
--
-- Cette instruction doit être la PREMIÈRE de sa transaction : c'est la seule prise de verrou de
-- ligne longue durée du chemin d'écriture, et il ne doit y en avoir qu'une (cf. ClaimNextNumber).
--
-- Le graphe de confiance est un PRÉDICAT, dans le WHERE de l'UPDATE, et NULLE PART AILLEURS —
-- ni dans le SELECT de l'INSERT, ni dans une query séparée, ni dans un `if` de service. Trois
-- propriétés en découlent, dont aucune n'est reproductible par du code :
--
--   1. Une direction non autorisée ne matche pas la CTE, exactement comme une clé inconnue ou une
--      clé d'une autre team : zéro ligne, sql.ErrNoRows, ErrNotFound, 404. Le refus n'a AUCUN
--      chemin de code à lui, donc il n'existe aucun code d'erreur à faire fuir.
--   2. Aucun numéro n'est consommé et aucun verrou de ligne n'est posé, parce que l'UPDATE ne
--      s'exécute pas. Mesuré : avec le prédicat déplacé sur l'INSERT, un créateur LÉGITIME tiers
--      passe de 73 ms à 1933 ms, parce que la session refusée détient le verrou de la ligne
--      projet pendant toute sa transaction. Le refus deviendrait un déni de service ciblé.
--   3. L'EXISTS est une LECTURE : l'UPDATE continue de ne toucher que next_number, colonne
--      non-clé, donc FOR NO KEY UPDATE est préservé et la contrainte de verrouillage de
--      sql/queries/projects.sql:21-25 tient telle quelle. Ne jamais transformer cet EXISTS en
--      jointure ni en écriture sur project_trust.
--
-- L'ARÊTE EST DIRIGÉE depuis 000013, et ce prédicat est le seul endroit où sa direction est lue.
-- `from_project_id = @author_project_id AND to_project_id = p.id` : l'arête `web → core` autorise
-- WEB À OUVRIR UNE QUESTION CHEZ CORE, et rien d'autre. La réciproque `core → web` est une seconde
-- ligne, que le client déclare ou ne déclare pas. Les deux côtés du couple ne sont plus liés :
-- écrire ici least/greatest, ou un OR sur les deux sens, rendrait au produit le graphe non orienté
-- que 000013 remplace, sans qu'aucune contrainte ne s'y oppose.
--
-- L'auto-adressage donne from = to, forme que project_trust_not_self (000013) rend non insérable,
-- donc jamais présente : il produit le même 404 que tout le reste, sans branche dédiée. La CHECK
-- issues_not_self (000004:47) devient de ce fait inatteignable par ce chemin, et le reste comme
-- second tour de clé si le prédicat disparaissait un jour.
--
-- FENÊTRE CONNUE, non fermée en v1 (arbitrage Maxence du 2026-08-03) : sous READ COMMITTED, un
-- create_issue qui BLOQUE sur le verrou de la ligne projet re-vérifie la ligne cible
-- (EvalPlanQual) mais évalue cet EXISTS avec son snapshot d'origine. Une révocation qui commite
-- pendant ce blocage laisse donc passer cette issue-là. Reproduit à trois sessions. Le correctif
-- testé est `FOR KEY SHARE` à la fin de l'EXISTS : il sérialise la révocation derrière les
-- créations en vol, au prix d'un nouvel ordre de verrous. Non appliqué parce que Q4 (retirer une
-- confiance ne ferme aucun fil) accepte déjà exactement ce résidu : une issue de plus dans un fil
-- ouvert. La garantie s'énonce donc « une paire non autorisée AU MOMENT OÙ SA TRANSACTION PREND
-- SON SNAPSHOT ne peut pas ouvrir d'issue ».
-- name: CreateIssue :one
WITH claimed AS (
    UPDATE projects p
    SET next_number = p.next_number + 1
    WHERE p.team_id = @team_id
      AND p.key     = @to_project_key
      AND EXISTS (
          SELECT 1 FROM project_trust tr
          WHERE tr.team_id         = @team_id
            AND tr.from_project_id = @author_project_id::uuid
            AND tr.to_project_id   = p.id
      )
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

-- GetIssueByRef résout CORE-34 pour un appelant donné. Le projet est désigné par sa CLÉ, jamais
-- par un UUID : un agent n'en manipule pas, donc il ne peut pas en injecter un.
-- name: GetIssueByRef :one
SELECT i.*, p.key AS project_key, a.key AS author_project_key
FROM issues i
JOIN projects p ON p.id = i.project_id        AND p.team_id = i.team_id
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
WHERE i.team_id = @team_id
  AND p.key     = @project_key
  AND i.number  = @number
  AND (i.project_id = @caller_project_id OR i.author_project_id = @caller_project_id);

-- ListIssueMessages rend la FIN du fil, bornée, et son total.
--
-- Le fil est scopé par jointure sur son issue : impossible de lire les messages d'une issue
-- qu'on ne voit pas, même en connaissant son identifiant.
--
-- LA BORNE EST ICI, PAS EN MÉMOIRE. Le service tranchait le résultat après coup : la base
-- sérialisait donc le fil ENTIER, le transportait, et Go en jetait tout sauf les dix derniers
-- messages. Le contexte de l'agent était protégé ; ni la base, ni le réseau, ni le tas du process
-- ne l'étaient. Sur un fil d'issue les corps sont COMPLETS — c'est la restitution la plus lourde
-- du produit, et le seul chemin où du texte écrit par un tiers arrive sans troncature.
--
-- Même patron que ListTaskNotes (tasks.sql), pour la même raison et au même endroit.
--
-- `count(*) OVER ()` est évalué APRÈS le WHERE et AVANT le LIMIT : le total est exact et coûte le
-- même aller-retour. Une seconde query de comptage en aurait ajouté un sur un chemin de lecture
-- que get(ref) emprunte à chaque reprise de conversation.
--
-- ORDER BY DESC parce que ce sont les DERNIERS messages qui portent l'état de la discussion ;
-- le store remet le fil dans l'ordre d'écriture, qui est celui dans lequel une conversation se lit.
-- name: ListIssueMessages :many
SELECT m.body_md, m.created_at, ap.key AS author_key, count(*) OVER () AS total
FROM issue_messages m
JOIN issues i    ON i.id  = m.issue_id
JOIN projects ap ON ap.id = m.author_project_id
WHERE i.team_id = @team_id
  AND i.id      = @issue_id
  AND (i.project_id = @caller_project_id OR i.author_project_id = @caller_project_id)
ORDER BY m.created_at DESC, m.id DESC
LIMIT @lim;

-- Une seule query pour les trois cas de rôle : trois queries seraient trois occasions de
-- re-sécuriser. `role` n'est JAMAIS une autorisation — c'est une restriction posée par-dessus la
-- clause de visibilité complète, qui reste inconditionnelle.
-- Filtre d'état par sqlc.narg et jamais par une sentinelle '' castée en enum : SQL ne garantit
-- aucun court-circuit sur OR, et ''::issue_state lève un 22P02 selon le plan choisi.
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

-- AnswerIssue ajoute un message ET applique la transition d'état en UNE instruction.
--
-- Deux statements séparés laisseraient passer ceci : l'appelant poste son message, le
-- correspondant ferme l'issue, la transition ne matche plus — le message existe dans une issue
-- close, updated_at n'a pas bougé, et l'inbox (dérivée de l'état) ne le montrera jamais. Une
-- réponse écrite qui disparaît.
--
-- L'état n'est jamais un paramètre : il est calculé depuis QUI parle. Un agent ne peut pas
-- mentir sur l'état qu'il produit.
--   - close = true                → closed (les deux participants peuvent fermer, c'est terminal)
--   - message du destinataire     → answered
--   - message de l'auteur         → open (la relance remet le destinataire en dette)
--
-- `AND i.state <> 'closed'` est non négociable : sans lui, répondre à une issue fermée la
-- ressuscite. `closed_at` n'est jamais écrasé par NULL : un CASE ... ELSE NULL effacerait la
-- date de clôture à chaque message.
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

-- AppendEvent returns the id it wrote: the caller uses it to bump the in-memory probe head (D55,
-- docs/DESIGN-WAKE.md §3), so a sleeping sibling can be woken without a query. The durable record
-- (the row) and the in-memory hint (the head) are the same fact.
-- name: AppendEvent :one
INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
VALUES (@team_id, @project_id, @actor_project_id, @notify_project_id, @kind, @subject_type, @subject_id)
RETURNING id;
