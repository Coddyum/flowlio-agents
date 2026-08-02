# Règle — système de modules

Référencée par `CLAUDE.md`. Détail de la brique "Système de modules" + "Architecture imposée".

## Interfaces et wiring

- Interfaces dans `internal/core/module/module.go`. Wiring dans `cmd/api/main.go`. Pas de `func init()`.
- `CoreServices` expose uniquement des **services partagés** (ex: `Auth()`, `Billing()`), jamais un
  service feature-specific.
- `ModuleConfig` regroupe toute l'infra partagée (DB, RawDB, Config, Ctx, Cache…) — un seul
  paramètre par `NewModule()`.

## Règles inter-modules

- Les modules **n'importent jamais** d'autres modules directement. À vérifier automatiquement
  (hook bloquant sur édition + `make lint`).
- Toute dépendance inter-features passe par `FeatureRegistry.Get("clé")` ou `CoreServices`.
- Ajouter une interface inter-module dans `module.go` = fichier critique, valider avec l'humain.
- Si `FeatureRegistry` est reçu mais jamais utilisé, le supprimer.
- Carte des interfaces existantes : `docs/ARCHITECTURE.md`.

## Autres règles structurelles

- **Middleware** : lié une fois dans `module.go`, jamais à l'intérieur des handlers.
- **Config** : `NewModule(cfg module.ModuleConfig)` — jamais de params directs (db, secret, timeout…).
- **Store** : le service reçoit une interface locale, jamais `*database.Queries` directement.
- **Transactions** : exposer un `Transactor` dans le store — `*sql.DB` ne fuite jamais dans le service.
- **Singletons** : pas de `var` globaux mutables — tout état passe par `CoreServices` ou `ModuleConfig`.

## Taille des fichiers

- Fichier `.go` > 300 lignes (hors `internal/database` généré et `_test.go`) → à découper.
