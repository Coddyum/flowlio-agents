# CLAUDE.md — <PROJECT_NAME>

> Remplacer `<PROJECT_NAME>` et la section **Projet** par le contexte réel.
> Tout le reste (architecture, flow, patterns, règles) est doctrine figée : ne pas diluer.

## Démarrage de session

Avant toute tâche, charger :

- `.claude/rules/module-system.md`, `feature-structure.md`, `code-conventions.md`,
  `file-sommaire.md` si la tâche touche une zone qu'ils couvrent.
- `docs/ARCHITECTURE.md` (carte des domaines + interfaces inter-modules) avant d'éditer une
  zone inconnue.

---

## Projet

> **À REMPLIR.** Une à trois phrases : ce que fait le produit, pour qui, périmètre de ce repo.
> Exemple de gabarit :

**<PROJECT_NAME>** est un back-end <type de produit> pour <cible>. Ce repo couvre
**uniquement le back-end** — pas d'accès au repo frontend.

---

## Stack

| Outil          | Version | Notes                            |
| -------------- | ------- | -------------------------------- |
| Golang         | 1.26.1  |                                  |
| Postgres       | 17      |                                  |
| net/http       | stdlib  | Pas de framework HTTP externe    |
| sqlc           | latest  | Génération des queries           |
| golang-migrate | latest  | Migrations manuelles uniquement  |
| Redis          | —       | Optionnel (cache, sessions)      |

Pas d'ORM. Pas de framework HTTP. Pas de dépendance externe ajoutée sans que ce soit demandé.

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
internal/pkg/            ← cache, config, crypto, cookies, database, monitoring
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

Détail complet : **`.claude/rules/feature-structure.md`**.

---

## Sommaire en tête de fichier

Tout fichier `.go` avec ≥ 2 déclarations top-level (func/type) doit avoir un bloc `// SOMMAIRE`
juste après `package xxx` (1 phrase de description + numéro de ligne par déclaration, pour sauter
directement au bon passage sans relire tout le fichier). Détail complet :
**`.claude/rules/file-sommaire.md`**.

---

## Base de données

- Migrations : **humain uniquement** — Claude ne lance jamais de migration.
- Queries SQL : dans `sql/queries/` uniquement, jamais dans un `.go`.
- `sqlc generate` : **humain uniquement** — Claude n'écrit jamais `internal/database/*.go` à la main.
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

---

## Ce que Claude ne fait pas

- Mettre de l'implémentation dans `store/store.go` ou une méthode dans `service.go` (contrats uniquement).
- Mettre du code service dans un fichier handler, ou inversement.
- Mélanger plusieurs actions métier dans `service/service.go`.
- Créer une feature sans sous-dossiers `handler/`, `service/`, `store/`.
- Lancer des migrations.
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
