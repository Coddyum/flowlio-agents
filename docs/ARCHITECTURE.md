# ARCHITECTURE — flowlio-agents

Carte des domaines et des interfaces inter-modules. À lire avant d'éditer une zone inconnue,
à mettre à jour dès qu'une feature ou une interface inter-module apparaît.

## État actuel

**M1, M2 et M3 livrés** : tenancy et tokens d'agent ; tâches et serveur MCP stdio ; issues
inter-projets et inbox d'état.

Décisions de conception : [DESIGN-V1.md](DESIGN-V1.md) pour le périmètre v1,
[DESIGN-M3.md](DESIGN-M3.md) pour le modèle des issues et du journal d'événements — à lire avant
de toucher à `issue`, `inbox` ou `events`, il explique des choix qui paraissent surprenants tant
qu'on n'a pas la raison (notamment : le curseur ne pilote qu'un drapeau d'affichage, jamais la
présence d'une ligne).

## Squelette

| Chemin                        | Rôle                                                                |
| ----------------------------- | ------------------------------------------------------------------- |
| `cmd/api/main.go`             | Wiring : config → DB → services partagés → registry → engine. Seul `log.Fatal`. |
| `internal/core/module/`       | Contrats `Module`, `CoreServices`, `FeatureRegistry`, `ModuleConfig`. **Fichier critique.** |
| `internal/core/engine/`       | Routeur racine, montage des modules sous `/api/<clé>/`, middleware global. |
| `internal/core/registry.go`   | Implémentation du `FeatureRegistry` (résolution lazy inter-features). |
| `internal/core/services.go`   | Implémentation de `CoreServices` (services partagés transverses).     |
| `internal/feature/<nom>/`     | Une feature = `module.go` + `handler/` + `service/` + `store/`.       |
| `internal/store/`             | Interfaces store globales / composition inter-features.               |
| `internal/database/`          | Généré par sqlc. Ne jamais éditer à la main.                          |
| `internal/pkg/cache/`         | Port `Cache` + implémentation mémoire (go-cache).                     |
| `internal/pkg/config/`        | `Config` + `Load()` depuis l'environnement, fail fast.                |
| `internal/pkg/database/`      | `Connect(dsn)` → pool `*sql.DB` (driver pgx v5 en mode `database/sql`), et refus d'un endpoint Neon mutualisé mal configuré. |
| `sql/migrations/`             | golang-migrate — humain uniquement.                                   |
| `sql/queries/`                | Source des queries lues par sqlc.                                     |
| `sql/schema/`                 | Source de vérité du modèle, mise à jour après chaque migration.       |

## Routage

L'engine monte chaque module sous `/api/<Key()>/` et retire le préfixe (`http.StripPrefix`) :
un module déclare ses routes relativement à lui-même (`POST /x`, `DELETE /x/{id}`).

Middleware global (engine, appliqué à toutes les routes, dans l'ordre) : `Recover`, `Logger`.
Le middleware d'une feature (auth…) se lie une seule fois dans son `module.go`.

**CORS** (`engine.CORS`) enveloppe le routeur, appliqué dans `main.go` et non dans la chaîne par
défaut de l'engine : la liste d'origines est de la configuration (`ALLOWED_ORIGINS`), et l'engine
n'en prend pas. Il est le plus externe, ce qui est nécessaire — un preflight de navigateur ne
porte aucun token, donc il doit être tranché avant le middleware d'auth.

| Règle | Pourquoi |
| ----- | -------- |
| Jamais `*` | cette API répond à un token d'administration qui vit sur la machine de l'utilisateur |
| Égalité stricte sur l'origine | `https://flowlio.me.evil.test` passe n'importe quel test de sous-chaîne |
| Aucun `Allow-Credentials` | il n'y a pas de cookie dans ce produit ; le token part en `Authorization` |
| `Vary: Origin` toujours posé | sinon un cache sert à une origine les en-têtes calculés pour une autre |
| Requête sans `Origin` intacte | la CLI et le serveur MCP ne sont pas des navigateurs |

## Domaines (features)

| Clé module  | Domaine                          | Routes                                                                                                            | Dépendances inter-modules |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `workspace` | teams, projets, tokens d'agent    | `/api/workspace` : `POST/GET /teams`, `POST/GET /projects`, `POST/GET /tokens`, `DELETE /tokens/{id}`, `GET /whoami` | aucune                    |
| `task`      | backlog d'un projet + notes       | `/api/task` : `POST/GET /`, `GET/PATCH /{number}` — **une seule route d'écriture** : note et archivage sont des champs du PATCH | aucune                    |
| `issue`     | questions inter-projets + fil     | `/api/issue` : `POST/GET /`, `GET /{project}/{number}`, `POST /{project}/{number}/answer`                           | aucune                    |
| `inbox`     | état actionnable du projet        | `/api/inbox` : `GET /`                                                                                              | aucune                    |
| `overview`  | lecture team-scopée, supervision  | `/api/overview` : `GET /`, `GET /refs/{project}/{number}` — **lecture seule, aucune écriture**                       | aucune                    |
| `ref`       | résolution d'une référence CORE-34 | `/api/ref` : `GET /{project}/{number}` — **lecture seule, ne possède aucune table**                                 | `task` et `issue`, par le `FeatureRegistry` |

Portées `workspace` : les routes d'administration exigent un token `admin` (`AdminOnly`) ;
`GET /projects` et `GET /whoami` acceptent tout token valide et restent scopés à sa team. Un
token admin désigne la team visée par `?team=<slug>` ; un token de projet ne peut pas sortir de
la sienne, même en forçant le paramètre.

Portées `task` : **toutes** les routes exigent un token de portée `project`
(`requireProjectScope`, lié une fois dans `module.go`). Un backlog est le travail interne d'un
repo : aucune route ne prend de projet en paramètre, donc il n'existe aucune surface où un scope
pourrait être contourné. Un token admin, qui n'est lié à aucun projet, reçoit `403`.

`team_id` **et** `project_id` figurent dans chaque query de `sql/queries/tasks.sql` — y compris
l'insertion d'une note, alimentée par un `SELECT` scopé sur la tâche. L'isolation entre projets
d'une même team est couverte par `internal/feature/task/store/store_integration_test.go`.

**Dépendances entre tâches** (`task_dependencies`, `sql/queries/dependencies.sql`) : une arête dit
« A est bloquée par B jusqu'à ce que B atteigne S ». Quand B l'atteint — ou est archivée, puisqu'elle
n'atteindra alors plus jamais rien — l'arête est libérée, un `task.unblocked` est journalisé sur la
tâche DÉBLOQUÉE, et celle-ci repasse `todo` **si et seulement si** toutes ses arêtes sont levées et
qu'au moins une l'avait bloquée (`set_blocked`). Ces trois conditions vivent dans la query
`ClearTaskBlock`, indissociables, pour qu'aucun appelant ne puisse en oublier une. Le tout est écrit
dans la transaction du patch qui le déclenche : hors transaction, l'état « la bloquante est `done`,
la bloquée l'ignore » redeviendrait atteignable.

**Une arête ne traverse jamais un repo** (D42), et ce n'est pas le service qui le garantit : les
deux clés étrangères composites de la table partagent la même colonne `project_id`, donc la
dépendance inter-projets est **inexprimable**, pas seulement refusée. Prouvé sans passer par le
service dans `store/dependency_integration_test.go`. Le besoin inter-repos a déjà son objet :
l'issue. Un cycle est refusé à l'écriture par un parcours en Go du graphe actif du projet
(`service/cycle.go`) — fonction pure, donc prouvable sans base.

Portées `overview` : **les deux routes exigent un token `admin`** (`AdminOnly`, lié une fois dans
`module.go`). Il n'y a pas de gate mixte, et il ne faut pas en introduire une : c'est la seule
surface du produit qui lit une team ENTIÈRE, y compris le fil d'une conversation entre deux repos
dont l'appelant n'est ni l'auteur ni le destinataire. Sous `auth.Middleware`, un agent lirait les
questions de ses repos frères, et les tests d'isolation de `task` et `issue` resteraient verts.

La team vient toujours de la résolution serveur de `?team=<slug>` : aucun UUID n'entre ni ne sort
de cette surface. Un admin porteur d'une team y est enfermé — même garde que `workspace`, et il
est écrit aux deux endroits parce qu'une défense qui vit dans un autre fichier n'est pas une
défense.

Portées `ref` : **token de portée `project` exigé**, comme toute surface qu'un agent emprunte. Un
token admin est refusé plutôt que de désigner une cible : une référence se lit DEPUIS un projet, et
un principal qui n'en a pas n'a rien d'où lire.

Ce module ne possède aucune table et n'écrit rien. Il garde **une** query
(`sql/queries/ref.sql`) pour le seul fait qu'aucun de ses deux pairs ne peut lui donner : la clé de
son propre projet. C'est elle qui distingue `CORE-34` — la mienne, donc peut-être une tâche — de
`FRNT-34`, celle d'un frère, donc nécessairement une issue. Sans cette garde, la query de tâche,
scopée sur le projet du token, **trouverait** quelque chose : la tâche 34 de l'appelant, rendue
sous une référence qui nomme quelqu'un d'autre.

## Services transverses (`internal/core`)

| Paquet      | Rôle                                                                              |
| ----------- | --------------------------------------------------------------------------------- |
| `auth`      | Token → `Principal{TeamID, ProjectID, Scope}`, middleware `Middleware` / `AdminOnly`. Exposé par `CoreServices.Auth()`. |
| `bootstrap` | Émet le token d'administration au tout premier démarrage en mode local.            |
| `engine`    | Routeur racine, montage des modules, middleware global.                            |

## Interfaces inter-modules

**Une seule composition existe, et c'est `ref`** (FLWL-16, tranchée le 2026-08-05). Le compteur
d'un projet est partagé entre tâches et issues : un agent qui lit `CORE-34` ne sait pas laquelle
des deux c'est. `ref` répond à la question en une requête HTTP, là où la couche MCP en faisait
deux — sur le chemin que `check_inbox` alimente, donc le plus appelé du produit.

| Interface | Déclarée dans | Implémentée par | Consommée par |
| --- | --- | --- | --- |
| `module.TaskRefResolver` | `internal/core/module/module.go` | `task` (`provider.go`) | `ref` |
| `module.IssueRefResolver` | `internal/core/module/module.go` | `issue` (`provider.go`) | `ref` |

Trois choix de forme, et chacun a une raison qui ne se devine pas :

| Choix | Pourquoi |
| --- | --- |
| Les interfaces vivent dans `internal/core/module` | fournisseur et consommateur doivent nommer le **même** type pour que le type-assert du registre réussisse, et une feature n'importe pas une feature : ce paquet est le seul que les trois partagent déjà |
| La charge est du JSON brut (`json.RawMessage`) | ce que rend un résolveur est la vue API de la feature propriétaire ; nommer `taskservice.TaskDetail` ici ferait entrer une feature dans le core. `encoding/json` recopie un `RawMessage` tel quel, donc le coût est nul |
| Deux interfaces plutôt qu'une | leurs signatures se ressemblent ; une seule laisserait `ref` interroger le mauvais module et compiler, sur un chemin dont tout le métier est de distinguer une tâche d'une issue |

`ref` est un **consommateur pur** : il ne s'enregistre sous aucune clé utile, et il ne doit pas
— offrir la composition aux features qu'elle compose ouvrirait un cycle qui ne se verrait qu'à
l'exécution. Il ne porte **aucune écriture**, pour la même raison : muter le domaine d'autrui
depuis un module qui n'a pas de query pour porter le scope n'aurait aucun garde-fou.

En revanche, **plusieurs features partagent des tables** — ce n'est pas un import, donc
`check-cross-feature-imports.sh` ne le voit pas, et c'est pour cette raison que c'est écrit ici.
`overview` est le cas extrême et le plus propre : il lit sept tables dont six ne lui appartiennent
pas, et n'en écrit aucune (décision M3 #26 — lire la table d'un autre domaine par une query
scopée dédiée est permis, y écrire ne l'est pas).

| Table      | Propriétaire   | Autres écrivains / lecteurs                                              |
| ---------- | -------------- | ------------------------------------------------------------------------ |
| `projects` | `workspace`    | `task` et `issue` en **écriture** (`ClaimNextNumber`), `inbox`, `overview` et `ref` en lecture |
| `tasks`    | `task`         | `inbox` et `overview` en lecture (seau des tâches en cours)               |
| `issues`   | `issue`        | `inbox` et `overview` en lecture (seaux entrants et sortants)             |
| `events`   | `issue`        | `inbox` en lecture                                                        |
| `teams`    | `workspace`    | `overview` en lecture (résolution du slug en scope)                       |
| `tokens`   | `workspace`    | `auth` en lecture/écriture (`last_used_at`), `overview` en lecture (le pouls d'un repo) |
| `issue_messages` | `issue`  | `overview` en lecture — **la seule FK simple du lot**, donc la seule où la clause de team porte réellement |
| `task_notes` | `task`       | `overview` en lecture (`last_move`, et le détail d'une tâche)             |

La règle qui rend ce partage sûr : **toute query porte son scope de tenancy**, quelle que soit la
feature qui l'écrit. Une query partagée qui prendrait un identifiant nu serait une faille pour
toutes les features à la fois, pas seulement pour la sienne.

### Les deux règles de scope du dépôt

Depuis `overview`, « porter son scope » ne veut plus dire une seule chose. Le dépôt en compte
**deux**, et une relecture qui les confond laisse passer exactement la faille qu'elle cherchait.

| | Règle A — projet | Règle B — team |
| --- | --- | --- |
| Prédicat | `team_id = @team_id AND project_id = @project_id` | `team_id = @team_id` **seul** |
| Où | `tasks.sql`, `dependencies.sql`, `issues.sql`, `inbox.sql`, `ref.sql`, `trust.sql`, et les queries de token projet de `tokens.sql` | `overview.sql` |
| Sens du `team_id` | vient du principal (`Principal.TeamID`) | vient d'une **résolution serveur** du slug `?team=` (`OverviewTeamBySlug`), jamais d'un UUID client |
| Écriture | autorisée | **interdite** — lecture seule, vérifié par `scripts/check-overview-scope.sh` dans `make lint` |
| Gate | `requireProjectScope` | `AdminOnly` |
| Exposée en MCP | oui | **non** — un agent qui lit l'état de sa team détruit la promesse d'isolation |

Les deux règles ne se voisinent pas dans un même fichier de queries : une query team-seule et une
query projet-scopée sur les mêmes tables est la configuration où le copier-coller fuit (décision
M3 #24). Chaque fichier porte sa règle en tête, donc un lecteur qui ouvre `overview.sql` sans
contexte comprend l'absence de `project_id` sans ouvrir un autre fichier.

**Deux fichiers ne relèvent d'aucune des deux règles, et c'est délibéré :**

| Fichier | Ce qu'il porte | Pourquoi ce n'est pas la règle B |
| --- | --- | --- |
| `projects.sql` | `team_id` seul, **sans `AdminOnly`** | C'est l'annuaire de la team, lisible par un token de projet : métadonnées uniquement (clé, nom), acté dans [DESIGN-V1](DESIGN-V1.md) § Isolation. Faut-il le filtrer davantage ? Question ouverte, carte FLWL-44. |
| `teams.sql` | **aucun prédicat de tenancy** | `GET /teams` énumère toutes les teams de l'installation. Sans conséquence en mode `local` — un humain, un token admin — et **bloquant de M7** (n° 2 de la carte FLWL-9) le jour où l'installation est partagée. |

Écrire une query dans l'un de ces deux fichiers demande donc de savoir laquelle des quatre
situations on est en train de reproduire. C'est précisément la raison de ce tableau.

Ce que ça vaut, mesuré et non affirmé : `internal/feature/matrix_integration_test.go`
(`TestScopeRouteMatrix`) monte les six modules sur leurs vrais stores et couvre trois principaux
— projet, admin, aucun — contre les six préfixes de routes. Un `requireProjectScope` qui
accepterait `|| p.IsAdmin()`, ou un `AdminOnly` qui accepterait un scope projet, fait tomber une
case.

Contrainte de verrouillage à ne pas casser : `ClaimNextNumber` ne doit jamais écrire une colonne
de clé. Tant que c'est vrai, Postgres prend un `FOR NO KEY UPDATE`, compatible avec le
`FOR KEY SHARE` que l'insertion d'une issue pose sur ses deux projets parents — sinon deux agents
symétriques (FRNT→CORE et CORE→FRNT) s'interbloquent. Détail dans
[DESIGN-M3.md](DESIGN-M3.md).

Rappel du pattern, tel que `ref` l'exerce réellement : `main.go` enregistre chaque module sous sa
clé, le consommateur résout **lazily** (`registry.Get("task")`) et type-assert sur l'interface
déclarée dans `internal/core/module`. La résolution se fait au moment de la requête et **jamais
dans `NewModule`** : tous les modules sont construits avant que le premier ne soit enregistré, donc
un pair capturé à la construction serait toujours nil. Aucun import `internal/feature/<autre>` —
vérifié par `scripts/check-cross-feature-imports.sh` (hook + `make lint`).

Un pair absent du registre est une **panne de câblage**, jamais un « introuvable » : rendre 404
ferait passer une instance mal montée pour un backlog vide.

Toute nouvelle interface inter-module se documente ici **et** se valide avec l'humain.

## Services partagés (`CoreServices`)

`Auth()` uniquement. `Billing()` viendra avec le mode hosted (M7).
Un service feature-specific n'entre jamais dans `CoreServices`.

## Transactions

Le store expose un `Transactor` (`WithTx(ctx, func(Store) error) error`). `*sql.DB` ne fuite
jamais dans le service : celui-ci ne voit que l'interface store. Implémenté dans `task` et
`issue` ; `inbox` n'en a pas, parce qu'il ne fait que lire un état.

**L'imbrication est refusée, pas absorbée.** Un `WithTx` dans un `WithTx` ouvrirait une seconde
transaction sur une autre connexion du pool, qui attendrait le verrou détenu par la première sur
la ligne du projet — un interblocage qu'aucun test mono-thread ne révèle. Rejoindre silencieusement
la transaction en cours serait pire : l'extérieur committerait les écritures d'un appel interne
dont l'erreur aurait été avalée.
