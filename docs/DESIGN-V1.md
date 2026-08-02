# DESIGN v1 — flowlio-ia

Décisions arrêtées à partir de `docs/concept.md`. Ce document est le contrat de ce qu'on
construit en v1 ; `docs/ARCHITECTURE.md` reste la carte technique du repo.

## Décisions structurantes

| # | Décision | Conséquence |
| - | -------- | ----------- |
| 1 | **Pas de board ni de colonnes.** Une tâche porte un `status`. | Modèle aplati `team → project → task`. Une vue kanban est reconstituée en lecture si un humain la veut. |
| 2 | **v1 = tasks + issues + inbox en pull.** | Pas de daemon, pas de wake-up automatique. L'agent appelle `check_inbox` lui-même. |
| 3 | **La mémoire = décisions + contrats typés** (v2, modèle déjà anticipé). | Pas de blob mémoire ni d'embeddings. Recherche Postgres FTS + tags. |
| 4 | **Multi-tenant dès j1**, `team_id` dans chaque query store. | Adaptateur d'auth `local` en v1, `hosted` + billing ajoutés sans toucher aux stores. |
| 5 | **Zéro IA dans le produit.** | Tout comportement est déterministe et testable. |
| 6 | **Postgres 18 partout, jamais SQLite.** Prod hébergée sur Neon. | Un seul dialecte SQL, un seul jeu de queries sqlc, aucune migration en double. La friction d'installation en self-host est assumée et compensée par `docker compose`. |

## Modèle de domaine

```
team (tenant)
 └── project (= 1 repo, clé courte : FRNT, CORE)
      ├── task     ← travail interne au repo, l'agent de CE repo le gère
      └── issue    ← question inter-projets, dans la team uniquement
```

- **task** : `FRNT-34`, `status ∈ {todo, in_progress, blocked, done}`, `priority`, `deadline`,
  description markdown riche, notes de progression, archivage.
- **issue** : ouverte par le projet A vers le projet B, `state ∈ {open, answered, closed}`,
  fil de messages. Isolée des tâches de B : répondre à une issue ne pollue pas son backlog.
- **event** : journal append-only par team. Alimente `check_inbox` en v1 et servira de flux
  SSE au daemon de wake-up en v2 sans changer de modèle.

### Identifiants

`<PROJECT_KEY>-<n>` (`FRNT-34`) pour tasks et issues. Compteur par projet, incrémenté dans la
transaction d'insertion. Les agents ne manipulent jamais d'UUID.

## Isolation et permissions

Un token d'agent est scopé à **un projet**, dans une team.

| Ressource                      | Portée du token projet          |
| ------------------------------ | ------------------------------- |
| tasks de son projet            | lecture / écriture              |
| tasks des autres projets       | **aucun accès**                 |
| issues dont il est émetteur    | lecture / écriture              |
| issues dont il est destinataire| lecture / écriture              |
| autres issues de la team       | aucun accès                     |
| métadonnées projets de la team | lecture seule (clé, nom)        |
| autre team                     | **aucun accès**                 |

Le filtrage `team_id` + `project_id` est appliqué **dans le store**, jamais seulement dans le
handler : une query sans scope est un bug de sécurité, pas un oubli d'UI.

## Modes de déploiement

| Mode     | Auth                                                    | Billing |
| -------- | ------------------------------------------------------- | ------- |
| `local`  | Pas de compte. `flowlio init` crée team + projet + token | —       |
| `hosted` | Comptes + JWT (v2)                                       | Stripe (v2) |

Un seul port `Auth()` dans `CoreServices`, deux adaptateurs. `buildModules()` ne monte le module
`billing` que si `MODE=hosted`.

## Sécurité (repo open source — non négociable)

- Token : `flw_<prefix>_<secret>`, secret de 32 octets aléatoires. Stockage **hashé SHA-256**,
  `prefix` indexé pour le lookup. Le secret n'est affiché **qu'une fois**, à la création.
  Pas d'argon2id ici : un secret de 256 bits n'a rien à craindre d'un dictionnaire, et un KDF à
  coût mémoire sur le chemin d'authentification serait un vecteur de déni de service. argon2id
  reste prévu pour les mots de passe des comptes hosted (M7).
- Jamais de token dans les logs, les erreurs, les traces ou les messages d'issue.
- Comparaison de secret en temps constant.
- Aucun secret en dur dans le binaire ni dans le repo ; `.env` ignoré par git, `.env.example`
  sans valeur réelle.
- Rate limiting sur la résolution de token.
- Révocation : `revoked_at`, vérifiée à chaque requête.

## Schéma

Livré (migrations `000001_init`, `000002_token_scope`) :

```
teams(id, slug, name, created_at, updated_at)
projects(id, team_id, key, name, next_number, created_at, updated_at)   unique(team_id, key)
tokens(id, scope, team_id, project_id, name, prefix, secret_hash,
       created_at, last_used_at, revoked_at)                            unique(prefix)
```

`tokens.scope` vaut `admin` (amorçage local, aucune team) ou `project` (agent, team et projet
obligatoires) — une seule table, donc un seul chemin de vérification de secret.

À venir :

```
tasks(id, team_id, project_id, number, title, body_md, status,
      priority, deadline, created_at, updated_at, archived_at)     unique(project_id, number)   -- M2
task_notes(id, task_id, body_md, created_at)                                                    -- M2
issues(id, team_id, project_id, author_project_id, number, title,
       state, created_at, updated_at, closed_at)                   unique(project_id, number)   -- M3
issue_messages(id, issue_id, author_project_id, body_md, created_at)                            -- M3
events(id, team_id, project_id, kind, subject_type, subject_id, created_at)                     -- M3
token_cursors(token_id, last_event_id)                                                          -- M3
```

Une issue appartient au projet **destinataire** (`project_id`) et mémorise son auteur
(`author_project_id`), comme une issue GitHub appartient au repo sur lequel elle est ouverte.
Elle tire son numéro du compteur de ce projet : tasks et issues partagent la même suite, donc
`CORE-34` désigne toujours un seul objet.

## Découpage en modules (`internal/feature/`)

| Module      | Clé         | Responsabilité                                              |
| ----------- | ----------- | ----------------------------------------------------------- |
| `workspace` | `workspace` | teams, projects, tokens d'agent (création, révocation)       |
| `task`      | `task`      | tâches d'un projet + notes de progression + archivage        |
| `issue`     | `issue`     | issues inter-projets, fil de messages, changements d'état    |
| `inbox`     | `inbox`     | lecture du journal d'événements depuis le curseur du token   |

`auth` n'est pas une feature : c'est un service transverse de `internal/core`, exposé via
`CoreServices.Auth()` (résolution token → `Principal{TeamID, ProjectID}` + middleware).

Dépendance inter-modules attendue : `issue` et `task` émettent des événements consommés par
`inbox`. Passe par `FeatureRegistry`, jamais par un import direct — ou par un port `EventWriter`
côté `internal/store/` si l'écriture doit être transactionnelle avec la tâche/issue.

## Surface MCP (v1)

Petite par conception : chaque outil superflu coûte des tokens à chaque tour d'agent.

```
whoami                     → projet, team, portée du token
list_tasks(status?, limit) → backlog du projet courant
get_task(key)              → détail + notes
create_task(...)           → nouvelle tâche
update_task(key, ...)      → statut, priorité, deadline, description
add_task_note(key, body)   → progression
create_issue(to_project, title, body)
list_issues(role=incoming|outgoing, state?)
answer_issue(key, body)
close_issue(key)
check_inbox()              → événements depuis le curseur, puis avance le curseur
```

## Binaires

- `cmd/api` — serveur HTTP (existant).
- `cmd/flowlio` — CLI humain (`init`, `project`, `token`, `task`, `issue`) **et** serveur MCP
  stdio via `flowlio mcp`. Même binaire, même auth, même client HTTP : local et hosted ne
  divergent pas.

## Hors périmètre v1 (assumé)

- Wake-up automatique des sessions (daemon local + SSE) — le journal d'événements est déjà là
  pour l'accueillir.
- Décisions / contrats versionnés — modèle prévu, non implémenté.
- Comptes hosted, JWT, Stripe.
- Toute interface web.
