-- RÈGLE DE SCOPE DE CE FICHIER : project_id sur toute lecture comme sur toute écriture, et
-- team_id partout où une tâche est atteinte par autre chose que son identifiant.
--
-- Une arête ne peut pas relier deux projets : les deux clés étrangères composites de
-- task_dependencies partagent la MÊME colonne project_id, donc la dépendance inter-repos est
-- inexprimable en base, pas seulement refusée par le service (D42). Les prédicats de ce fichier
-- ne sont pas cette garantie — ils empêchent d'ATTEINDRE une arête d'un autre projet, ce qui est
-- une question différente.

-- CreateTaskDependency ouvre une arête « task_id est bloquée par blocker_task_id ».
--
-- project_id est lu depuis la tâche bloquée plutôt que fourni : c'est ce qui fait entrer les deux
-- extrémités dans la même clé étrangère composite. Un blocker d'un autre projet fait alors
-- échouer la contrainte, sans qu'aucun prédicat n'ait à le vérifier.
-- name: CreateTaskDependency :one
INSERT INTO task_dependencies (project_id, task_id, blocker_task_id, until_status, set_blocked)
SELECT t.project_id, t.id, @blocker_task_id, @until_status::task_status, @set_blocked::boolean
FROM tasks t
WHERE t.id = @task_id
  AND t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.archived_at IS NULL
RETURNING *;

-- ReleaseDependenciesOfBlocker libère les arêtes qu'une tâche vient de débloquer en changeant de
-- statut, et rend les tâches concernées.
--
-- « Atteindre » un statut est monotone et non une égalité : une bloquante qui saute de `todo` à
-- `done` libère aussi les arêtes qui n'attendaient que `in_progress`. L'égalité stricte
-- fabriquerait des arêtes que plus rien ne peut libérer — exactement la tâche morte-vivante que
-- cette carte existe pour empêcher.
--
-- `force` couvre l'archivage : une bloquante archivée n'atteindra jamais rien, ses arêtes se
-- libèrent donc quelle que soit leur condition.
-- name: ReleaseDependenciesOfBlocker :many
UPDATE task_dependencies d
SET released_at = now()
WHERE d.blocker_task_id = @blocker_task_id
  AND d.project_id = @project_id
  AND d.released_at IS NULL
  AND (
        @force::boolean
     OR @blocker_status::task_status = 'done'
     OR (@blocker_status::task_status = 'in_progress' AND d.until_status = 'in_progress')
  )
RETURNING d.task_id;

-- ReleaseDependencyPair libère UNE arête nommée, ce que fait unblock_task.
-- Zéro ligne rendue signifie « cette arête n'existe pas, ou elle est déjà libérée » : les deux
-- sont le même non-événement pour l'appelant.
-- name: ReleaseDependencyPair :many
UPDATE task_dependencies d
SET released_at = now()
WHERE d.task_id = @task_id
  AND d.blocker_task_id = @blocker_task_id
  AND d.project_id = @project_id
  AND d.released_at IS NULL
RETURNING d.task_id;

-- ClearTaskBlock ramène une tâche de `blocked` à `todo`, et SEULEMENT si c'est une arête qui l'y
-- avait mise.
--
-- Les trois conditions sont indissociables et tiennent dans la query pour qu'aucune ne puisse
-- être oubliée par un appelant :
--   - status = 'blocked'  : un agent qui a déjà repris la tâche à la main n'est pas écrasé ;
--   - aucune arête active : être libéré par une arête sur trois ne débloque rien ;
--   - au moins un set_blocked : une tâche que l'agent avait bloquée pour une autre raison garde
--     son statut. On notifie, on ne décide pas à sa place.
--
-- Zéro ligne rendue est un résultat normal, pas une erreur : c'est le cas « on notifie et on ne
-- touche pas au statut ».
-- name: ClearTaskBlock :many
UPDATE tasks t
SET status = 'todo', updated_at = now()
WHERE t.id = @task_id
  AND t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.archived_at IS NULL
  AND t.status = 'blocked'
  AND NOT EXISTS (
      SELECT 1 FROM task_dependencies d
      WHERE d.task_id = t.id AND d.released_at IS NULL
  )
  AND EXISTS (
      SELECT 1 FROM task_dependencies d
      WHERE d.task_id = t.id AND d.set_blocked
  )
RETURNING t.number;

-- ListActiveDependencyEdges rend le graphe de blocage ACTIF du projet, et sert à une seule chose :
-- refuser un cycle avant de l'écrire. A bloque B qui bloque A laisserait les deux `blocked` pour
-- toujours, sans que rien ne le dise.
--
-- Le graphe entier plutôt qu'un parcours récursif en SQL, pour deux raisons. La première est que
-- sqlc ne résout pas les colonnes d'une CTE récursive. La seconde vaut mieux que la première : le
-- parcours devient une fonction Go pure, donc prouvable sans Postgres — et « un cycle est refusé »
-- est précisément le genre de garantie qu'on veut voir tenir dans un test qui ne dépend de rien.
--
-- La taille est bornée par la nature de l'objet : ce sont les blocages NON LIBÉRÉS d'un seul repo,
-- pas son historique. Les arêtes libérées sont exclues — elles ne bloquent plus, donc elles ne
-- peuvent pas fermer un cycle.
-- name: ListActiveDependencyEdges :many
SELECT d.task_id, d.blocker_task_id
FROM task_dependencies d
WHERE d.project_id = @project_id
  AND d.released_at IS NULL;
