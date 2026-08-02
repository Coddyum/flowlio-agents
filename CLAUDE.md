# CLAUDE.md — flowlio-ia

> Architecture, flow, patterns et règles = doctrine figée : ne pas diluer.
> Référence complète du squelette : `blueprint/ARCHITECTURE-BLUEPRINT.md`.

## Démarrage de session

**Premier geste, avant toute autre chose : lire le board Flowlio.** Ce projet se développe sur
plusieurs sessions ; l'état réel de ce qui reste à faire n'est pas dans cette conversation, il
est dans le tracker.

```
mcp__flowlio__list_teams → team « Flowlio » (FLOWL)
mcp__flowlio__list_projects → projet « FLOWLIO_IA » (FLWL)
mcp__flowlio__list_project_tasks → colonnes In progress, puis Blocked / decision, puis Ready
```

Une tâche restée dans `In progress` signale une session interrompue : la reprendre avant d'en
ouvrir une nouvelle. Protocole complet (choix, mise à jour, archivage, secrets) :
**`.claude/rules/flowlio-workflow.md`**.

Aucun fichier de suivi en markdown dans ce dépôt (`PROGRESS.md`, `TODO.md`, `NEXT-STEPS.md`…) :
le board porte l'état, `docs/` porte les décisions.

Charger ensuite, selon la zone touchée :

- `.claude/rules/module-system.md`, `feature-structure.md`, `code-conventions.md`,
  `file-sommaire.md` si la tâche touche une zone qu'ils couvrent.
- `docs/ARCHITECTURE.md` (carte des domaines + interfaces inter-modules) avant d'éditer une
  zone inconnue.
- `docs/DESIGN-V1.md` pour le périmètre des jalons et les décisions déjà tranchées.

---

## Projet

**flowlio-ia** est un gestionnaire de projets **pour agents IA** (Claude Code, Codex, OpenCode),
pas pour humains. Modèle : `team → project (= 1 repo) → tasks | issues`. Les tâches sont le
travail interne d'un repo ; les issues sont les questions qu'un repo adresse à un repo frère de
la même team. Aucune IA dans le produit : tout est déterministe.

Interface : **CLI + MCP uniquement**, jamais de front web. Deux modes : `local` (open source,
aucun compte) et `hosted` (comptes + Stripe, à venir).

Périmètre de ce repo : tout le produit — API, CLI, serveur MCP.

Jalons et décisions de conception : `docs/DESIGN-V1.md`. Concept d'origine : `docs/concept.md`.

**État : M1 livré** (tenancy, tokens, auth). Prochain : M2 (tâches + MCP), puis M3 (issues +
inbox), qui est le cœur différenciant du produit.

---

## Stack

| Outil          | Version | Notes                                                        |
| -------------- | ------- | ------------------------------------------------------------ |
| Golang         | 1.26.1  |                                                              |
| Postgres       | 18      | dev : docker compose ; prod : **Neon**                        |
| pgx            | v5      | via l'adaptateur `database/sql` (requis par le `Transactor`)  |
| net/http       | stdlib  | Pas de framework HTTP externe                                 |
| sqlc           | 1.30    | Génération des queries                                        |
| golang-migrate | 4.19    | Migrations manuelles uniquement                               |
| go-cache       | latest  | Cache mémoire process. Pas de Redis.                          |

Pas d'ORM. Pas de framework HTTP. Pas de Redis. **Pas de SQLite** : une seule base, Postgres, en
dev comme en prod. Pas de dépendance externe ajoutée sans que ce soit demandé.

### Neon

Le dev tourne sur la même version majeure que la prod (18) : un écart de majeure est une classe
de bugs qui n'apparaît qu'après déploiement.

Deux endpoints, deux usages :

| Endpoint            | Usage                        | DSN                                                    |
| ------------------- | ---------------------------- | ------------------------------------------------------ |
| direct              | migrations (`make up-prod`)  | `?sslmode=require`                                      |
| mutualisé `-pooler` | l'API                        | `?sslmode=require&default_query_exec_mode=exec`         |

PgBouncer en mode transaction ne garantit pas qu'une requête préparée survive d'une requête à
l'autre : sans `default_query_exec_mode=exec`, pgx échoue par intermittence **sous charge, en
production uniquement**. `database.Connect` refuse donc de démarrer sur un endpoint `-pooler`
sans ce paramètre.

---

## Architecture

Hexagonal Architecture (Ports & Adapters) + système de modules/plugins. Carte complète des
domaines et interfaces inter-modules : **`docs/ARCHITECTURE.md`**.

```
cmd/api/main.go          ← point d'entrée, seul endroit autorisé pour log.Fatal
internal/core/           ← engine, interfaces Module/CoreServices/FeatureRegistry, services partagés
internal/feature/<nom>/  ← un module = handler/ + service/ + store/
internal/store/          ← interfaces store globales / composition inter-features
internal/database/       ← code généré sqlc (ne pas éditer à la main)
internal/pkg/            ← cache, config, database
sql/                     ← migrations / queries (sqlc) / schema
```

Détail système de modules, règles inter-modules, limite de taille de fichier :
**`.claude/rules/module-system.md`**.

---

## Flow de données — règle absolue

```
handler  →  service  →  store  →  DB
```

| Couche      | Accès autorisé        | Interdit                               |
| ----------- | --------------------- | -------------------------------------- |
| **handler** | service uniquement    | store, `*database.Queries`, `*sql.DB`  |
| **service** | store (via interface) | `*database.Queries`, `*sql.DB` directs |
| **store**   | `*database.Queries`   | logique métier, appels HTTP            |

Un handler ne connaît pas le store. Un service ne connaît pas sqlc. Aucune exception.

---

## Patterns obligatoires

Toute feature suit `handler/` + `service/` + `store/`, sans exception. `service.go` et `store.go`
sont des **contrats uniquement** (interface + struct + constructeur, jamais d'implémentation).

> **RÈGLE CRITIQUE** : un fichier est soit handler, soit service, jamais les deux.

Ajouter une feature = créer `internal/feature/<nom>/` puis une ligne dans `buildModules()`
(`cmd/api/main.go`). Rien d'autre.

Détail complet : **`.claude/rules/feature-structure.md`**.

---

## Sommaire en tête de fichier

Tout fichier `.go` avec ≥ 2 déclarations top-level (func/type) doit avoir un bloc `// SOMMAIRE`
juste après `package xxx` (1 phrase de description + numéro de ligne par déclaration, pour sauter
directement au bon passage sans relire tout le fichier). Détail complet :
**`.claude/rules/file-sommaire.md`**.

---

## Base de données

Délégation actée le 2026-08-02 : Claude gère le cycle de schéma **en dev**, la prod reste humaine.

- Migrations : Claude les écrit dans `sql/migrations/` et applique `make up-dev` sur la base
  locale.
- **`make up-prod` : humain exclusivement.** Aucune exception.
- **Migration destructrice** (`DROP`, `ALTER` avec perte de données, `TRUNCATE`) : accord humain
  explicite **avant** exécution, même en dev.
- `make sqlc` : Claude peut le lancer. `internal/database/*.go` reste du code généré —
  jamais écrit ni corrigé à la main.
- Queries SQL : dans `sql/queries/` uniquement, jamais dans un `.go`.
- `sql/schema/` : source de vérité du modèle de données, mise à jour après chaque migration.

---

## Auth (si applicable)

- JWT : access token + refresh token.
- Rate limiting sur les endpoints d'auth.
- Sessions multiples supportées.
- Cookies HTTP-only pour stocker les tokens côté client.

---

## Conventions de code

Nommage, gestion des erreurs, principes (performance/DRY/SRP), style Go idiomatique :
**`.claude/rules/code-conventions.md`**.

---

## Sécurité

Lors de l'exploration, si un bug ou une faille est détecté :

1. Commentaire inline : `// BUG TODO FIX: <ce qui se passe et pourquoi c'est un problème>`.
2. Ou note dans `errors.md` à la racine si le problème est cross-fichiers.

Ne pas chercher activement des failles hors du périmètre de la tâche. Si ça saute aux yeux,
le noter et continuer.

---

## Garde-fous — ne jamais déclarer une tâche terminée si :

- `go build` échoue,
- `go vet` échoue,
- les tests échouent,
- un sommaire de fichier (`// SOMMAIRE`) est manquant ou désynchronisé.

`make check` = vet + tests. `make lint` = golangci-lint + imports inter-features + taille fichiers.

Hook `PostToolUse` sur édition `.go` : `scripts/hook-go-postedit.sh` (build + vet + imports
inter-features + sommaire), exit 2 bloquant.

---

## Ce que Claude ne fait pas

- Commencer à coder sans avoir lu le board Flowlio, ou finir une session sans l'avoir mis à jour.
- Créer un fichier de suivi markdown là où le board fait le travail.
- Écrire un token, un DSN ou un secret dans une description de tâche Flowlio.
- Mettre de l'implémentation dans `store/store.go` ou une méthode dans `service.go` (contrats uniquement).
- Mettre du code service dans un fichier handler, ou inversement.
- Mélanger plusieurs actions métier dans `service/service.go`.
- Créer une feature sans sous-dossiers `handler/`, `service/`, `store/`.
- Lancer `make up-prod`, ou une migration destructrice sans accord explicite.
- Écrire le code généré par sqlc à la main.
- Écrire des queries SQL dans des fichiers `.go`.
- Utiliser `log.Fatal` hors de `main.go`.
- Faire appeler un handler directement un store ou `*database.Queries`.
- Faire appeler un service directement `*database.Queries` ou `*sql.DB`.
- Mettre des valeurs de config en paramètres directs de `NewModule()` (passer `ModuleConfig`).
- Répéter les dépendances middleware sur chaque route (lier une fois).
- Créer des `var` globaux pour de l'état mutable partagé.
- Importer une feature depuis une autre feature.
- Modifier l'interface `Module` ou l'engine sans validation explicite.
- Ajouter des dépendances externes sans que ce soit demandé.
- Créer des abstractions ou helpers non demandés.
- Utiliser `func init()`.
