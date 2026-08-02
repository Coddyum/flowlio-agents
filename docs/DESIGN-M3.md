# DESIGN M3 — issues inter-projets + journal d'événements

> Note de conception produite le 2026-08-02 par un fan-out d'agents (cinq angles indépendants,
> deux critiques adversariales, une synthèse), **avant** écriture du code. Elle complète
> `DESIGN-V1.md`, qui reste le contrat de périmètre de la v1.
>
> Statut : décisions **appliquées** par l'implémentation de M3. Ce document est la référence pour
> comprendre POURQUOI le modèle a cette forme. Les écarts constatés entre ce document et le code
> se corrigent dans le code, ou se documentent ici avec leur raison.


> État réel du dépôt au moment de cette note (vérifié) : **M1 et M2 sont commités** (`abae3e2`, `5021f58`). `internal/feature/task/**` est livré avec son `Transactor` (`store/tx.go`), `ClaimNextNumber` renvoie déjà un `int64` (cast `::bigint` présent dans `sql/queries/projects.sql`), `projects_id_team_unique UNIQUE (id, team_id)` existe depuis `000003`, `requireProjectScope` est un middleware local de `task/module.go`, et **le serveur MCP existe déjà** (`cmd/flowlio/mcp.go`, `mcp_tools.go`, `mcp_call.go` — JSON-RPC 2.0 écrit à la main, zéro dépendance, 6 outils). Toute recommandation qui suppose l'inverse est caduque et n'apparaît pas ci-dessous.

---

## Décisions tranchées

| # | Décision | Conséquence |
| - | -------- | ----------- |
| 1 | **`check_inbox` ne renvoie PAS un flux d'événements.** Il renvoie l'**état courant actionnable** en trois seaux : `needs_answer` (issues entrantes `open`), `answered` (mes issues sortantes passées `answered`), `in_progress` (mes tâches `in_progress`). | Aucune notification ne peut être perdue : l'état est recalculé à chaque appel. Un second appel renvoie les mêmes seaux — c'est une propriété, pas un défaut : elle empêche la fausse conclusion « rien à faire » après un compactage de contexte. |
| 2 | **Le curseur ne pilote que le drapeau `new`.** Il ne conditionne jamais la présence d'une ligne dans un seau. | Toute la mécanique de fiabilité de livraison (`xid8`, filigrane `pg_snapshot_xmin`, curseur composite, fan-out d'audience, `actor_token_id`, `DrainInbox` en un statement) est **supprimée**. Un événement raté coûte un booléen, jamais une issue invisible. |
| 3 | **Pas de `xid8`, pas de filigrane, pas de `SELECT ... FOR UPDATE` sur le curseur, pas de transaction dans `inbox`.** | `models.go` reste sans `interface{}`. `check_inbox` = 5 requêtes indépendantes, aucune sérialisation, aucun `idle_in_transaction_session_timeout` à poser. Le trou de séquence de `bigserial` existe toujours et est **assumé et documenté** : il dégrade un `new: true` en `new: false`, rien d'autre. |
| 4 | **`token_cursors.last_event_id` démarre à `0`, sans amorçage.** | Une rotation de token (`RevokeProjectToken` + `CreateProjectToken`) ne perd rien et ne rejoue rien : les seaux sont bornés à 10, tout est simplement marqué `new`. Le problème « semer au filigrane courant perd les issues non lues » disparaît par construction. |
| 5 | **`events` est écrit dans la MÊME transaction que l'issue et son message, par le store de `issue` directement (`s.q.AppendEvent`).** Aucun port `internal/store/eventlog` en M3. | `internal/store/` reste vide. Un seul écrivain d'événements en M3 : la règle DRY (« plus de deux fois ») ne se déclenche pas. Le port sera créé quand `task` émettra à son tour (v2 / SSE). |
| 6 | **M3 n'émet des événements que pour les issues** (`issue.opened`, `issue.answered`, `issue.reopened`, `issue.closed`). `task` n'est pas rouvert. | Aucun seau ne dépend d'un événement de tâche (`in_progress` est dérivé de `tasks.status`). Émettre des `task.*` que personne ne lit serait du poids mort. Tâche de backlog pour v2. |
| 7 | **`issues.project_id` = destinataire (propriétaire de l'issue et du numéro), `issues.author_project_id` = émetteur.** Le numéro est tiré du compteur du **destinataire**. | `CORE-41` ouverte par FRNT porte la clé de CORE. Sémantique GitHub, clé auto-descriptive. |
| 8 | **Clause de visibilité canonique, littérale, répétée dans CHAQUE query issue :** `i.team_id = @team_id AND (i.project_id = @project_id OR i.author_project_id = @project_id)`, `@project_id` venant **exclusivement** de `Principal.ProjectID`. | Jamais un `if` de service. Jamais un `role` utilisé comme autorisation : `role` est une **restriction supplémentaire** posée par-dessus la clause complète. |
| 9 | **Toute écriture porte son scope.** Aucune query issue ne prend un `id` nu. Le message et la transition d'état sont **une seule instruction** (CTE modifiante). | Pas de TOCTOU : impossible d'écrire un message dans une issue fermée entre-temps, impossible d'avoir un message sans sa transition. |
| 10 | **`closed` est terminal. Les DEUX participants peuvent fermer.** L'état n'est jamais un paramètre : il est **calculé en SQL** depuis le rôle de l'appelant. | Message du destinataire → `answered` ; message de l'auteur → `open` (la relance remet le destinataire en dette) ; `close=true` → `closed`. Un agent ne peut pas mentir sur l'état qu'il produit. Rouvrir = nouvelle issue. Conséquence directe : **aucun 403 sur une clé d'issue**, donc aucun chemin « UPDATE puis relecture pour choisir entre 403 et 404 ». |
| 11 | **`answer_issue` refuse une issue déjà close** (`AND i.state <> 'closed'` dans la query), et `closed_at` n'est **jamais** écrasé (`CASE WHEN @close THEN now() ELSE i.closed_at END`). | Corrige les deux bugs de l'esquisse initiale : résurrection silencieuse d'une issue fermée, et effacement de la date de clôture à chaque message. |
| 12 | **Le titre d'une issue est immuable.** Pas d'`update_issue`. | Le destinataire ne peut pas requalifier la demande ; l'auteur ne peut pas invalider a posteriori la réponse. `issues.updated_at` ne bouge que sur message ou transition — c'est donc un tri d'inbox honnête. |
| 13 | **Une issue vers son propre projet est refusée**, par `CHECK (author_project_id <> project_id)` **et** par une validation de service qui rend un `400` explicite (« une question à soi-même est une tâche : utiliser create_task »). | Le `CHECK` seul remonterait `23514` → `ErrConflict` → `409`, code trompeur. Le CHECK reste le filet pour toute écriture future. Rend `incoming`/`outgoing` réellement disjoints et le `CASE` de transition total. |
| 14 | **Clés étrangères COMPOSITES `(project_id, team_id) REFERENCES projects (id, team_id)`** sur `issues` (les deux colonnes projet) et sur `events`. | Reprend à l'identique le patron `tasks_project_fk` de `000003`. Une issue inter-team n'est pas filtrée : elle est **impossible à insérer**. **Aucune nouvelle contrainte `UNIQUE` à créer** — `projects_id_team_unique` existe déjà, Postgres apparie l'ensemble de colonnes, pas leur ordre. |
| 15 | **`ON DELETE CASCADE` sur les deux FK projet d'`issues`.** Pas de `NO ACTION DEFERRABLE`. | v1 n'expose ni `DELETE /projects` ni `DELETE /teams` : trente lignes de schéma pour une opération non déclenchable. La conséquence connue (supprimer le projet auteur efface le fil chez le destinataire) est commentée dans la migration et devient une tâche de backlog, pas une fonctionnalité. |
| 16 | **`issue_messages` ne porte pas de `team_id`.** L'insertion et la lecture passent par un `SELECT`/CTE scopé sur l'issue. | Miroir exact de `task_notes` en `000003` (« une note n'est jamais lue sans passer par sa tâche, qui porte le scope ») et de `CreateTaskNote`. |
| 17 | **Jamais de sentinelle `= ''` castée en enum.** Filtre d'état = `sqlc.narg('state')::issue_state IS NULL OR i.state = sqlc.narg('state')::issue_state`. | `('' OR …)` sur un enum produit un `22P02 invalid input value for enum` **intermittent**, dépendant du plan : SQL ne garantit aucun court-circuit sur `OR`. Le patron correct est déjà dans `sql/queries/tasks.sql`. |
| 18 | **Un unique `ClaimNextNumber` par transaction, pris EN PREMIER**, et il ne doit **jamais** toucher une colonne de clé. | Un `UPDATE` sur une colonne non-clé prend `FOR NO KEY UPDATE`, compatible avec le `FOR KEY SHARE` que l'`INSERT` d'issue pose sur ses deux projets parents : deux agents symétriques (FRNT→CORE et CORE→FRNT) ne s'interbloquent pas. La vraie raison est écrite au-dessus de la query. |
| 19 | **Retirer `updated_at = now()` de `ClaimNextNumber`** (`sql/queries/projects.sql`), puis `make sqlc`. | Créer une tâche ou une issue n'est pas modifier le projet. Sans ce retrait, `projects.updated_at` devient « date du dernier objet créé » et toute logique future de cache ou de synchro sur cette colonne est fausse. Aucun changement de signature Go. |
| 20 | **Le compteur reste transactionnel. Migrer vers une `SEQUENCE` est interdit.** | Un `UPDATE ... RETURNING` rollback avec sa transaction : zéro trou. Un trou dans une numérotation lisible par un agent est un signal qui n'existe pas et sur lequel il spéculera. Test d'intégration : `BEGIN; claim; ROLLBACK;` ⇒ `next_number` inchangé. |
| 21 | **`ClaimNextNumber` n'est jamais exposée seule dans l'interface `Store` de `issue`.** `CreateIssue` fait résolution + claim + insert + message + event dans un seul `WithTx`, et le claim est fusionné dans la CTE d'insertion. | Il n'existe aucun chemin capable d'incrémenter le compteur d'un projet frère sans rien insérer. Une clé inconnue dans la team ⇒ 0 ligne ⇒ **aucun numéro consommé**. |
| 22 | **`WithTx` refuse l'imbrication bruyamment** (champ `inTx bool`, erreur `"transaction imbriquée"`), dans `issue` **et** en correctif sur `task/store/tx.go` qui passe aujourd'hui `db: s.db`. | Rejoindre silencieusement la transaction (`return fn(s)`) échange un interblocage contre un commit partiel invisible : un inner qui échoue et dont l'erreur est avalée voit ses écritures commitées par l'outer. Ouvrir une seconde connexion attend le verrou que la première détient sur la ligne `projects` : interblocage invisible en test mono-thread. |
| 23 | **Une violation d'unicité sur `*_number_unique_per_project` n'est PAS un `409`.** `translate()` branche sur `pgErr.ConstraintName` et remonte une erreur interne (`500`) + un log explicite. Même correctif dans `task/store/errors.go`. | Un `23505` sur `number` signifie que le compteur est corrompu : c'est un défaut serveur, pas une erreur d'appelant. |
| 24 | **Trois modules : `task`, `issue`, `inbox`.** Pas de fusion, et **pas d'extraction de `httpx`/`pgerr` en M3**. | `task` et `issue` ont des prédicats de scope différents (`project_id = $` vs `project_id = $ OR author_project_id = $`) : les voisiner dans un même `Store` est la configuration exacte où le copier-coller fuit. L'extraction de plomberie est un refactor transverse sur du code livré, découplé du nombre de modules, à arbitrer hors M3 — `task/handler.go` et `workspace/handler.go` divergent déjà (`scope()` vs `principal()`+`teamFor()`, `"conflict"` vs `"already exists"`, `maxBodyBytes` différents) : ~40 lignes réellement communes, pas 200. |
| 25 | **`requireProjectScope` est recopié dans `issue/module.go` et `inbox/module.go`.** Aucune modification de `internal/core/auth` ni de `module.go`. | Le patron existe et est documenté (`ARCHITECTURE.md`). Ajouter un `ProjectOnly` à `auth.Service` ouvrirait un fichier critique soumis à validation humaine pour dupliquer 12 lignes déjà écrites. Un token admin reçoit `403`, pas un `200 []`. |
| 26 | **`issue` et `inbox` lisent des tables d'autres domaines via leurs propres fichiers de queries scopées. Règle : une feature peut LIRE une table d'un autre domaine par une query scopée dédiée ; elle n'ÉCRIT jamais hors de son domaine, sauf via un port déclaré.** | `issue` lit `projects` (`GetProjectByKey`) et écrit `projects.next_number` via `ClaimNextNumber` — dette déjà contractée par `task` depuis M2 et documentée nulle part. `inbox` lit `issues`, `tasks` et `events`. À inscrire dans `docs/ARCHITECTURE.md` § « Interfaces inter-modules » (première entrée), avec la surface **exhaustive** autorisée. Aucun lint ne le vérifie : c'est une revue humaine. |
| 27 | **404 uniforme sur tout ce qui n'est pas résoluble dans la team du principal.** Clé de projet inconnue, projet d'une autre team, issue invisible, issue close : même corps `{"error":"not found"}`. | Codes distincts = oracle d'énumération inter-tenant (l'espace des clés est `^[A-Z][A-Z0-9]{1,9}$`). Cohérent avec le `decoyHash` d'`authenticate.go` et avec le commentaire déjà présent dans `task/handler.go:60-62`. La distinction n'a même pas à être calculée : la query team-scopée ne peut pas la produire. |
| 28 | **Les messages d'erreur sont enrichis dans la couche MCP, jamais dans `writeError`.** | L'API garde ses messages génériques. Le serveur MCP, qui connaît son propre token, reformule pour son agent — et n'énonce jamais que ce que l'agent sait déjà. Deux enrichissements : `to_project` inconnu → liste des clés valides de la team ; ref introuvable → « soit l'objet n'existe pas, soit c'est une tâche d'un autre projet ». |
| 29 | **Le paramètre qui porte `CORE-34` s'appelle `ref` partout ; celui qui porte `CORE` s'appelle `to_project`.** Renommage de `key` → `ref` dans les outils MCP déjà livrés, même commit. | `projects.key` vaut `CORE` : garder `key` pour `CORE-34` fait désigner deux choses par un même mot dans une surface où `DisallowUnknownFields` transforme une confusion en `400` + retry. Un concept, un nom. Normalisation serveur : majuscules, numéro nu accepté et résolu contre le projet du token. |
| 30 | **`get_task` devient `get(ref)`, résolvant tâche OU issue**, avec `kind` en premier champ de la réponse. La désambiguïsation est faite **côté MCP**, pas côté API. | Le compteur partagé rend la distinction invisible à l'appelant : une ref lue dans un commit ou dans `check_inbox` doit être consommable sans savoir ce qu'elle désigne. Côté HTTP, `GET /api/task/{number}` reste inchangé et **sans paramètre de projet** — la propriété « aucune surface où un scope pourrait être contourné » (`ARCHITECTURE.md`) est préservée. |
| 31 | **L'identité et la liste des projets frères sont injectées dans le champ `instructions` de `initialize`.** L'outil `whoami` est supprimé. | `runMCP` résout déjà `/whoami` au démarrage et échoue avec un message clair si le token n'est pas scopé projet (comportement livré, conservé). On y ajoute `GET /api/workspace/projects`. Rattrapage de la péremption du snapshot : une erreur `to_project inconnu` déclenche un **re-fetch** de la liste avant de composer le message d'erreur — le filet ne s'alimente pas du même snapshot que le chemin nominal. |
| 32 | **Politique de verbosité à trois niveaux, une seule constante de troncature (500).** Listes : aucun corps. `check_inbox` : dernier message tronqué à 500 + `truncated`. `get(ref)` : corps complets, fil plafonné aux 10 derniers + `messages_total`. Troncature **en SQL**, pas en Go. | Une inbox de trois lignes se traite sans un seul `get`. Aucun UUID, aucun `created_at` dans les lignes de liste (le tri est fait côté serveur), aucun écho des paramètres d'entrée dans les réponses d'écriture. |

---

## DDL

### `sql/migrations/000004_issues.up.sql`

```sql
-- 000004_issues — questions inter-projets, fil de messages, journal d'événements.
--
-- Une issue est ouverte par le projet A vers le projet B, à l'intérieur d'une team. Elle
-- appartient à B (comme une issue GitHub appartient au repo sur lequel elle est ouverte) et tire
-- son numéro du compteur de B : tasks et issues partagent la même suite, donc CORE-34 désigne
-- toujours un seul objet.

CREATE TYPE issue_state AS ENUM ('open', 'answered', 'closed');
CREATE TYPE event_subject AS ENUM ('task', 'issue');

-- `team_id` est dénormalisé comme sur `tasks`, pour que CHAQUE lecture porte son scope de
-- tenancy complet dans la query, sans jointure. Les deux clés étrangères composites garantissent
-- que cette dénormalisation ne peut pas diverger : une issue dont le team_id ne serait pas celui
-- de ses deux projets est impossible à insérer, quelle que soit la query qui l'écrit.
CREATE TABLE issues (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id           uuid        NOT NULL,
    -- Destinataire : propriétaire de l'issue, et projet dont le compteur a fourni `number`.
    project_id        uuid        NOT NULL,
    -- Émetteur : le projet qui pose la question.
    author_project_id uuid        NOT NULL,
    number            bigint      NOT NULL,
    title             text        NOT NULL,
    state             issue_state NOT NULL DEFAULT 'open',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    closed_at         timestamptz,

    CONSTRAINT issues_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    -- CASCADE aussi sur l'auteur : v1 n'expose ni DELETE /projects ni DELETE /teams, donc la
    -- seule cascade réellement déclenchable est la suppression d'une team, qui emporte tout.
    -- Conséquence connue et assumée : le jour où un DELETE /projects existera, supprimer le
    -- projet auteur effacera le fil chez le destinataire. À rouvrir à ce moment-là (archived_at
    -- sur projects), pas avant.
    CONSTRAINT issues_author_project_fk FOREIGN KEY (author_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,

    CONSTRAINT issues_number_unique_per_project UNIQUE (project_id, number),
    CONSTRAINT issues_number_positive CHECK (number >= 1),
    CONSTRAINT issues_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT issues_title_length CHECK (char_length(title) <= 200),

    -- Une issue vers soi-même serait à la fois entrante et sortante : elle casserait la
    -- partition de list_issues(role=) et ne pourrait jamais atteindre `answered`, puisque la
    -- transition est déduite de l'émetteur du message. Une question à soi-même est une tâche.
    CONSTRAINT issues_not_self CHECK (author_project_id <> project_id),

    -- closed_at et state ne peuvent pas diverger : une issue fermée a une date, une issue
    -- ouverte n'en a pas. Sans ça, un UPDATE mal écrit produit une issue « close » sans date ou
    -- une issue ouverte qui prétend l'avoir été.
    CONSTRAINT issues_closed_at_shape CHECK ((state = 'closed') = (closed_at IS NOT NULL))
);

-- Deux index miroirs : le prédicat de visibilité est un OR sur deux colonnes, qu'aucun index
-- composite unique ne peut servir. Le planner fait un BitmapOr des deux quand le rôle n'est pas
-- précisé, un index scan simple quand il l'est.
--
-- La colonne projet est en tête (comme tasks_project_active_idx) : ces index servent AUSSI la
-- maintenance des deux clés étrangères lors d'une cascade de suppression de team, qui sinon
-- ferait un seq scan complet de `issues` par projet supprimé.
CREATE INDEX issues_incoming_idx ON issues (project_id, team_id, state, updated_at DESC);
CREATE INDEX issues_outgoing_idx ON issues (author_project_id, team_id, state, updated_at DESC);

-- Fil de messages, append-only. Pas de team_id ici : un message n'est jamais lu sans passer par
-- son issue, qui porte le scope — même règle que task_notes.
CREATE TABLE issue_messages (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id          uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    author_project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    body_md           text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT issue_messages_body_not_blank CHECK (btrim(body_md) <> '')
);

CREATE INDEX issue_messages_thread_idx ON issue_messages (issue_id, created_at, id);
-- Index de clé étrangère, pas de lecture : sans lui, une cascade sur projects scanne la table.
CREATE INDEX issue_messages_author_idx ON issue_messages (author_project_id);

-- Journal append-only par team.
--
-- En v1 il ne sert qu'à une chose : calculer le drapeau `new` de check_inbox. L'état de
-- référence est TOUJOURS issues.state / tasks.status — un événement manqué ne coûte donc jamais
-- qu'un `new: false`, jamais une issue invisible. C'est ce qui autorise à ne PAS payer le prix
-- d'une livraison exactement-une-fois (colonne xid8, filigrane de snapshot, curseur composite).
--
-- Le trou de séquence est réel et assumé : `id` est attribué à l'INSERT, pas au COMMIT, donc une
-- transaction lente peut committer un id plus petit après qu'un lecteur a dépassé ce point. Le
-- seul effet est un drapeau `new` manquant. Ne PAS « corriger » ce comportement sans avoir
-- d'abord relu la décision #1 : la correction coûte plus cher que le défaut.
--
-- Aucun texte libre ici : ni titre, ni corps, ni référence dénormalisée. Une ligne fait ~100
-- octets, taille bornée, et check_inbox lit les titres depuis issues/tasks — qui sont de toute
-- façon jointes. Dénormaliser une ref imposerait un GetProjectByID supplémentaire DANS la
-- transaction d'écriture (Principal ne porte pas la clé du projet) pour un gain nul.
CREATE TABLE events (
    id               bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id          uuid          NOT NULL,
    -- Projet propriétaire du SUJET (celui dont le compteur a fourni la ref), pas l'audience :
    -- l'inbox ne lit jamais `events` par ce champ, elle y accède par jointure sur un sujet déjà
    -- scopé. La lecture d'événements n'a donc aucune surface non scopée.
    project_id       uuid          NOT NULL,
    actor_project_id uuid          NOT NULL,
    kind             text          NOT NULL,
    subject_type     event_subject NOT NULL,
    subject_id       uuid          NOT NULL,
    created_at       timestamptz   NOT NULL DEFAULT now(),

    CONSTRAINT events_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_actor_fk FOREIGN KEY (actor_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_kind_format CHECK (kind ~ '^[a-z_]+\.[a-z_]+$')
);

-- Sert l'EXISTS du drapeau `new` : sans lui, chaque ligne d'inbox scanne le journal de la team.
CREATE INDEX events_subject_idx ON events (subject_id, id);
-- Sert le max(id) par team (tête du journal) et, en v2, le flux SSE.
CREATE INDEX events_team_idx ON events (team_id, id);
-- Index de clé étrangère.
CREATE INDEX events_actor_idx ON events (actor_project_id);

-- Curseur de lecture, par TOKEN et non par projet : deux sessions d'agent sur le même repo ont
-- chacune leur avancement.
--
-- Aucune clé étrangère vers events.id, délibérément : une purge future du journal doit rester un
-- simple DELETE, sans cascade ni violation de contrainte. Le curseur démarre à 0 et n'est jamais
-- amorcé : un token neuf (ou fraîchement tourné) voit tout marqué `new`, ce qui est exact, et ne
-- rejoue rien puisque les seaux sont bornés.
CREATE TABLE token_cursors (
    token_id      uuid        PRIMARY KEY REFERENCES tokens (id) ON DELETE CASCADE,
    last_event_id bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT token_cursors_last_event_id_positive CHECK (last_event_id >= 0)
);
```

### `sql/migrations/000004_issues.down.sql`

```sql
DROP TABLE IF EXISTS token_cursors;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS issue_messages;
DROP TABLE IF EXISTS issues;
DROP TYPE IF EXISTS event_subject;
DROP TYPE IF EXISTS issue_state;
```

---

## Queries scopées critiques

### `sql/queries/projects.sql` — correctif (décision #19)

```sql
-- ClaimNextNumber réserve le prochain identifiant lisible du projet (FRNT-34).
-- Le UPDATE ... RETURNING sérialise les appels concurrents sur la ligne du projet, et rollback
-- avec sa transaction : aucun trou dans la numérotation.
--
-- CONTRAINTE DE VERROUILLAGE — ne jamais ajouter ici l'écriture d'une colonne de clé (ni une
-- colonne couverte par un index unique). Tant que l'UPDATE ne touche que des colonnes non-clé,
-- Postgres prend un FOR NO KEY UPDATE, compatible avec le FOR KEY SHARE que l'INSERT d'issue
-- pose sur ses DEUX projets parents. Le jour où ce n'est plus vrai, deux agents symétriques
-- (FRNT→CORE et CORE→FRNT) s'interbloquent.
--
-- `updated_at` n'est volontairement PAS touché : créer une tâche ou une issue n'est pas modifier
-- le projet. L'y écrire ferait de projects.updated_at une « date du dernier objet créé ».
-- name: ClaimNextNumber :one
UPDATE projects
SET next_number = next_number + 1
WHERE id = $1 AND team_id = $2
RETURNING (next_number - 1)::bigint AS claimed_number;
```

### `sql/queries/issues.sql`

```sql
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
-- name: CreateIssue :one
WITH claimed AS (
    UPDATE projects p
    SET next_number = p.next_number + 1
    WHERE p.team_id = @team_id AND p.key = @to_project_key
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

-- Le fil est scopé par jointure sur son issue : impossible de lire les messages d'une issue
-- qu'on ne voit pas, même en connaissant son identifiant.
-- name: ListIssueMessages :many
SELECT m.body_md, m.created_at, ap.key AS author_key
FROM issue_messages m
JOIN issues i    ON i.id  = m.issue_id
JOIN projects ap ON ap.id = m.author_project_id
WHERE i.team_id = @team_id
  AND i.id      = @issue_id
  AND (i.project_id = @caller_project_id OR i.author_project_id = @caller_project_id)
ORDER BY m.created_at, m.id;

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

-- name: AppendEvent :exec
INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
VALUES (@team_id, @project_id, @actor_project_id, @kind, @subject_type, @subject_id);
```

### `sql/queries/inbox.sql`

```sql
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
```

---

## Interfaces Go

Flow strict `handler → service → store`. Aucun import `internal/feature/<autre>`. `*sql.DB` ne dépasse jamais la couche store. Tous les fichiers ci-dessous portent un bloc `// SOMMAIRE` dès 2 déclarations top-level.

### `internal/feature/issue/store/store.go` — CONTRAT UNIQUEMENT

```go
package store

var (
	ErrNotFound = errors.New("issue store: not found")
	ErrConflict = errors.New("issue store: conflict")
)

// Ref désigne un objet par sa clé lisible, du point de vue d'un appelant donné.
// CallerProjectID vient de Principal.ProjectID et n'est jamais lu depuis une requête.
type Ref struct {
	TeamID          uuid.UUID
	CallerProjectID uuid.UUID
	ProjectKey      string // préfixe de la ref : CORE dans CORE-34
	Number          int64
}

type Issue struct {
	ID               uuid.UUID
	TeamID           uuid.UUID
	ProjectID        uuid.UUID // destinataire
	AuthorProjectID  uuid.UUID // émetteur
	ProjectKey       string
	AuthorProjectKey string
	Number           int64
	Title            string
	State            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ClosedAt         *time.Time
}

type Message struct {
	AuthorKey string
	Body      string
	CreatedAt time.Time
}

// NewIssue : le destinataire est désigné par sa CLÉ, jamais par un UUID. La résolution, la
// réservation du numéro et l'insertion sont une seule instruction SQL.
type NewIssue struct {
	TeamID          uuid.UUID
	AuthorProjectID uuid.UUID
	ToProjectKey    string
	Title           string
	Body            string
}

type Filter struct {
	TeamID        uuid.UUID
	ProjectID     uuid.UUID
	OnlyIncoming  bool
	OnlyOutgoing  bool
	State         string // vide = pas de filtre
	IncludeClosed bool
	Limit         int32
}

// Answer porte un message et, éventuellement, la fermeture. L'état résultant n'est PAS un champ :
// il est calculé en SQL depuis ProjectID (qui parle), et le store le renvoie.
type Answer struct {
	Ref   Ref
	Body  string
	Close bool
}

// Event est une ligne du journal. ProjectID est le projet propriétaire du sujet.
type Event struct {
	TeamID         uuid.UUID
	ProjectID      uuid.UUID
	ActorProjectID uuid.UUID
	Kind           string
	SubjectType    string // "issue"
	SubjectID      uuid.UUID
}

// Store est le contrat consommé par le service.
//
// Toute méthode porte le scope complet de l'appelant. Il n'existe aucune lecture ni écriture
// non scopée dans ce contrat, donc aucun appelant ne peut en oublier une. ClaimNextNumber n'y
// figure PAS : réserver un numéro n'est jamais une opération adressable seule, sinon il
// existerait un chemin capable de faire avancer le compteur d'un projet frère sans rien insérer.
type Store interface {
	// WithTx exécute fn dans une transaction, sur un store qui la partage. Refuse l'imbrication
	// par une erreur explicite : rejoindre silencieusement la transaction laisserait committer
	// les écritures d'un appel interne dont l'erreur a été avalée.
	WithTx(ctx context.Context, fn func(Store) error) error

	// ProjectIDByKey résout une clé de projet dans la team de l'appelant. Une clé d'une autre
	// team est introuvable, pas interdite.
	ProjectIDByKey(ctx context.Context, teamID uuid.UUID, key string) (uuid.UUID, error)

	CreateIssue(ctx context.Context, in NewIssue) (Issue, error)
	AppendFirstMessage(ctx context.Context, issueID, authorProjectID uuid.UUID, body string) error

	IssueByRef(ctx context.Context, ref Ref) (Issue, error)
	ListIssues(ctx context.Context, f Filter) ([]Issue, error)
	ListMessages(ctx context.Context, teamID, callerProjectID, issueID uuid.UUID) ([]Message, error)

	// Answer insère le message et applique la transition d'état en une seule instruction.
	Answer(ctx context.Context, in Answer) (Issue, error)

	// AppendEvent écrit dans le journal. Appelé exclusivement à l'intérieur d'un WithTx : un
	// événement sans son issue notifierait un objet inexistant.
	AppendEvent(ctx context.Context, e Event) error
}

type store struct {
	q    *database.Queries
	db   *sql.DB
	inTx bool
}

func New(q *database.Queries, db *sql.DB) Store { return &store{q: q, db: db} }
```

### `internal/feature/issue/store/tx.go`

```go
// WithTx exécute fn dans une transaction et ne commite que si fn réussit.
//
// L'imbrication est refusée, pas absorbée. Ouvrir une seconde transaction prendrait une autre
// connexion du pool, qui attendrait le verrou que celle-ci détient sur la ligne projects
// (ClaimNextNumber) : un interblocage invisible en test unitaire comme en test d'intégration
// mono-thread. Et rejoindre silencieusement l'existante ferait committer par l'extérieur les
// écritures partielles d'un appel interne dont l'erreur aurait été avalée.
func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.inTx {
		return errors.New("issue store: transaction imbriquée")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("issue store: ouverture de transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // sans effet après un Commit réussi

	if err := fn(&store{q: s.q.WithTx(tx), db: s.db, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("issue store: commit: %w", err)
	}
	return nil
}
```

> Correctif à appliquer à l'identique dans `internal/feature/task/store/tx.go`, qui passe aujourd'hui `db: s.db` sans garde et est donc ré-entrant sur une seconde connexion (ligne 22).

### `internal/feature/issue/service/service.go` — CONTRAT UNIQUEMENT

```go
var (
	ErrInvalidInput = errors.New("issue: invalid input")
	ErrNotFound     = errors.New("issue: not found")
	ErrConflict     = errors.New("issue: conflict")
)

// Service porte les questions inter-projets.
//
// TeamID et ProjectID viennent du Principal du token, jamais du corps de la requête : c'est ce
// qui rend impossible d'agir au nom d'un autre projet.
type Service interface {
	CreateIssue(ctx context.Context, in CreateIssueInput) (Issue, error)
	ListIssues(ctx context.Context, in ListIssuesInput) ([]Issue, error)
	GetIssue(ctx context.Context, in RefInput) (IssueDetail, error)
	Answer(ctx context.Context, in AnswerInput) (Issue, error)
}

type service struct{ store store.Store }

func New(st store.Store) Service { return &service{store: st} }

// CreateIssueInput — le destinataire est une CLÉ. TeamID/AuthorProjectID portent `json:"-"` :
// ils ne peuvent pas être renseignés depuis le corps.
type CreateIssueInput struct {
	TeamID          uuid.UUID `json:"-"`
	AuthorProjectID uuid.UUID `json:"-"`

	ToProject string `json:"to_project"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

// RefInput désigne CORE-34 pour un appelant. ProjectKey est le préfixe de la ref, il n'est PAS
// un choix libre : le service refuse une clé qui ne désigne ni le projet de l'appelant ni un
// projet de sa team, et la query refuserait de toute façon.
type RefInput struct {
	TeamID     uuid.UUID `json:"-"`
	ProjectID  uuid.UUID `json:"-"`
	ProjectKey string    `json:"-"`
	Number     int64     `json:"-"`
}

type ListIssuesInput struct {
	TeamID    uuid.UUID `json:"-"`
	ProjectID uuid.UUID `json:"-"`

	Role  string `json:"role"`  // "", "incoming", "outgoing"
	State string `json:"state"` // "", "open", "answered", "closed"
	Limit int    `json:"limit"`
}

// AnswerInput — Close ferme l'issue. L'état résultant n'est pas exprimable : il est déduit.
type AnswerInput struct {
	Ref   RefInput `json:"-"`
	Body  string   `json:"body"`
	Close bool     `json:"close"`
}

// Issue est la vue API. Ref est la clé lisible complète, composée côté service : c'est le SEUL
// producteur de clé de la feature, aucune concaténation ailleurs.
type Issue struct {
	Ref       string     `json:"ref"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Role      string     `json:"role"` // "incoming" | "outgoing"
	Peer      string     `json:"peer"` // clé du projet à l'autre bout
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

type Message struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type IssueDetail struct {
	Issue
	Messages      []Message `json:"messages"`
	MessagesTotal int       `json:"messages_total"`
}
```

### `internal/feature/inbox/store/store.go` — CONTRAT UNIQUEMENT

```go
// Scope porte le scope complet d'une lecture d'inbox. Il n'existe pas de constructeur alternatif :
// TeamID/ProjectID viennent du Principal, TokenID aussi.
type Scope struct {
	TokenID   uuid.UUID
	TeamID    uuid.UUID
	ProjectID uuid.UUID
	Limit     int32
}

type Cursor struct {
	LastEventID int64
	HeadEventID int64
}

type IssueLine struct {
	Number    int64
	Title     string
	Peer      string
	Excerpt   string
	Truncated bool
	New       bool
	UpdatedAt time.Time
}

type TaskLine struct {
	Number    int64
	Title     string
	Priority  string
	UpdatedAt time.Time
}

// Store lit l'état actionnable d'un projet. Aucun Transactor : la cohérence de check_inbox ne
// dépend d'aucune atomicité — le curseur ne pilote qu'un drapeau d'affichage.
type Store interface {
	Cursor(ctx context.Context, sc Scope) (Cursor, error)
	IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error)
	InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error)
	Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error
}

type store struct{ q *database.Queries }

// New ne reçoit pas de *sql.DB : inbox n'ouvre jamais de transaction.
func New(q *database.Queries) Store { return &store{q: q} }
```

### `internal/feature/inbox/service/service.go` — CONTRAT UNIQUEMENT

```go
type Service interface {
	Check(ctx context.Context, in CheckInput) (Inbox, error)
}

type CheckInput struct {
	TokenID    uuid.UUID `json:"-"`
	TeamID     uuid.UUID `json:"-"`
	ProjectID  uuid.UUID `json:"-"`
	ProjectKey string    `json:"-"` // composé par le handler depuis le Principal résolu
}

type IssueLine struct {
	Ref       string    `json:"ref"`
	Title     string    `json:"title"`
	Peer      string    `json:"peer"`
	Excerpt   string    `json:"excerpt"`
	Truncated bool      `json:"truncated,omitempty"`
	New       bool      `json:"new"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskLine struct {
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// Inbox est l'état courant, pas un flux. Les `more` disent ce qui n'a pas tenu dans le seau.
type Inbox struct {
	Project     string      `json:"project"`
	NeedsAnswer []IssueLine `json:"needs_answer"`
	Answered    []IssueLine `json:"answered"`
	InProgress  []TaskLine  `json:"in_progress"`
	More        More        `json:"more,omitempty"`
}

type More struct {
	NeedsAnswer int `json:"needs_answer,omitempty"`
	Answered    int `json:"answered,omitempty"`
	InProgress  int `json:"in_progress,omitempty"`
}
```

### Routage

```go
// internal/feature/issue/module.go — middleware lié UNE fois, requireProjectScope recopié.
r.Handle("POST /{$}",                    project(m.h.CreateIssue))
r.Handle("GET /{$}",                     project(m.h.ListIssues))
r.Handle("GET /{project}/{number}",      project(m.h.GetIssue))
r.Handle("POST /{project}/{number}/answer", project(m.h.Answer))

// internal/feature/inbox/module.go
r.Handle("GET /{$}", project(m.h.Check))

// cmd/api/main.go — buildModules()
issue.NewModule(base),   // store.New(cfg.DB, cfg.RawDB)
inbox.NewModule(base),   // store.New(cfg.DB)  — pas de RawDB, pas de transaction
```

---

## Surface MCP M3

**Huit** outils depuis FLWL-15 (neuf à la livraison de M3 : voir la note sur `add_task_note` plus bas). Le budget est réinjecté à **chaque tour** de chaque session : tout ajout se paie indéfiniment.

| Outil | Paramètres |
| ----- | ---------- |
| `list_tasks` | `status?` ∈ todo\|in_progress\|blocked\|done, `limit?` (déf. 50, max 200), `archived?` |
| `get` | **`ref`** (requis) — `CORE-34`, ou numéro nu résolu dans le projet du token |
| `create_task` | `title` (requis), `body?`, `status?`, `priority?`, `deadline?` (RFC 3339) |
| `update_task` | **`ref`** (requis), `title?`, `body?`, `status?`, `priority?`, `deadline?`, `clear_deadline?`, `note?`, `archive?` |
| `create_issue` | `to_project` (requis), `title` (requis), `body` (requis) |
| `list_issues` | `role?` ∈ incoming\|outgoing (omis = les deux), `state?` ∈ open\|answered\|closed (omis = open + answered), `limit?` (déf. 20, max 100) |
| `answer_issue` | `ref` (requis), `body` (requis), `close?` (déf. false) |
| `check_inbox` | **aucun paramètre** |

**Comportements non négociables de la surface :**

- `get(ref)` renvoie `kind: "task"|"issue"` en premier champ. Résolution **côté MCP** : si le préfixe est ma propre clé, essayer `GET /api/task/{n}` puis `GET /api/issue/{maClé}/{n}` (le compteur partagé rend `CORE-34` ambigu pour l'agent de CORE : ce peut être sa tâche ou une issue entrante) ; sinon `GET /api/issue/{clé}/{n}`. L'API HTTP de `task` reste sans paramètre de projet.
- `get` est le **seul** outil qui renvoie des corps complets. Fil plafonné aux 10 derniers messages + `messages_total`.
- `create_issue` refuse `to_project` = mon propre projet, avec un message qui redirige vers `create_task`. `to_project` est normalisé en majuscules avant résolution. Réponse minimale : `{ref, to_project, state}`.
- `check_inbox` renvoie `{project, needs_answer[], answered[], in_progress[], more{}}`. 10 lignes par seau. Extraits à 500 caractères + `truncated`. `in_progress` ne porte pas de `new`.
- Description de `check_inbox`, à écrire mot pour mot : *« Ce qui vous attend : questions entrantes à traiter, vos questions qui ont reçu une réponse, vos tâches en cours. L'état de référence reste `list_issues` / `list_tasks`. »*
- `answer_issue` : `body` est obligatoire même pour clore — une clôture sans motif ne dit rien au correspondant.
- Aucun UUID ne traverse la couche MCP. Corollaire : retirer `Project.ID` de la réponse de `GET /api/workspace/projects` — il contredit le commentaire de `workspace/service/service.go` et n'a aucun consommateur (`cmd/flowlio/commands.go` n'imprime que `Key` et `Name`).

### Supprimé de la surface annoncée (`docs/DESIGN-V1.md:131-143`)

| Outil | Raison |
| ----- | ------ |
| `whoami` | Appelé une fois par session, contenu constant sur la vie du token. `runMCP` résout déjà `/whoami` au démarrage : on y ajoute `GET /projects` et on injecte le tout dans `initialize.instructions` (« Tu es l'agent du projet CORE (omiros-core), team omiros. Projets frères : WEB, API. Une référence se lit CLE-NUMERO. »). Zéro schéma, zéro tour, l'info est dans le contexte avant le premier message. |
| `close_issue` | Fusionné dans `answer_issue(close=true)`. Le cas majoritaire est « je réponds et ça clôt le sujet » : deux outils = deux tours pour un acte unique. Découvrabilité couverte par la description de `answer_issue`. |
| `get_task` | Remplacé par `get(ref)`. Le compteur partagé cache délibérément à l'agent si `CORE-34` est une tâche ou une issue : deux outils typés échoueraient une fois sur deux quand l'agent n'a que la ref (lue dans `check_inbox`, un commit, un message d'issue). |
| `get_issue` | **Jamais ajouté** — absorbé par `get(ref)`. |
| `list_projects` | **Jamais ajouté** — la liste des projets frères vit dans `instructions`, et se rafraîchit sur erreur `to_project inconnu`. |
| `archive_task` | Déjà absorbé en M2 par `update_task(archive=true)`. |
| `update_issue` | **Jamais ajouté** — le titre d'une issue est immuable (décision #12). |

`add_task_note` a été **conservé en M3**, puis **replié dans `update_task` en champ `note?`** (FLWL-15, 2026-08-02) — la tâche de backlog annoncée ici. Ce que le repli a coûté, et pourquoi il valait le coup :

| | Avant | Après |
| --- | --- | --- |
| Outils exposés | 9 | **8** |
| « passer en done et dire pourquoi » | 2 appels, 2 transactions | **1 appel, 1 transaction** |
| État « statut changé, motif perdu » | atteignable | **impossible** |

Le gain n'est pas seulement un schéma économisé. Deux écritures séparées laissaient exister l'état où le `done` est en base et la note est tombée : la session suivante lisait un `done` que rien n'expliquait. La note voyage donc DANS le patch, écrite par `service.UpdateTask` dans un `WithTx` avec lui.

> Conséquence côté API : la route `POST /api/task/{number}/notes` **n'existe plus**, et `service.AddNote` / `handler.AddNote` non plus. Il ne reste qu'un seul chemin d'écriture vers le fil d'une tâche, emprunté à l'identique par la CLI (`flowlio task note`) et par MCP. `store.AddNote` subsiste : c'est lui qu'appelle `UpdateTask` dans la transaction.

### Forme des retours d'écriture (FLWL-15)

Toute écriture rend `{ref, <objet>}`, et rien d'autre :

```
create_task  → {"ref": "CORE-34", "task":  {…}}
update_task  → {"ref": "CORE-34", "task":  {…}}
create_issue → {"ref": "FRNT-12", "issue": {…}}
answer_issue → {"ref": "FRNT-12", "issue": {…}}
list_tasks   → [{"ref": "CORE-7", "task": {…}}, …]
get          → {"kind": "task"|"issue", "ref": …, "task"|"issue": {…}}
```

Avant, un agent devait deviner où lire la référence selon l'outil qu'il venait d'appeler : sous `key` pour une tâche, à l'intérieur de l'objet pour une issue. `list_issues` garde `[]Issue` sans enveloppe — chaque ligne porte déjà son `ref`, et l'envelopper le dupliquerait sur chaque ligne d'un listing.

---

## Pièges à ne pas rater à l'implémentation

1. **Trou de séquence du journal.** `events.id` est attribué à l'`INSERT`, pas au `COMMIT` : une transaction lente peut committer un id plus petit après qu'un lecteur l'a dépassé. **C'est accepté**, et ça n'est acceptable que parce que le curseur ne pilote que le drapeau `new`. Le jour où quelqu'un veut faire dépendre la présence d'une ligne d'inbox du curseur, il réintroduit une perte silencieuse d'issue. Écrire cette phrase dans la migration.
2. **`AND i.state <> 'closed'` et `closed_at`.** Sans le garde, `answer_issue` ressuscite une issue fermée et elle réapparaît indéfiniment dans le seau du correspondant. Et `closed_at = CASE ... ELSE NULL` efface la date de clôture à chaque message : c'est `ELSE i.closed_at`.
3. **Message et transition = une seule instruction.** Deux statements laissent passer un message écrit dans une issue fermée entre-temps : `updated_at` ne bouge pas, l'inbox dérivée ne le montre jamais, une réponse disparaît.
4. **Jamais de sentinelle `''` castée en enum.** `(@state::text = '' OR state = @state::issue_state)` lève un `22P02` de façon **intermittente** selon le plan : SQL ne court-circuite pas `OR`. Utiliser `sqlc.narg(...)::issue_state IS NULL`, patron déjà présent dans `sql/queries/tasks.sql`.
5. **`ClaimNextNumber` ne doit jamais écrire une colonne de clé.** Tant que c'est vrai, `FOR NO KEY UPDATE` et le `FOR KEY SHARE` des FK de l'`INSERT` sont compatibles et deux agents symétriques ne s'interbloquent pas. Ce n'est pas « un seul claim par transaction » qui protège, contrairement à ce qu'on croit en le lisant.
6. **Ré-entrance de `WithTx`.** Refuser bruyamment. Ni `db: s.db` (seconde connexion → interblocage sur la ligne `projects`), ni `return fn(s)` (commit partiel silencieux). Correctif à porter aussi sur `task/store/tx.go`.
7. **`translate()` et `23505`.** Une violation de `issues_number_unique_per_project` signifie que le compteur est corrompu : `500` + log explicite, pas `409 conflict`. Brancher sur `pgErr.ConstraintName`, dans `issue` **et** dans `task` (aujourd'hui `23505`, `23514` et `23503` sont tous trois mappés sur `ErrConflict`).
8. **« Inexistant » et « interdit » ne se distinguent jamais** sur une clé d'issue ni sur une clé de projet. Le prédicat d'accès est dans le `WHERE`, jamais un `if` de service : zéro ligne dans les deux cas, même code, même message, même latence. Pas de 403 sur `answer_issue` (les deux participants peuvent fermer, donc ce chemin n'existe pas). Désambiguïsation éventuelle **côté MCP uniquement**, à partir de ce que l'appelant peut déjà lire.
9. **`issue_messages` sans `team_id`.** L'insertion et la lecture passent par l'issue, comme `CreateTaskNote` passe par sa tâche. Ne pas « corriger » en ajoutant un `team_id` : ce serait une seconde source de vérité à maintenir cohérente.
10. **`sql/schema/schema.sql` à re-dumper** (`make schema`) et **`000004_issues.down.sql` à écrire**. CLAUDE.md § Base de données : le schéma est la source de vérité, mise à jour après chaque migration. sqlc lit directement `sql/migrations/` : une migration incomplète casse `make sqlc`.
11. **`// SOMMAIRE` sur tous les nouveaux fichiers ≥ 2 déclarations**, avec les numéros de ligne **finaux**. Le hook `PostToolUse` bloque en exit 2.
12. **Taille des fichiers.** `task/store/task.go` fait déjà 201 lignes pour 6 méthodes. Le store de `issue` en aura 8 + messages + événements : découper d'emblée en `issue.go` / `message.go` / `event.go` / `project.go`, ne pas attendre que `scripts/check-file-size.sh` (300) bloque.
13. **`instructions` : dégradation.** `runMCP` échoue déjà si `/whoami` échoue — conserver ce comportement (message clair sur **stderr**, jamais stdout). Mais la liste des projets frères doit se **re-récupérer** au moment de composer l'erreur « `to_project` inconnu » : si le filet s'alimente du snapshot de démarrage, le chemin nominal et son rattrapage tombent ensemble pour toute la durée du process.
14. **`stdout` appartient au protocole MCP.** Un seul `Println` égaré dans le nouveau code d'outil casse la session de l'agent.
15. **Idempotence.** Un `create_issue` dont la réponse se perd (timeout, session tuée) sera rejoué par l'agent : issue dupliquée et numéro brûlé, alors que la numérotation dense est un invariant. Le défaut préexiste sur `create_task` ; M3 le double sur le chemin le plus coûteux du produit. **Hors périmètre M3** — créer la tâche de backlog, ne pas l'improviser.
16. **Dettes DESIGN-V1 non livrées, à tracker et pas à traiter en M3** : rate limiting sur la résolution de token (`DESIGN-V1.md` § Sécurité, absent du code), purge du journal d'événements (quand elle arrivera : purger par âge **seul** ferait sauter à un projet dormant tout ce qu'il n'a pas lu — la borne est le curseur le plus en retard).
17. **Documentation à mettre à jour dans le même commit que la migration** : `docs/ARCHITECTURE.md` § Domaines (deux lignes : `issue`, `inbox`, avec leurs portées), § Interfaces inter-modules (**première entrée**, décision #26, à valider avec Maxence), et `docs/DESIGN-V1.md` § Surface MCP + § Schéma (`events` n'a plus la forme annoncée ligne 102, et la liste d'outils ligne 131-143 change).

---

## Questions qui restent pour l'humain

Une seule, plus une validation de procédure.

1. **Qui peut fermer une issue ?** Tranché ici : **les deux participants**, `closed` terminal, rouvrir = nouvelle issue. Cela supprime tout chemin 403 sur une clé d'issue, donc toute surface d'oracle, et correspond au modèle mental GitHub. L'alternative (auteur seul, le destinataire disposant de `answered` pour signaler qu'il a fait sa part) protège l'auteur contre un destinataire qui clôt une question gênante, au prix d'un 403 conditionnel à écrire avec un ordre strict (UPDATE scopé → relecture → 403 sinon 404). **Si Maxence ne tranche pas, la décision #10 s'applique.**

2. **Validation de procédure, pas d'arbitrage :** `docs/ARCHITECTURE.md` § « Interfaces inter-modules » dit « Aucune pour l'instant » et la règle impose de valider avec l'humain toute entrée. M3 en écrit la première — qui n'est pas un `FeatureRegistry` mais la règle d'accès partagé aux tables (décision #26). Elle formalise une dette **déjà contractée par M2** (`task/store/task.go:39` écrit dans `projects` via `ClaimNextNumber`) et qu'aucun lint ne voit, `check-cross-feature-imports.sh` ne scannant que les imports Go.