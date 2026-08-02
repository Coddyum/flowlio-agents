-- Toute lecture et toute écriture porte team_id ET project_id : le scope est DANS la query.
-- Une tâche d'un autre projet est introuvable, pas seulement interdite — et il n'existe aucune
-- query de tâche sans scope, donc aucun appelant ne peut en oublier un.

-- name: CreateTask :one
INSERT INTO tasks (team_id, project_id, number, title, body_md, status, priority, deadline)
VALUES (@team_id, @project_id, @number, @title, @body_md, @status, @priority, sqlc.narg('deadline'))
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks
WHERE team_id = @team_id AND project_id = @project_id AND number = @number;

-- ListTasks sert le backlog du projet courant. Les archivées sont exclues par défaut : un agent
-- qui demande son travail en cours ne doit pas payer en tokens l'historique du repo.
-- name: ListTasks :many
SELECT * FROM tasks
WHERE team_id = @team_id
  AND project_id = @project_id
  AND (@include_archived::boolean OR archived_at IS NULL)
  AND (sqlc.narg('status')::task_status IS NULL OR status = sqlc.narg('status')::task_status)
ORDER BY number DESC
LIMIT @max_rows::int;

-- UpdateTask est un patch : un champ absent (NULL) laisse la valeur en place.
-- `clear_deadline` existe parce que NULL signifie déjà « ne change pas » — sans ce drapeau, il
-- serait impossible d'effacer une échéance.
-- Une tâche archivée n'est pas modifiable : la clause archived_at IS NULL la rend introuvable
-- pour cette query, ce qui remonte le même ErrNotFound qu'un numéro inexistant.
--
-- TRANCHÉ — un patch ne portant QU'UNE NOTE déplace quand même updated_at, et c'est voulu.
-- Une note EST une progression : la tâche remonte en tête du seau `in_progress` de check_inbox,
-- qui trie par updated_at DESC, donc la session suivante retrouve d'abord ce que la précédente
-- a réellement touché en dernier. L'ancien POST /notes ne le déplaçait pas — c'était l'anomalie,
-- pas la référence : il laissait une tâche travaillée pendant une heure au fond de la pile.
-- Ce qui n'allait pas, c'est que ce changement de comportement soit arrivé sans être décidé.
-- L'ARCHIVAGE est un champ de ce patch, et non plus une query à part.
--
-- Il l'était, et l'outil MCP faisait alors DEUX requêtes HTTP : un PATCH puis un POST /archive.
-- L'atomicité gagnée en repliant la note s'arrêtait à cette frontière. Panne entre les deux : la
-- note était commitée, l'archivage non, l'agent lisait `api: internal error` et rejouait — ce qui
-- écrivait la note une SECONDE fois. Doublon silencieux, sur le chemin de fin de tâche.
-- Replié ici, l'appel entier réussit ou échoue d'un bloc : un rejeu écrit la note une fois, ou
-- rend ErrNotFound sur une tâche déjà archivée. Jamais deux.
-- Ce n'est PAS de la déduplication — docs/DECISION-idempotence.md reste en vigueur : on supprime
-- une couture non atomique, on n'ajoute aucune clé d'idempotence.
--
-- Rejouer l'archivage remonte ErrNotFound : la clause archived_at IS NULL ne cible que les tâches
-- encore actives, et c'est le même ErrNotFound qu'un numéro inexistant.
-- name: UpdateTask :one
UPDATE tasks
SET title       = COALESCE(sqlc.narg('title'), title),
    body_md     = COALESCE(sqlc.narg('body_md'), body_md),
    status      = COALESCE(sqlc.narg('status')::task_status, status),
    priority    = COALESCE(sqlc.narg('priority')::task_priority, priority),
    deadline    = CASE
                      WHEN @clear_deadline::boolean THEN NULL
                      ELSE COALESCE(sqlc.narg('deadline'), deadline)
                  END,
    archived_at = CASE WHEN @archive::boolean THEN now() ELSE archived_at END,
    updated_at  = now()
WHERE team_id = @team_id
  AND project_id = @project_id
  AND number = @number
  AND archived_at IS NULL
RETURNING *;

-- L'insertion d'une note passe par un SELECT scopé sur la tâche : impossible d'écrire dans le
-- fil d'une tâche d'un autre projet, même en connaissant son identifiant.
--
-- TRANCHÉ — l'ÉCRITURE n'est PAS bornée, ni en nombre de notes ni en taille cumulée.
-- Le coût qu'il fallait fermer était celui de la LECTURE : un fil non borné remplissait le
-- contexte d'un agent qui reprenait une tâche. C'est fait, dans ListTaskNotes.
-- Refuser une écriture, en revanche, coûterait la trace au moment précis où elle vaut le plus :
-- le fil est le journal que relira la session suivante, et un agent qui en écrit beaucoup est un
-- agent en difficulté, pas un attaquant — l'auteur est authentifié et enfermé dans un seul projet.
-- La réponse à un agent en boucle est de le voir, pas de lui murer la porte au milieu de son
-- explication.
-- Ce qui rouvrirait la question : le mode hosted, où le stockage est facturé à un tiers. C'est un
-- quota par projet, donc un sujet de M7, pas une borne à deviner ici.
-- name: CreateTaskNote :one
INSERT INTO task_notes (task_id, body_md)
SELECT t.id, @body_md
FROM tasks t
WHERE t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.number = @number
  AND t.archived_at IS NULL
RETURNING *;

-- ListTaskNotes rend la FIN du fil, bornée, et son total.
--
-- Sans LIMIT, `get CORE-34` sérialisait le fil entier : mesuré sur la base de dev, 1 000 notes de
-- 64 KiB donnaient 62,6 Mio en 669 ms. C'est l'outil qu'un agent appelle pour reprendre une
-- tâche : un fil non borné, c'est un appel qui remplit son contexte entier sur une lecture qu'il
-- croyait anodine. Le dépôt borne partout ailleurs pour cette raison exacte — list_tasks exclut
-- la description, list_issues plafonne à 100, check_inbox à 10 lignes et 500 caractères, et le fil
-- d'une issue à 10 messages avec son total. Le fil de notes était le seul à y échapper.
--
-- `count(*) OVER ()` est évalué APRÈS le WHERE et AVANT le LIMIT : le total est exact et coûte le
-- même aller-retour. Une seconde query de comptage aurait ajouté un aller-retour au chemin de
-- lecture le plus appelé du produit.
--
-- ORDER BY DESC parce que ce sont les DERNIÈRES notes qui portent l'état ; l'appelant remet le fil
-- dans l'ordre d'écriture, qui est celui dans lequel un journal se lit.
-- name: ListTaskNotes :many
SELECT n.*, count(*) OVER () AS total
FROM task_notes n
JOIN tasks t ON t.id = n.task_id
WHERE t.team_id = @team_id
  AND t.project_id = @project_id
  AND t.number = @number
ORDER BY n.created_at DESC, n.id DESC
LIMIT @lim;
