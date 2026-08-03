-- overview — SEUL fichier du dépôt dont les queries lisent des lignes métier SANS prédicat de
-- projet. RÈGLE INVERSE de tasks.sql et issues.sql, et c'est exactement pourquoi elles vivent
-- ici : voisiner une query team-seule et une query project-scopée sur les mêmes tables est la
-- configuration où le copier-coller fuit (décision M3 #24). La query dangereuse doit être
-- physiquement loin de la query sûre.
--
-- Quatre invariants, aucun n'est optionnel :
--   1. @team_id vient d'une RÉSOLUTION SERVEUR (OverviewTeamBySlug sur ?team=), jamais d'un
--      identifiant fourni par le client, jamais d'un UUID en entrée.
--   2. Chaque query porte `team_id = @team_id`, y compris dans CHAQUE join vers projects, même
--      quand une FK composite le rend redondant : on doit pouvoir grep `FROM issues` et vérifier
--      chaque occurrence sans raisonner sur le contexte. Exception unique et nommée :
--      OverviewTeamBySlug, qui PRODUIT le scope.
--   3. LECTURE SEULE. Aucun INSERT, UPDATE, DELETE, jamais. Vérifié par
--      scripts/check-overview-scope.sh, dans make lint.
--   4. Aucune de ces queries n'est exposée en MCP. Un agent qui lit l'état de sa team détruit la
--      promesse d'isolation du produit, en lecture, sans qu'aucun test de tenancy ne tombe.
--
-- Règle de typage sqlc, vérifiée en exécutant sqlc 1.30 et non déduite : un cast `::timestamptz`
-- déclare la colonne NOT NULL. Ne l'écrire QUE sur une expression qui ne peut pas être NULL. Une
-- agrégation min()/max() sur un ensemble possiblement vide sort dans sa propre query GROUP BY
-- (cf. OverviewLastSeen) — sinon le Scan échoue sur une team saine, ou sqlc produit un
-- interface{}, interdit par code-conventions.md et par la décision M3 #3.

-- OverviewTeamBySlug résout le scope. Seule query du fichier sans team_id, et elle ne renvoie que
-- l'identité de la team : elle ne peut pas devenir une fuite en grossissant. Ne jamais y ajouter
-- de colonne. Slug inconnu ⇒ zéro ligne ⇒ 404.
-- name: OverviewTeamBySlug :one
SELECT id, slug, name FROM teams WHERE slug = @slug;

-- OverviewProjects — une ligne par repo, TOUJOURS, y compris un repo sans rien en vol.
--
-- Sous-requêtes scalaires corrélées, et surtout PAS un JOIN vers issues ou tasks : un JOIN ferait
-- DISPARAÎTRE de l'écran du superviseur le repo qui n'a rien en vol. C'est la pire panne possible
-- ici, parce qu'elle est silencieuse. Une sous-requête scalaire d'agrégat renvoie toujours une
-- valeur, donc la propriété est structurelle.
--
-- Chaque sous-requête dérive son scope de p.team_id — la ligne déjà matchée — et non du paramètre.
-- Les count() ne sont jamais NULL ; le ::bigint ne sert qu'à fixer le type Go en int64.
--
-- Aucun LIMIT : la liste des projets n'est jamais tronquée.
-- name: OverviewProjects :many
SELECT
    p.key,
    (SELECT count(*) FROM issues i
       WHERE i.team_id = p.team_id AND i.project_id = p.id
         AND i.state = 'open')::bigint                                    AS owes_answer,
    (SELECT count(*) FROM issues i
       WHERE i.team_id = p.team_id AND i.author_project_id = p.id
         AND i.state = 'open')::bigint                                    AS awaiting_answer,
    (SELECT count(*) FROM issues i
       WHERE i.team_id = p.team_id AND i.author_project_id = p.id
         AND i.state = 'answered')::bigint                                AS answered_unread,
    (SELECT count(*) FROM tasks t
       WHERE t.team_id = p.team_id AND t.project_id = p.id
         AND t.archived_at IS NULL AND t.status = 'in_progress')::bigint  AS tasks_running,
    (SELECT count(*) FROM tasks t
       WHERE t.team_id = p.team_id AND t.project_id = p.id
         AND t.archived_at IS NULL AND t.status = 'blocked')::bigint      AS tasks_blocked
FROM projects p
WHERE p.team_id = @team_id
ORDER BY p.key;

-- OverviewLastSeen — le pouls d'un repo : dernier appel authentifié d'un de ses tokens.
--
-- Query SÉPARÉE avec GROUP BY, et non une sixième sous-requête de OverviewProjects. Raison
-- exacte : tokens.last_used_at est NULLABLE, donc max() sur un projet dont aucun token n'a
-- jamais servi renvoie NULL, et sqlc typerait la colonne time.Time NOT NULL — le Scan échouerait
-- sur un repo neuf, c'est-à-dire le cas nominal du premier jour. Le WHERE ... IS NOT NULL garantit
-- au moins une ligne par groupe, ce qui rend le cast honnête. Le service fusionne par clé ; un
-- projet absent du résultat n'a simplement pas de pouls.
--
-- Un token admin a project_id NULL : le JOIN l'exclut, ce qui est voulu — l'humain n'est pas un
-- agent, et son propre trafic ne doit pas faire paraître un repo vivant.
--
-- `@team_id::uuid` et non `@team_id` nu : tokens.team_id est NULLABLE depuis 000006 (un token
-- admin ne porte pas de team), donc sqlc typait le PARAMÈTRE en uuid.NullUUID — seule query du
-- fichier dans ce cas. Un scope de tenancy qu'un appelant peut passer à NULL n'a rien à faire ici,
-- même si `= NULL` ne matcherait rien : ce qui doit être impossible doit être inexprimable. Le
-- cast rend la signature identique aux huit autres.
-- name: OverviewLastSeen :many
SELECT p.key, max(tk.last_used_at)::timestamptz AS last_seen
FROM tokens tk
JOIN projects p ON p.id = tk.project_id AND p.team_id = tk.team_id
WHERE tk.team_id      = @team_id::uuid
  AND tk.revoked_at   IS NULL
  AND tk.last_used_at IS NOT NULL
GROUP BY p.key;

-- OverviewIssueDebts — toute issue en vol de la team, la PLUS VIEILLE d'abord.
--
-- ORDER BY updated_at ASC est l'inverse de ListIssues (DESC) et c'est délibéré : un agent veut ce
-- qui est frais, un superviseur veut ce qui pourrit. « Quelle question traîne depuis trois jours »
-- doit être la première ligne, pas la cinquantième. Ne pas « corriger » en DESC.
--
-- Ni corps ni extrait : cinquante extraits ne se lisent pas en trois secondes.
--
-- count(*) OVER () compte AVANT le LIMIT et évite un second aller-retour. Il force la
-- matérialisation du jeu complet : acceptable à l'échelle d'une team.
-- name: OverviewIssueDebts :many
SELECT i.number, i.state, i.title, i.updated_at,
       p.key AS project_key,
       a.key AS author_project_key,
       (count(*) OVER ())::bigint AS total
FROM issues i
JOIN projects p ON p.id = i.project_id        AND p.team_id = i.team_id
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
WHERE i.team_id = @team_id
  AND i.state  <> 'closed'
ORDER BY i.updated_at ASC, i.number ASC
LIMIT @max_rows::int;

-- OverviewTaskDebts — les tâches sur lesquelles un humain peut agir, et rien d'autre.
--
-- last_move corrige un piège de M2 : CreateTaskNote n'écrit PAS tasks.updated_at. Sans le max()
-- sur les notes, un agent qui documente activement sa progression serait signalé « session
-- morte ». Le coalesce sur t.updated_at (NOT NULL) rend l'expression non-nullable, ce qui est la
-- seule condition qui autorise le cast ::timestamptz.
--
-- Table dérivée et non alias en WHERE : SQL n'autorise pas à filtrer sur un alias de SELECT, et
-- répéter l'expression greatest() deux fois serait la première divergence à apparaître.
--
-- @stale_before est calculé en Go (now - 24 h). L'horloge appartient au service, le scope à la
-- query : le test d'intégration devient déterministe et le seuil se règle sans migration.
--
-- has_open_question distingue « bloqué et il a demandé » de « bloqué et il n'a rien demandé ». Le
-- second est le seul cul-de-sac que ni MCP, ni la CLI, ni le terminal de l'agent ne rendent
-- visible : c'est lui qui justifie ce jalon.
--
-- DEUX ÉCARTS AVEC LE LITTÉRAL DE docs/DESIGN-TUI.md, tous deux imposés par sqlc 1.30 et isolés
-- par bisection, pas devinés :
--
--   1. table dérivée au lieu d'une CTE `WITH candidate AS (…)`. L'analyseur de sqlc n'enregistre
--      pas l'alias de la CTE et rend `table alias "c" does not exist` — sur la statement SUIVANTE,
--      ce qui rend le message doublement trompeur.
--   2. `@stale_before::timestamptz` et non `@stale_before` nu. Sans le cast, sqlc n'infère pas le
--      type du paramètre à travers la table dérivée et rend la MÊME erreur d'alias. Vérifié :
--      la comparaison seule échoue, le cast seul suffit à la faire passer.
--
-- Postgres accepte les deux formes d'origine ; ce sont des limites de l'outil, pas du SQL. La
-- sémantique est inchangée.
-- name: OverviewTaskDebts :many
SELECT c.number, c.status, c.priority, c.title, c.deadline,
       c.project_key,
       c.last_move::timestamptz AS last_move,
       c.has_open_question,
       (count(*) OVER ())::bigint AS total
FROM (
    SELECT t.number, t.status, t.priority, t.title, t.deadline,
           p.key AS project_key,
           greatest(
               t.updated_at,
               coalesce((SELECT max(n.created_at) FROM task_notes n WHERE n.task_id = t.id),
                        t.updated_at)
           ) AS last_move,
           EXISTS (SELECT 1 FROM issues i
                    WHERE i.team_id           = @team_id
                      AND i.author_project_id = t.project_id
                      AND i.state             = 'open') AS has_open_question
    FROM tasks t
    JOIN projects p ON p.id = t.project_id AND p.team_id = t.team_id
    WHERE t.team_id     = @team_id
      AND t.archived_at IS NULL
      AND t.status IN ('in_progress', 'blocked')
) c
WHERE c.status = 'blocked' OR c.last_move < @stale_before::timestamptz
ORDER BY c.last_move ASC, c.number ASC
LIMIT @max_rows::int;

-- OverviewIssueByRef — le fil, vu par un tiers.
--
-- DIFFÉRENCE VOLONTAIRE avec GetIssueByRef (sql/queries/issues.sql) : la clause de visibilité
--     AND (i.project_id = @project_id OR i.author_project_id = @project_id)
-- est ABSENTE. C'est la capacité nouvelle — lire une conversation WEB→CORE sans être ni l'un ni
-- l'autre — et c'est exactement ce qui rend cette query interdite à tout principal de portée
-- projet. Deux tests l'encadrent : l'un prouve que la capacité existe, l'autre que la team la
-- borne. Sans le premier, le second passerait sur une query qui ne renvoie jamais rien.
-- name: OverviewIssueByRef :one
SELECT i.id, i.number, i.state, i.title, i.created_at, i.updated_at,
       p.key AS project_key,
       a.key AS author_project_key
FROM issues i
JOIN projects p ON p.id = i.project_id        AND p.team_id = i.team_id
JOIN projects a ON a.id = i.author_project_id AND a.team_id = i.team_id
WHERE i.team_id = @team_id
  AND p.key     = @project_key
  AND i.number  = @number;

-- OverviewIssueMessages — les N derniers messages, rendus dans l'ordre de lecture.
--
-- `ap.team_id = i.team_id` est LOAD-BEARING ici, contrairement aux joins de OverviewIssueByRef.
-- issue_messages n'a pas de team_id (décision M3 #16) et sa FK vers projects est SIMPLE —
-- issue_messages_author_project_id_fkey → projects(id) — pas composite. Rien au niveau du schéma
-- n'empêche author_project_id de pointer un projet d'une autre team. C'est la SEULE occurrence du
-- dépôt où retirer cette clause est observable, donc la seule qui se teste. Ailleurs, le contrôle
-- réel est la FK composite, et c'est la FK qu'il faut tester.
--
-- Le sous-select DESC + LIMIT puis ORDER BY ASC renvoie les N plus RÉCENTS dans l'ordre de
-- lecture. Un simple ASC + LIMIT couperait la queue du fil, c'est-à-dire la réponse.
-- name: OverviewIssueMessages :many
SELECT tail.author_key, tail.body_md, tail.created_at, tail.total
FROM (
    SELECT ap.key AS author_key, m.body_md, m.created_at, m.id,
           (count(*) OVER ())::bigint AS total
    FROM issue_messages m
    JOIN issues   i  ON i.id  = m.issue_id          AND i.team_id  = @team_id
    JOIN projects ap ON ap.id = m.author_project_id AND ap.team_id = i.team_id
    WHERE m.issue_id = @issue_id
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT @max_rows::int
) tail
ORDER BY tail.created_at, tail.id;

-- OverviewTaskByRef — une tâche désignée par CORE-34, sans le token de son repo.
--
-- Le filtre archived_at IS NULL est le même que celui des dettes : une tâche archivée n'appelle
-- plus d'action, et l'ouvrir depuis l'aperçu n'aurait pas de sens.
-- name: OverviewTaskByRef :one
SELECT t.id, t.number, t.status, t.priority, t.title, t.body_md, t.deadline,
       t.created_at, t.updated_at,
       p.key AS project_key
FROM tasks t
JOIN projects p ON p.id = t.project_id AND p.team_id = t.team_id
WHERE t.team_id     = @team_id
  AND p.key         = @project_key
  AND t.number      = @number
  AND t.archived_at IS NULL;

-- OverviewTaskNotes — les notes de progression. C'est la vraie réponse à « pourquoi c'est
-- bloqué », et c'est le seul endroit du produit où un humain la lit sans le token du repo.
-- Le join sur tasks porte le team_id : task_notes n'en a pas, comme issue_messages.
-- name: OverviewTaskNotes :many
SELECT tail.body_md, tail.created_at, tail.total
FROM (
    SELECT n.body_md, n.created_at, n.id,
           (count(*) OVER ())::bigint AS total
    FROM task_notes n
    JOIN tasks t ON t.id = n.task_id AND t.team_id = @team_id
    WHERE n.task_id = @task_id
    ORDER BY n.created_at DESC, n.id DESC
    LIMIT @max_rows::int
) tail
ORDER BY tail.created_at, tail.id;
