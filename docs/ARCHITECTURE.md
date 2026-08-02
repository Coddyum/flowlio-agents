# ARCHITECTURE — flowlio-ia

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

## Domaines (features)

| Clé module  | Domaine                          | Routes                                                                                                            | Dépendances inter-modules |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `workspace` | teams, projets, tokens d'agent    | `/api/workspace` : `POST/GET /teams`, `POST/GET /projects`, `POST/GET /tokens`, `DELETE /tokens/{id}`, `GET /whoami` | aucune                    |
| `task`      | backlog d'un projet + notes       | `/api/task` : `POST/GET /`, `GET/PATCH /{number}` — **une seule route d'écriture** : note et archivage sont des champs du PATCH | aucune                    |
| `issue`     | questions inter-projets + fil     | `/api/issue` : `POST/GET /`, `GET /{project}/{number}`, `POST /{project}/{number}/answer`                           | aucune                    |
| `inbox`     | état actionnable du projet        | `/api/inbox` : `GET /`                                                                                              | aucune                    |

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

## Services transverses (`internal/core`)

| Paquet      | Rôle                                                                              |
| ----------- | --------------------------------------------------------------------------------- |
| `auth`      | Token → `Principal{TeamID, ProjectID, Scope}`, middleware `Middleware` / `AdminOnly`. Exposé par `CoreServices.Auth()`. |
| `bootstrap` | Émet le token d'administration au tout premier démarrage en mode local.            |
| `engine`    | Routeur racine, montage des modules, middleware global.                            |

## Interfaces inter-modules

Aucune interface Go inter-module : aucune feature n'en importe une autre, aucune ne passe par
`FeatureRegistry`.

En revanche, **trois features partagent des tables** — ce n'est pas un import, donc
`check-cross-feature-imports.sh` ne le voit pas, et c'est pour cette raison que c'est écrit ici :

| Table      | Propriétaire   | Autres écrivains / lecteurs                                              |
| ---------- | -------------- | ------------------------------------------------------------------------ |
| `projects` | `workspace`    | `task` et `issue` en **écriture** (`ClaimNextNumber`), `inbox` en lecture |
| `tasks`    | `task`         | `inbox` en lecture (seau des tâches en cours)                             |
| `issues`   | `issue`        | `inbox` en lecture (seaux entrants et sortants)                           |
| `events`   | `issue`        | `inbox` en lecture                                                        |

La règle qui rend ce partage sûr : **toute query porte son scope de tenancy**, quelle que soit la
feature qui l'écrit. Une query partagée qui prendrait un identifiant nu serait une faille pour
toutes les features à la fois, pas seulement pour la sienne.

Contrainte de verrouillage à ne pas casser : `ClaimNextNumber` ne doit jamais écrire une colonne
de clé. Tant que c'est vrai, Postgres prend un `FOR NO KEY UPDATE`, compatible avec le
`FOR KEY SHARE` que l'insertion d'une issue pose sur ses deux projets parents — sinon deux agents
symétriques (FRNT→CORE et CORE→FRNT) s'interbloquent. Détail dans
[DESIGN-M3.md](DESIGN-M3.md).

Rappel du pattern : le fournisseur s'enregistre (`registry.Register("b", api)`) dans son
`NewModule`, le consommateur résout lazily (`registry.Get("b")`) et type-assert sur une interface
qu'il déclare de son côté. Aucun import `internal/feature/<autre>` — vérifié par
`scripts/check-cross-feature-imports.sh` (hook + `make lint`).

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
