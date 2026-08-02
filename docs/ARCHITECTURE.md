# ARCHITECTURE — flowlio-ia

Carte des domaines et des interfaces inter-modules. À lire avant d'éditer une zone inconnue,
à mettre à jour dès qu'une feature ou une interface inter-module apparaît.

## État actuel

**M1 livré** : tenancy (teams, projets), tokens d'agent et authentification.
Tâches (M2) et issues inter-projets (M3) à venir. Décisions de conception :
[DESIGN-V1.md](DESIGN-V1.md).

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
| `internal/pkg/database/`      | `Connect(dsn)` → pool `*sql.DB` (driver pgx v5 en mode `database/sql`). |
| `sql/migrations/`             | golang-migrate — humain uniquement.                                   |
| `sql/queries/`                | Source des queries lues par sqlc.                                     |
| `sql/schema/`                 | Source de vérité du modèle, mise à jour après chaque migration.       |

## Routage

L'engine monte chaque module sous `/api/<Key()>/` et retire le préfixe (`http.StripPrefix`) :
un module déclare ses routes relativement à lui-même (`POST /x`, `DELETE /x/{id}`).

Middleware global (engine, appliqué à toutes les routes, dans l'ordre) : `Recover`, `Logger`.
Le middleware d'une feature (auth…) se lie une seule fois dans son `module.go`.

## Domaines (features)

| Clé module  | Domaine                          | Routes (préfixées `/api/workspace`)                                                                             | Dépendances inter-modules |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `workspace` | teams, projets, tokens d'agent    | `POST/GET /teams`, `POST/GET /projects`, `POST/GET /tokens`, `DELETE /tokens/{id}`, `GET /whoami`                  | aucune                    |

Portées : les routes d'administration exigent un token `admin` (`AdminOnly`) ; `GET /projects` et
`GET /whoami` acceptent tout token valide et restent scopés à sa team. Un token admin désigne la
team visée par `?team=<slug>` ; un token de projet ne peut pas sortir de la sienne, même en
forçant le paramètre.

## Services transverses (`internal/core`)

| Paquet      | Rôle                                                                              |
| ----------- | --------------------------------------------------------------------------------- |
| `auth`      | Token → `Principal{TeamID, ProjectID, Scope}`, middleware `Middleware` / `AdminOnly`. Exposé par `CoreServices.Auth()`. |
| `bootstrap` | Émet le token d'administration au tout premier démarrage en mode local.            |
| `engine`    | Routeur racine, montage des modules, middleware global.                            |

## Interfaces inter-modules

Aucune pour l'instant.

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
jamais dans le service : celui-ci ne voit que l'interface store.
