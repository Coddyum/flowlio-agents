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
- Rate limiting sur la résolution de token — calibré ci-dessous.
- Révocation : `revoked_at`, vérifiée à chaque requête.
- Rejeu d'une création interrompue : aucune déduplication, décision argumentée et vérifiée par
  exécution dans `docs/DECISION-idempotence.md`.

### Calibrage du rate limiting

Livré dans `internal/core/auth/rate_limit.go`, `trusted_tokens.go` et `request_source.go`.

**Ce que ce limiteur protège, et ce qu'il ne protège pas.** Il ne protège **pas** contre la
découverte d'un token : un secret fait 32 octets aléatoires, soit 2^256 possibilités, et aucun
seuil ne change cette arithmétique — c'est l'entropie qui tient. Il protège contre la
**consommation de ressources** par une source qui échoue en boucle : un aller-retour Postgres et
un SHA-256 par tentative.

Cette distinction commande le reste : puisque ce contre quoi on se défend est déjà impossible,
tout mécanisme capable de refuser un token **valide** est un bilan négatif.

**Un seul seau**, à fenêtre fixe d'une minute, consommé **avant** l'aller-retour store — compter
après laissait passer toute une rafale concurrente pendant la latence de la base.

| Seau               | Seuil | Ce qu'il borne                                          |
| ------------------ | ----- | --------------------------------------------------------- |
| `maxAttemptsPerIP` | 120   | les tentatives de tokens distincts depuis une même source   |

Le seuil est volontairement large : il borne une consommation de ressources, pas une force
brute. Le serrer refuserait des agents légitimes à froid derrière un même NAT sans rien gagner.

**Le seau par préfixe a été supprimé après revue.** Il existait pour freiner l'acharnement sur un
token précis. Le préfixe étant la partie **publique** du token, il s'est révélé être le seul
moyen de couper un agent légitime : mesuré, 11 requêtes par minute sur le préfixe d'une victime
lui faisaient refuser son token valide, fenêtre après fenêtre, et 4 400 requêtes depuis une seule
source coupaient 400 victimes à la fois. En face il ne rachetait rien. Un dispositif qui ne
défend rien et qui coupe les légitimes se supprime, il ne se recalibre pas.

**Un token valide ne consomme jamais de quota**, par deux mécanismes indexés sur l'empreinte
SHA-256 du **token complet** — jamais sur le préfixe :

1. les requêtes concurrentes portant le même token partagent **une seule** charge. Un groupe
   cesse d'accueillir dès sa première réponse : sans cette borne, un flux pipeliné entretenait un
   groupe indéfiniment et passait sans limite (3 200 requêtes en 480 ms, mesurées). La borne
   exacte est donc `maxAttemptsPerIP × concurrence`, et seulement pour des requêtes portant le
   même token, dont la répétition n'apprend rien à l'attaquant ;
2. un token qui s'est authentifié est exempté de quota (24 h), exemption **retirée au premier
   refus**, ce qui fait tomber un token révoqué.

Ce n'est pas un cache d'authentification : chaque requête va quand même au store et compare le
secret, la révocation reste immédiate.

**Une seule issue rend la charge : l'authentification réussie.** Ni l'échec, ni la panne du
store, ni l'abandon du client. Une version précédente remboursait aussi les pannes, au nom de la
disponibilité pendant un incident ; c'était un contournement complet, parce que l'attaquant
provoque lui-même cette issue en abandonnant sa requête HTTP — le contexte annulé remonte comme
une panne et rembourse la charge que la requête jumelle vient de payer. Le prix de ce
renversement est borné : pendant un incident l'API ne répond de toute façon pas, et un token déjà
authentifié reste exempté, donc les agents en session ne sont pas touchés.

Limites connues, assumées, non compensées ailleurs :

- **la boucle locale est exemptée du seau, donc le limiteur ne freine rien en mode local.** C'est
  cohérent avec le modèle de menace, pas un oubli : un attaquant capable d'émettre depuis
  `127.0.0.1` lit déjà le fichier de credentials, il n'a aucune raison de deviner un token. Ce
  limiteur défend le mode hosted, où la source d'une requête est une information ; en local,
  c'est le système de fichiers qui protège. Corollaire utile : la boucle locale ne crée **aucune**
  clé de cache, donc aucune famille de clés n'est fabricable en masse ;
- le chemin bloqué calcule bien un SHA-256 mais ne touche pas la base : sa **latence** distingue
  « limité » de « refusé ». L'aligner supposerait d'offrir la requête que le limiteur existe pour
  refuser ;
- NAT, conteneur ou proxy partagé : un voisin bruyant peut faire refuser un token valide **pas
  encore authentifié** dans le process courant. À traiter le jour où un proxy de confiance
  existe, par configuration explicite, jamais en faisant confiance à `X-Forwarded-For` ;
- plusieurs instances de l'API multiplient la limite effective par leur nombre, chacune portant
  son propre compteur mémoire. Le jour où ça arrive, c'est le cache qui change.

## Schéma

Livré (migrations `000001_init`, `000002_token_scope`, `000003_tasks`) :

```
teams(id, slug, name, created_at, updated_at)
projects(id, team_id, key, name, next_number, created_at, updated_at)   unique(team_id, key)
                                                                        unique(id, team_id)
tokens(id, scope, team_id, project_id, name, prefix, secret_hash,
       created_at, last_used_at, revoked_at)                            unique(prefix)
tasks(id, team_id, project_id, number, title, body_md, status,
      priority, deadline, created_at, updated_at, archived_at)          unique(project_id, number)
task_notes(id, task_id, body_md, created_at)
```

`tokens.scope` vaut `admin` (amorçage local, aucune team) ou `project` (agent, team et projet
obligatoires) — une seule table, donc un seul chemin de vérification de secret.

`tasks` porte un `team_id` **dénormalisé** pour que chaque query puisse embarquer son scope de
tenancy complet sans jointure. La clé étrangère composite `(project_id, team_id)` vers
`projects (id, team_id)` — d'où l'unicité ajoutée sur `projects` — garantit que cette
dénormalisation ne peut jamais diverger : une tâche dont le `team_id` ment est impossible en base,
pas seulement improbable. Le même schéma s'appliquera aux issues.

`task_status ∈ {todo, in_progress, blocked, done}`,
`task_priority ∈ {low, normal, high, urgent}` (défaut `normal`).

Livré aussi (migration `000004_issues`) :

```
issues(id, team_id, project_id, author_project_id, number, title,
       state, created_at, updated_at, closed_at)                   unique(project_id, number)
issue_messages(id, issue_id, author_project_id, body_md, created_at)
events(id, team_id, project_id, actor_project_id, kind, subject_type, subject_id, created_at)
token_cursors(token_id, last_event_id, updated_at)
```

`events` porte un `actor_project_id` que le modèle annoncé n'avait pas, et **check_inbox ne lit
pas ce journal comme un flux** : il renvoie l'état actionnable courant, et le curseur ne sert
qu'au drapeau « nouveau ». Raison complète dans [DESIGN-M3.md](DESIGN-M3.md) — c'est ce qui rend
sans conséquence le trou de séquence d'un compteur `bigserial`, au lieu d'en faire une classe de
bugs.

Une issue appartient au projet **destinataire** (`project_id`) et mémorise son auteur
(`author_project_id`), comme une issue GitHub appartient au repo sur lequel elle est ouverte.
Elle tire son numéro du compteur de ce projet : tasks et issues partagent la même suite, donc
`CORE-34` désigne toujours un seul objet.

Conséquence sur la surface MCP : `get(ref)` n'est pas typé. Un agent qui lit `CORE-34` dans un
commit ou dans son inbox ne sait pas si c'est une tâche ou une issue — deux outils typés
échoueraient une fois sur deux.

## Découpage en modules (`internal/feature/`)

| Module      | Clé         | Responsabilité                                              | État |
| ----------- | ----------- | ----------------------------------------------------------- | ---- |
| `workspace` | `workspace` | teams, projects, tokens d'agent (création, révocation)       | livré |
| `task`      | `task`      | tâches d'un projet + notes de progression + archivage        | livré |
| `issue`     | `issue`     | issues inter-projets, fil de messages, changements d'état    | livré |
| `inbox`     | `inbox`     | état actionnable du projet (trois seaux) + curseur du token  | livré |

`auth` n'est pas une feature : c'est un service transverse de `internal/core`, exposé via
`CoreServices.Auth()` (résolution token → `Principal{TeamID, ProjectID}` + middleware).

Dépendance inter-modules attendue : `issue` et `task` émettent des événements consommés par
`inbox`. Passe par `FeatureRegistry`, jamais par un import direct — ou par un port `EventWriter`
côté `internal/store/` si l'écriture doit être transactionnelle avec la tâche/issue.

## Surface MCP (v1)

Petite par conception : chaque outil superflu coûte des tokens à **chaque tour** d'agent.

Livrés (M2) :

```
whoami                                → projet, team, portée du token
list_tasks(status?, limit?, archived?) → backlog du projet courant
get_task(key)                         → détail + fil de notes
create_task(title, body?, ...)        → nouvelle tâche, renvoie sa clé
update_task(key, ..., archive?)       → statut, priorité, deadline, description, archivage
add_task_note(key, body)              → progression
```

À venir (M3) :

```
create_issue(to_project, title, body)
list_issues(role=incoming|outgoing, state?)
answer_issue(key, body)
close_issue(key)
check_inbox()              → événements depuis le curseur, puis avance le curseur
```

**Aucun outil n'accepte de projet en paramètre** (sauf `to_project` d'une issue, qui désigne un
destinataire et non un scope de lecture) : le projet vient du token. Il n'existe donc aucun appel
MCP capable de désigner le backlog d'un autre projet.

`archive_task` a été fusionné dans `update_task` sous forme de drapeau : un septième outil se
paierait dans le contexte de chaque tour pour une action que personne n'appelle sans d'abord
passer la tâche en `done`.

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
