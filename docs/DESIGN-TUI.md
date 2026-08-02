# DESIGN TUI — surface de lecture team-scopée et supervision humaine

> Note de conception produite le 2026-08-02 par un fan-out d'agents (quatre angles indépendants,
> une critique adversariale, une synthèse), **avant** écriture du code. Elle répond à `FLWL-20`.
> Elle complète `DESIGN-V1.md` (contrat de périmètre) et `DESIGN-M3.md` (modèle des issues).
>
> Statut : décisions **tranchées, pas encore appliquées**. Une session qui implémente lit d'abord
> les décisions, puis le SQL littéral, puis le découpage en tâches. Un écart entre ce document et
> le code se corrige dans le code, ou se documente ici avec sa raison.

---

## État vérifié du dépôt au moment de cette note

M1 → M4 sont livrés et verts. 9 outils MCP exposés. Faits établis par lecture directe, sur
lesquels toute la note s'appuie :

| Fait | Preuve |
| ---- | ------ |
| Un token admin porte `TeamID = uuid.Nil` et `ProjectID = uuid.Nil` | `sql/queries/tokens.sql` `CreateAdminToken` force `NULL, NULL` ; `internal/core/auth/auth.go:47-55` |
| Aucune query du dépôt ne lit `tasks`/`issues`/`issue_messages` sans prédicat de projet | `sql/queries/{tasks,issues,inbox}.sql`, vérifié ligne à ligne |
| La seule query team-seule existante est `InboxCursor`, qui renvoie un entier | `sql/queries/inbox.sql:15` |
| `requireProjectScope` refuse un admin par égalité stricte, dans `task`, `issue`, `inbox` | trois copies identiques, décision M3 #25 |
| `teamFor` **ignore** `p.TeamID` pour un admin et honore `?team=` | `internal/feature/workspace/handler/handler.go:117-129` — **lu, confirmé** |
| `tokens_scope_shape` autorise déjà `scope='admin' AND team_id IS NOT NULL` | `sql/schema/schema.sql:258` — la forme est légale en base, produite par aucun code |
| `issue_messages` n'a pas de `team_id` et sa FK vers `projects` est **simple**, pas composite | `schema.sql:503-504` (`issue_messages_author_project_id_fkey`) |
| Les FK de `issues` et `tasks` vers `projects` sont **composites** `(id, team_id)` | décision M3 #14 |
| `CreateTaskNote` n'écrit **pas** `tasks.updated_at` | `sql/queries/tasks.sql:60-68` |
| `tokens.last_used_at` est **nullable** et n'est lu par personne | `schema.sql:253`, un seul `UPDATE` dans `tokens.sql:26` |
| Ordre de l'enum `task_priority` : `low, normal, high, urgent` | `schema.sql:47-52` |
| Prochaine migration disponible : **`000006`** | `sql/migrations/` s'arrête à `000005_task_deadline_bound` |
| `go.mod` a 3 dépendances directes ; `golang.org/x/text` est déjà présent en indirecte | via pgx v5 |
| Le limiteur est désarmé sur la boucle locale, et un token authentifié est exempté du seau | `auth/request_source.go:37-39`, `auth/rate_limit.go:186-189` |
| `client.FromCredentials` exige `FLOWLIO_API_URL` **et** `FLOWLIO_TOKEN` **ensemble** | `internal/pkg/client/client.go:74-84` |
| `auth.contextKey` est privé au paquet : aucun test hors `package auth` ne peut injecter un Principal | `auth/middleware.go:25-27` |

---

## Le problème, en une phrase

Il y a deux utilisateurs et un seul est servi. L'agent a MCP et 9 outils. L'humain, qui supervise
N agents sur N repos, a `flowlio task list`, un repo à la fois, avec un token par repo. Il ne veut
pas écrire de tâches : il veut savoir **qui attend une réponse de qui, quel agent est bloqué,
quelle question traîne depuis trois jours**.

Le vrai obstacle n'est pas le rendu. **L'API n'a aucune lecture à l'échelle de la team.** Chaque
query est scopée à un projet via `Principal.ProjectID`, et c'est la promesse centrale du produit.
Un token admin porte `TeamID`/`ProjectID` vides : il n'existe littéralement aucun moyen de répondre
à « montre-moi l'état des cinq repos ». C'est ça, le travail de ce jalon. Le TUI vient après, et
peut-être jamais.

---

## Décisions tranchées

| # | Décision | Conséquence |
| - | -------- | ----------- |
| 1 | **Un cinquième module `internal/feature/overview/`, monté sur `/api/overview/`, en LECTURE SEULE.** Pas d'extension d'`inbox`. | `inbox` scope par `project_id`, `overview` par `team_id` seul : les voisiner dans un même store est la configuration exacte où le copier-coller fuit (décision M3 #24). `inbox` a de plus un gate unique ; y ajouter des routes `AdminOnly` crée un module à deux portées, donc une route ajoutée demain qui hérite du mauvais middleware sans que ça se voie. |
| 2 | **`AdminOnly`, lié une fois dans `overview/module.go`. Jamais `auth.Middleware`.** | Monter ces routes sous « tout token valide » livrerait à l'agent DOCS le fil FRNT→CORE et les titres de tâches de CORE : la clause de visibilité canonique (M3 #8) est *absente par conception* de ces queries. Ça contredit une ligne littérale de `DESIGN-V1.md` § Isolation (« tasks des autres projets : aucun accès ») et **les huit tests d'isolation existants resteraient verts** — ils testent les queries de `task`/`issue`, pas celles d'`overview`. La régression passerait la CI. |
| 3 | **La team est désignée par `?team=<slug>`, résolue en handler, jamais fournie en UUID.** | Patron déjà en production (`workspace/handler/handler.go:117-129`). Zéro migration, zéro touche à `internal/core/auth`. Le `team_id` ne descend jamais du principal : le jour où un scope dédié arrive, seul `teamFor` change, ni le service ni le store ne bougent d'une ligne. |
| 4 | **Aucun troisième scope `team` en v1.** | La proposition est bonne sur le fond — l'admin est le mauvais credential pour un terminal ouvert huit heures — mais elle touche `ALTER TYPE token_scope`, le `CHECK`, `TokenRecord` (`auth/store.go`, en-tête « CONTRAT UNIQUEMENT »), `authenticate.go`, `RevokeProjectToken`/`ListProjectTokens` qui codent en dur `scope = 'project'`, et `workspace/service/tokens.go` qui **exige un `ProjectKey`**. C'est un jalon de tenancy, pas un prérequis de TUI. Inscrit comme bloquant de M7. |
| 5 | **PRÉREQUIS BLOQUANT : corriger `teamFor` avant tout le reste.** Aucune ligne `tokens` de forme `scope='admin' AND team_id IS NOT NULL` ne doit exister avant ce correctif. | `if !p.IsAdmin() { return p.TeamID }` : pour un admin, `p.TeamID` n'est **jamais lu**. Un « admin épinglé à une team » passerait `IsAdmin()`, `AdminOnly` l'accepterait sur les 8 routes de `workspace`, `teamFor` honorerait `?team=B`, et `POST /tokens?team=B` émettrait un token de projet chez le voisin, secret en clair. Le correctif fait 3 lignes et **précède**, il ne suit pas. |
| 6 | **Deux routes, deux écrans.** L'état de la team (la dette), et un détail polymorphe qui rend une tâche **ou** une issue. | Le « backlog d'un repo » est `flowlio task list` en couleur : il répond à « comment va CORE ? », question que l'humain n'a pas — il ne gère pas les backlogs, les agents le font. Il devient un **filtre** sur l'écran 1, pas un écran. Le détail polymorphe tombe gratuitement de la décision M3 #30 (`get(ref)` résout tâche ou issue). |
| 7 | **L'écran 1 est une FILE DE DETTE, pas un tableau de bord.** | Un état se contemple, une dette s'épuise. Le test qui départage : sur une team saine, **une file de dette est vide**, et une file vide est la meilleure information du produit (« rien ne t'attend, retourne travailler »). C'est aussi ce qui rend le produit falsifiable : un tableau de bord ne peut pas être faux, il peut seulement être ignoré. |
| 8 | **Quatre types de dette, tous dérivés de données déjà en base, zéro nouvelle table.** `answer`, `collect`, `resume`, `ask`. | Détail § Le modèle de dette. `ask` (bloqué sans avoir rien demandé) est le seul cul-de-sac que ni MCP, ni la CLI, ni le terminal de l'agent ne rendent visible : c'est lui qui justifie le jalon. |
| 9 | **Tri par ancienneté croissante (`updated_at ASC`), jamais par repo.** Inversion délibérée de `ListIssues`. | Un agent veut ce qui est frais, un superviseur veut ce qui pourrit. Trié par âge, **la première ligne est par construction la pire chose du système** : lire une seule ligne suffit à agir juste. Le regroupement par repo oblige à scanner trois blocs pour trouver la plus vieille. |
| 10 | **`tokens.last_used_at`, agrégé par projet sur les tokens non révoqués, devient le pouls d'un repo.** | Déjà écrit à chaque requête authentifiée (au plus une fois par minute, `authenticate.go:75-80`), lu par personne. C'est ce qui transforme « CORE-41 est ouverte depuis 3 j » (une ligne de tableau) en « cette question n'a pas de lecteur » (une conclusion sur laquelle on agit). |
| 11 | **LECTURE SEULE. Aucun chemin d'écriture, jamais, depuis cette surface.** | Le blocage n'est pas budgétaire, il est physique. `issue_messages.author_project_id` est `NOT NULL REFERENCES projects(id)`, `events.actor_project_id` est `NOT NULL` : le schéma n'a aucun auteur qui ne soit pas un projet. Et `AnswerIssue` **calcule** l'état depuis qui parle (`WHEN i.project_id = @project_id THEN 'answered' ELSE 'open'`) : un superviseur n'a pas de valeur correcte pour `@project_id`. Écrire imposerait soit un mensonge dans `author_project_id`, soit une colonne nullable qui rend le `CASE` de transition non total — c'est-à-dire rouvrir le trou que la décision M3 #10 a fermé. C'est un jalon, pas un champ. |
| 12 | **Aucune exposition MCP de cette surface. Aucun outil, jamais.** | Un agent qui lit l'état de sa team détruit la promesse d'isolation du produit, en lecture, silencieusement, sans qu'aucun test de tenancy ne tombe. Le refus de `runMCP` de démarrer sur un token non scopé projet (`mcp.go:117-120`) devient un garde-fou de sécurité, à commenter comme tel. |
| 13 | **Aucun paramètre de requête hors `?team=`.** Pas de `?limit=`, pas de `?status=`, pas de `?since=`. Aucun UUID en entrée ni en sortie. | Les seules entrées de tout le module sont `{project}` (`^[A-Z][A-Z0-9]{1,9}$`) et `{number}` (entier), résolus **dans** la query. Un paramètre qu'on n'accepte pas est un paramètre qu'on n'a pas à valider : la classe « param de route non validé » est supprimée par construction, pas par vigilance. Les bornes sont des constantes de service. |
| 14 | **404 uniforme sur tout ce qui n'est pas résoluble dans la team.** Slug inconnu, clé de projet d'une autre team, ref inexistante : même corps. `400` uniquement sur `?team=` absent. | Décision M3 #27, appliquée telle quelle. `400` sur team absente reproduit `workspace/handler/handler.go:121-123` : aucune nouvelle sémantique d'erreur. |
| 15 | **`sql/queries/overview.sql` est le SEUL fichier du dépôt à scoper par `team_id` seul, et il porte sa règle inverse en tête.** | L'ensemble des requêtes capables de traverser les projets devient énumérable en un `cat`. Contrôle mécanique : `scripts/check-overview-scope.sh` dans `make lint`. |
| 16 | **Amender l'en-tête de `sql/queries/tasks.sql`.** « Il n'existe aucune query de tâche sans scope » devient faux. | Un commentaire de sécurité faux est plus coûteux qu'un commentaire absent : il fait passer la relecture d'une vérification à un acte de confiance. Nouveau texte : « aucune query de CE fichier sans `project_id` ; la lecture team-scopée vit dans `overview.sql`, sous sa propre règle ». |
| 17 | **Règle de typage sqlc, apprise en exécutant sqlc 1.30 et pas en la déduisant : un cast `::timestamptz` déclare la colonne `NOT NULL`.** Ne l'écrire que sur une expression qui ne peut pas être NULL. | `(min(i.updated_at) FILTER (…))::timestamptz` produit `OldestWait time.Time` : le `Scan` échoue dès qu'un projet n'a **pas** d'issue entrante ouverte, c'est-à-dire sur une team saine. La même expression sans cast produit `interface{}`, interdit par `code-conventions.md` et par la décision M3 #3. Toute agrégation sur un ensemble possiblement vide sort dans sa **propre query `GROUP BY`** — cf. `OverviewLastSeen`. |
| 18 | **Sous-requêtes scalaires corrélées pour les compteurs, pas de `CROSS JOIN LATERAL`, et jamais de `JOIN`.** | Les deux formes préservent la propriété essentielle (un repo sans rien en vol **reste une ligne**) ; un `JOIN issues` le ferait disparaître de l'écran du superviseur, ce qui est la pire panne possible ici parce qu'elle est silencieuse. Mais les index existants sont préfixés par la colonne projet (`issues_incoming_idx (project_id, team_id, state, updated_at DESC)`) : une sous-requête corrélée sur `p.id` les utilise directement, le LATERAL avec son `OR` dépend d'un BitmapOr que rien ne garantit. |
| 19 | **Rafraîchissement : polling fixe à 10 s, horodatage de fraîcheur affiché en permanence, touche `r` pour forcer. Pas de SSE, pas d'ETag, pas de GET conditionnel.** | « Rien + touche recharger » détruit la promesse des trois secondes : chaque lecture s'accompagnerait de « est-ce à jour ? », qui coûte plus cher que le rafraîchissement. 5 s est du bruit pour un tour d'agent qui dure 10 s à 2 min. Et le détecteur de changement « évident » — `max(events.id)` — est un piège : `events` n'enregistre **que** des issues (décision M3 #6), donc un changement de statut de tâche ne le ferait pas bouger et l'écran afficherait un état périmé **en croyant être à jour**. |
| 20 | **`internal/pkg/termtext` est un prérequis de la PREMIÈRE ligne de sortie du jalon, pas du TUI.** | L'incrément 1 est en `fmt.Fprintf("%-12s %s\n", …)`, patron déjà en place (`task.go:95`). Il imprime des titres d'issues venant de la base sans le moindre filtre. Et `left(m.body_md, 500)` (`inbox.sql:31`) tronque à 500 **caractères**, ce qui peut trancher une séquence CSI en deux avant même qu'un renderer existe. |
| 21 | **`termtext` est une LISTE BLANCHE, pas une liste noire.** Un rune est conservé si et seulement si `unicode.IsGraphic(r)`. | `IsGraphic` couvre L, M, N, P, S, Zs — il exclut donc d'un coup C0, C1, DEL, les `Cf` (contrôles bidi Trojan Source, ZWJ) et les `Co`. Une liste noire écrite à la main sur un espace hostile (CSI, OSC 52 presse-papier, DSR/DA qui font écrire le terminal sur stdin, DCS, `\r` nu sans ESC, C1 8 bits) finit toujours par avoir un trou. Ordre non négociable : **neutraliser, PUIS tronquer, PUIS styler.** |
| 22 | **Largeur d'affichage mesurée avec `golang.org/x/text/width`, déjà présent en dépendance indirecte.** | `fmt` compte des runes : une idéographique occupe 2 cellules, tout alignement en colonnes est cassable à volonté par un titre d'issue. Promouvoir une indirecte déjà compilée n'est pas « ajouter une dépendance ». |
| 23 | **Incrément 1 = `flowlio watch` + `flowlio show <ref>`, en `fmt.Fprint`, ZÉRO dépendance.** C'est déjà la fonctionnalité demandée par FLWL-20. | Une commande, l'état des N repos. La plainte de la tâche est traitée en entier sans bubbletea. Ça déplace la décision Charm d'un cran : si quelqu'un la conteste, l'incrément 1 est déjà livré et résout déjà le problème. |
| 24 | **Charm (`bubbletea` + `lipgloss`) est acheté APRÈS deux semaines d'usage réel de l'incrément 1, et seulement si les trois critères d'échec passent.** `bubbles` est refusé définitivement. | Le piège nommé dans la tâche — « si on ne comprend pas en trois secondes, ce sont les écrans qui sont faux » — se paie alors au prix de 60 lignes de CLI au lieu du prix de deux dépendances et d'un moteur de rendu. `bubbles` traîne `atotto/clipboard`, qui `exec` `pbcopy`/`xclip`/`wl-copy` depuis un process qui a lu `~/.config/flowlio/credentials.json`, plus 8 autres propriétaires, pour un curseur sur un slice (15 lignes) et une fenêtre de défilement (20 lignes). |
| 25 | **Si Charm est acheté : jamais `lipgloss.AdaptiveColor`, jamais `HasDarkBackground()`. Index ANSI uniquement (`lipgloss.Color("3")`).** | `HasDarkBackground()` appelle `termenv.termStatusReport(11)`, qui bascule le termios en noecho/non-canonique, écrit une séquence OSC **sur le tty** et **lit stdin**. `cmd/flowlio` porte aussi `flowlio mcp` : ça injecterait des octets dans le flux JSON-RPC et mangerait une requête. La règle est greppable, donc vérifiable par script — et elle protège le chemin MCP autant que le TUI. |
| 26 | **v1 = une team à la fois. La team est un argument, pas une dimension de l'écran.** | FLWL-20 dit « plusieurs repos », pas « plusieurs teams ». Le jour où quelqu'un a deux teams, N écrans est exactement l'architecture que la tâche refuse. À rouvrir alors, pas maintenant. |

---

## Prérequis bloquant — le correctif `teamFor`

> **Rien de ce jalon ne se livre avant ce correctif.** Ce n'est pas un nettoyage, c'est le seul
> endroit du dépôt où une forme de token légale au schéma produit une escalade de privilège
> complète.

Chaîne vérifiée, trois sauts : un token `scope='admin', team_id=A` passe `IsAdmin()` → `AdminOnly`
l'accepte sur les 8 routes admin de `workspace` → `teamFor` honore `?team=B` →
`POST /api/workspace/tokens?team=B` émet un token de projet chez le voisin, secret en clair
(`create_token.go:38`) → lecture et écriture totales de la team B.

```go
// internal/feature/workspace/handler/handler.go
//
// Un token admin porte aujourd'hui TeamID vide : le slug DÉSIGNE la team. Mais
// tokens_scope_shape autorise déjà `scope='admin' AND team_id IS NOT NULL` — forme légale en
// base qu'aucun code n'émet. Le jour où elle est émise, ce garde l'ENFERME dans sa team : le
// slug ne peut alors que la confirmer, jamais la remplacer. Écrit maintenant parce qu'il coûte
// trois lignes maintenant et une escalade plus tard.
//
// 404 et non 403 : un code distinct dirait à l'appelant que la team existe.
func (h *Handler) teamFor(ctx context.Context, p auth.Principal, slug string) (uuid.UUID, error) {
	if !p.IsAdmin() {
		return p.TeamID, nil
	}
	if slug == "" {
		return uuid.Nil, errors.Join(service.ErrInvalidInput, errors.New("team manquante"))
	}
	team, err := h.svc.TeamBySlug(ctx, slug)
	if err != nil {
		return uuid.Nil, err
	}
	if p.TeamID != uuid.Nil && p.TeamID != team.ID {
		return uuid.Nil, service.ErrNotFound
	}
	return team.ID, nil
}
```

Le même garde est recopié dans `overview/handler/handler.go`. Deux copies, deux modules : c'est la
doctrine déjà appliquée trois fois à `requireProjectScope` (décision M3 #25).

> **Honnêteté sur la portée de ce garde :** tant qu'aucun code n'émet cette forme de token, le
> chemin est mort et son test (mutation 12 ci-dessous) injecte la ligne en SQL brut. Il teste donc
> la lecture, pas l'émission. Le jour où l'émission arrive, elle doit venir avec son propre test.

---

## Le modèle de dette

Quatre types, tous dérivables de données déjà en base, aucune nouvelle table.

| `kind` | Condition | Débiteur | Ce que ça veut dire |
| ------ | --------- | -------- | ------------------- |
| `answer` | `issues.state = 'open'` | `project_id` (destinataire) | un agent frère est bloqué sur lui |
| `collect` | `issues.state = 'answered'` | `author_project_id` | il a sa réponse et ne l'a pas consommée |
| `resume` | `tasks.status = 'in_progress'` et `last_move < now - 24 h` | son projet | session vraisemblablement morte en cours de tâche |
| `ask` | `tasks.status = 'blocked'` **et** aucune issue `open` dont ce projet est `author_project_id` | son projet | s'est déclaré coincé sans rien demander à personne |

Trois précisions qui ne sont pas des détails :

1. **`last_move` n'est pas `tasks.updated_at`.** `CreateTaskNote` (`sql/queries/tasks.sql:60-68`)
   n'écrit pas `updated_at`. Sans le `max()` sur les notes, un agent qui documente activement sa
   progression serait signalé « session morte ». `last_move = greatest(updated_at, max(note.created_at))`.
2. **Le seuil de `resume` n'est pas dans le SQL.** Le service calcule `now - 24 h` et le passe en
   `@stale_before`. L'horloge appartient au service, le scope appartient à la query, et le test
   d'intégration devient déterministe.
3. **Une tâche `blocked` avec une question ouverte n'est PAS une dette de type `ask`.** Elle est
   déjà représentée par la ligne `answer` du repo destinataire. La classification est totale :

| `status` | `has_open_question` | `stale` | `kind` |
| -------- | ------------------- | ------- | ------ |
| `blocked` | `false` | — | `ask` |
| `blocked` | `true` | — | *omise* — la question ouverte est déjà une ligne |
| `in_progress` | — | `true` | `resume` |
| `in_progress` | — | `false` | *omise* — ça avance |

---

## Surface HTTP

Module monté sous `/api/overview/` (`Key() = "overview"`). Middleware lié **une seule fois** dans
`module.go` : `admin := m.auth.AdminOnly`. Il n'existe aucune route de ce module atteignable par un
token de portée projet — pas de gate mixte.

| Verbe + chemin | Paramètre | 200 | Erreurs |
| -------------- | --------- | --- | ------- |
| `GET /api/overview/{$}` | `?team=<slug>` requis | `TeamState` | 401 sans token · **403 token projet** · 400 `team` absente · 404 slug inconnu |
| `GET /api/overview/refs/{project}/{number}` | `?team=<slug>` requis | `RefDetail` (`kind: issue\|task`) | idem + 404 ref introuvable dans la team |

Bornes, **constantes de service, jamais des paramètres** : 50 dettes, 200 messages de fil, 50 notes
de tâche. `projects[]` n'est **jamais** tronqué, quelle que soit la borne : un repo qui disparaît de
l'écran du superviseur est le seul défaut irrécupérable de cette surface — il ne peut pas chercher
ce qu'il ne voit pas.

### `GET /api/overview/?team=omiros`

```json
{
  "generated_at": "2026-08-02T14:32:04Z",
  "projects": [
    { "key": "CORE",  "owes_answer": 2, "awaiting_answer": 0, "answered_unread": 0,
      "tasks_running": 3, "tasks_blocked": 1, "last_agent_seen_at": "2026-07-30T08:02:00Z" },
    { "key": "WEB",   "owes_answer": 0, "awaiting_answer": 2, "answered_unread": 1,
      "tasks_running": 1, "tasks_blocked": 0, "last_agent_seen_at": "2026-08-02T14:30:00Z" },
    { "key": "INFRA", "owes_answer": 1, "awaiting_answer": 0, "answered_unread": 0,
      "tasks_running": 0, "tasks_blocked": 0, "last_agent_seen_at": "2026-08-02T14:14:00Z" }
  ],
  "debts": [
    { "kind": "answer", "ref": "CORE-41", "debtor": "CORE", "peer": "WEB",
      "title": "Le contrat /v1/sessions a-t-il changé ?", "since": "2026-07-30T09:14:00Z" },
    { "kind": "resume", "ref": "WEB-19", "debtor": "WEB",
      "title": "Migrer le store de session vers /v1", "since": "2026-07-29T11:40:00Z" }
  ],
  "truncated": 0
}
```

Ce qui est gardé, ce qui est coupé, et pourquoi :

| Champ | Sort | Raison |
| ----- | ---- | ------ |
| `projects[].key` | gardé | la seule identité qu'un humain utilise pour ses repos |
| `projects[].name` | **coupé** | il connaît ses repos par leur clé ; le nom est dans `GET /workspace/projects` |
| `last_agent_seen_at` | gardé | le pouls. Un **horodatage**, jamais une durée : une durée périme dans le client |
| `debtor_last_seen_at` sur chaque dette | **coupé** | dérivable par jointure sur `debtor` → `projects[]`. Une donnée, une source |
| `debts[].ref` | gardé | `CORE-41` porte déjà le destinataire — pas de champ `to` |
| `debts[].peer` | gardé, `omitempty` | vide pour `resume` et `ask`, qui n'ont pas de correspondant |
| `debts[].since` | gardé | l'âge. Nommé `since` et non `updated_at` parce qu'il agrège deux colonnes différentes (`issues.updated_at`, `last_move`) |
| `debts[].excerpt` / `truncated` par ligne | **coupés** | 50 extraits de 500 caractères ne se lisent pas en trois secondes. L'extrait est dans le détail |
| `debts[].new` | **coupé** | le curseur appartient à un token d'agent (décision M3 #2) ; un « déjà vu » humain est une nouvelle table, donc une écriture sur une surface déclarée en lecture seule |
| `health`, `is_stale`, durée en secondes, couleur, seuil | **coupés** | « trois jours = rouge » est une politique de rendu. La coder dans l'API la rend fausse pour la team suivante |
| `generated_at` | gardé | le client calcule tous les âges depuis **cet** horodatage, pas depuis sa propre horloge : une dérive d'horloge ne produit jamais « il y a −3 s » |
| `truncated` | gardé | `total - len(debts)`. Sans lui, une liste tronquée ment, et l'écran est faux d'une manière silencieuse et crédible |
| écho de `team` | **coupé** | l'appelant l'a fourni ; l'y renvoyer invite à faire de cette réponse la source des métadonnées de team |

### `GET /api/overview/refs/CORE/41?team=omiros`

```json
{
  "kind": "issue", "ref": "CORE-41", "from": "WEB", "state": "open",
  "title": "Le contrat /v1/sessions a-t-il changé ?",
  "created_at": "2026-07-28T08:00:00Z", "updated_at": "2026-07-30T09:14:00Z",
  "messages": [ { "from": "WEB", "created_at": "2026-07-30T09:14:00Z", "body": "..." } ],
  "messages_total": 1
}
```

```json
{
  "kind": "task", "ref": "CORE-12", "status": "blocked", "priority": "high",
  "title": "Refonte du calcul d'idempotence", "body": "...",
  "created_at": "2026-07-29T10:00:00Z", "updated_at": "2026-08-01T09:11:00Z",
  "deadline": "2026-08-05T00:00:00Z",
  "notes": [ { "created_at": "2026-08-01T09:11:00Z", "body": "..." } ],
  "notes_total": 3
}
```

`kind` en premier champ, miroir exact de la décision M3 #30. `created_at` **et** `updated_at` sur
une issue : « ouverte depuis 5 jours, silence depuis 3 » sont deux informations différentes pour un
superviseur. `closed_at` coupé (`state` + `updated_at` le disent). `messages_total` / `notes_total`
émis seulement s'ils dépassent le nombre renvoyé.

---

## `sql/queries/overview.sql` — littéral

```sql
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
-- name: OverviewLastSeen :many
SELECT p.key, max(tk.last_used_at)::timestamptz AS last_seen
FROM tokens tk
JOIN projects p ON p.id = tk.project_id AND p.team_id = tk.team_id
WHERE tk.team_id      = @team_id
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
-- matérialisation du jeu complet : acceptable à l'échelle d'une team (cf. § Ce que ça coûte).
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
-- last_move corrige un piège de M2 : CreateTaskNote (sql/queries/tasks.sql:60-68) n'écrit PAS
-- tasks.updated_at. Sans le max() sur les notes, un agent qui documente activement sa progression
-- serait signalé « session morte ». Le coalesce sur t.updated_at (NOT NULL) rend l'expression
-- non-nullable, ce qui est la seule condition qui autorise le cast ::timestamptz.
--
-- CTE et non alias en WHERE : SQL n'autorise pas à filtrer sur un alias de SELECT, et répéter
-- l'expression greatest() deux fois serait la première divergence à apparaître.
--
-- @stale_before est calculé en Go (now - 24 h). L'horloge appartient au service, le scope à la
-- query : le test d'intégration devient déterministe et le seuil se règle sans migration.
--
-- has_open_question distingue « bloqué et il a demandé » de « bloqué et il n'a rien demandé ». Le
-- second est le seul cul-de-sac que ni MCP, ni la CLI, ni le terminal de l'agent ne rendent
-- visible : c'est lui qui justifie ce jalon.
-- name: OverviewTaskDebts :many
WITH candidate AS (
    SELECT t.id, t.team_id, t.project_id, t.number, t.status, t.priority, t.title, t.deadline,
           greatest(
               t.updated_at,
               coalesce((SELECT max(n.created_at) FROM task_notes n WHERE n.task_id = t.id),
                        t.updated_at)
           ) AS last_move
    FROM tasks t
    WHERE t.team_id     = @team_id
      AND t.archived_at IS NULL
      AND t.status IN ('in_progress', 'blocked')
)
SELECT c.number, c.status, c.priority, c.title, c.deadline,
       p.key AS project_key,
       c.last_move::timestamptz AS last_move,
       EXISTS (SELECT 1 FROM issues i
                WHERE i.team_id           = @team_id
                  AND i.author_project_id = c.project_id
                  AND i.state             = 'open') AS has_open_question,
       (count(*) OVER ())::bigint AS total
FROM candidate c
JOIN projects p ON p.id = c.project_id AND p.team_id = c.team_id
WHERE c.status = 'blocked' OR c.last_move < @stale_before
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
-- issue_messages_author_project_id_fkey → projects(id), vérifié dans schema.sql:503 — pas
-- composite. Rien au niveau du schéma n'empêche author_project_id de pointer un projet d'une
-- autre team. C'est la SEULE occurrence du dépôt où retirer cette clause est observable, donc la
-- seule qui se teste. Ailleurs, le contrôle réel est la FK composite, et c'est la FK qu'il faut
-- tester.
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
```

### Migration `000006` — non destructrice

```sql
-- 000006_overview_index.up.sql
--
-- OverviewIssueDebts filtre team_id SANS prédicat de projet. issues_incoming_idx et
-- issues_outgoing_idx sont préfixés par la colonne projet (project_id, team_id, state,
-- updated_at DESC) : aucun des deux ne sert, la query ferait un seq scan d'issues.
-- Les compteurs de OverviewProjects, eux, restent couverts : leurs sous-requêtes sont corrélées
-- sur p.id, donc préfixées par le projet.
CREATE INDEX issues_team_state_idx ON issues (team_id, state, updated_at);

-- OverviewTaskDebts filtre team_id + status sans projet. tasks_project_active_idx est préfixé par
-- project_id : inutilisable ici. L'index partiel suit le prédicat de la CTE.
CREATE INDEX tasks_team_status_idx ON tasks (team_id, status) WHERE archived_at IS NULL;
```

```sql
-- 000006_overview_index.down.sql
DROP INDEX IF EXISTS tasks_team_status_idx;
DROP INDEX IF EXISTS issues_team_state_idx;
```

Deux `CREATE INDEX`, aucune perte de données : Claude peut appliquer `make up-dev` sans accord
préalable. `make schema` ensuite, `sql/schema/` reste la source de vérité.

### `scripts/check-overview-scope.sh` — dans `make lint`

Trois vérifications, une douzaine de lignes d'awk :

1. Chaque bloc `-- name:` de `sql/queries/overview.sql` contient `team_id = @team_id`. Exception
   unique et nommée : `OverviewTeamBySlug`.
2. Le fichier ne contient aucun `INSERT`, `UPDATE`, `DELETE`.
3. Aucune occurrence de `Overview` dans un fichier `.go` hors de `internal/feature/overview/`.

La troisième est la seule protection contre le vrai risque permanent : `internal/database` est
importable de partout, donc un contributeur qui trouve `OverviewIssueDebts` pratique et l'appelle
depuis `issue/store` obtient une fuite complète, et `check-cross-feature-imports.sh` ne voit rien.
Je ne peux pas rendre ce risque nul, seulement bruyant.

---

## Placement dans l'hexagone

```
internal/feature/overview/
├── module.go                  admin := m.auth.AdminOnly, lié UNE fois, 2 routes
├── handler/
│   ├── handler.go             Handler, New, writeJSON, writeError, principal, teamFor
│   ├── team_state.go          GET /{$}
│   └── ref_detail.go          GET /refs/{project}/{number}
├── service/
│   ├── service.go             CONTRAT : interface, struct, New, types de vue, erreurs
│   ├── team_state.go          assemble projets + pouls + dettes, classe les kinds
│   ├── ref_detail.go          issue d'abord, tâche ensuite, kind en premier champ
│   └── validate.go            bornes constantes + garde uuid.Nil
└── store/
    ├── store.go               CONTRAT : interface + struct + New. Pas de tx.go : lecture pure
    ├── team.go                TeamBySlug
    ├── projects.go            Projects, LastSeen
    ├── debts.go               IssueDebts, TaskDebts
    ├── thread.go              IssueByRef, IssueMessages
    ├── task.go                TaskByRef, TaskNotes
    └── store_integration_test.go
```

Wiring : une ligne dans `buildModules()` (`cmd/api/main.go`) → `overview.NewModule(base)`, avec
`store.New(cfg.DB)`. Pas de `RawDB` : aucune transaction.

**L'invariant de signature est le vrai garde-fou.** Le slug ne descend jamais sous le handler.
Toutes les méthodes du store prennent `teamID uuid.UUID` en premier paramètre, non-nullable :

```go
// internal/feature/overview/store/store.go — CONTRAT UNIQUEMENT
//
// TeamBySlug est la SEULE méthode sans teamID : c'est celle qui le produit. Toutes les autres en
// exigent un, donc aucun appelant ne peut en oublier un. Transposition littérale du contrat de
// task/store/store.go, où chaque méthode prend teamID ET projectID.
type Store interface {
	TeamBySlug(ctx context.Context, slug string) (Team, error)

	Projects(ctx context.Context, teamID uuid.UUID) ([]ProjectCounters, error)
	LastSeen(ctx context.Context, teamID uuid.UUID) ([]ProjectPulse, error)
	IssueDebts(ctx context.Context, teamID uuid.UUID, limit int32) ([]IssueDebt, int64, error)
	TaskDebts(ctx context.Context, teamID uuid.UUID, staleBefore time.Time, limit int32) ([]TaskDebt, int64, error)

	IssueByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Issue, error)
	IssueMessages(ctx context.Context, teamID, issueID uuid.UUID, limit int32) ([]Message, int64, error)
	TaskByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Task, error)
	TaskNotes(ctx context.Context, teamID, taskID uuid.UUID, limit int32) ([]Note, int64, error)
}
```

Défense en profondeur côté service, à l'image de `task/handler/handler.go:112-120` :
`TeamState(ctx, uuid.Nil)` renvoie `ErrInvalidInput` **sans toucher le store**.

> **Ce n'est pas un import inter-feature.** La décision M3 #26 est déjà écrite et déjà appliquée :
> une feature peut LIRE une table d'un autre domaine par une query scopée dédiée ; elle n'ÉCRIT
> jamais hors de son domaine. `overview` lit `teams`, `projects`, `tokens`, `issues`,
> `issue_messages`, `tasks`, `task_notes`, et n'écrit rien du tout — le cas le plus propre possible.
> Zéro import Go, `check-cross-feature-imports.sh` passe. À inscrire dans `ARCHITECTURE.md`.

---

## Écrans

Deux écrans. Les maquettes sont mesurées à **≤ 78 colonnes**, en caractères de largeur 1.

> **Aucun glyphe classé East Asian Ambiguous.** Pas de `⚠`, `→`, `─`, `·`, `…`, `•` : ils se
> rendent sur une ou deux cellules selon le terminal, et tout le design repose sur l'alignement en
> colonnes. Un caractère qui bouge de largeur casse l'écran chez l'utilisateur, jamais chez le
> développeur. Les accents latins (`é`, `à`, `ê`) sont sûrs : ils sont Narrow, sans ambiguïté.
> Marqueur de troncature : `...` en ASCII.
>
> **L'écran doit être intégralement lisible avec `NO_COLOR=1`.** Les maquettes ci-dessous le
> prouvent : elles n'ont pas une seule couleur. La couleur ne fait que renforcer l'ordre de tri,
> les deux blocs et les lignes `(!)`, qui portent toute l'information.

### Écran 1 — LA DETTE

```
 flowlio  team omiros                     données à 14:32:04  -  [r] recharger
 agents   CORE vu il y a 3 j (!)  WEB vu il y a 2 min   INFRA vu il y a 18 min
==============================================================================
 EN ATTENTE D'UN AUTRE REPO                                       4 en dette
------------------------------------------------------------------------------
  3 j  CORE doit répondre à WEB                                       CORE-41
       Le contrat /v1/sessions a-t-il changé ?
   (!) dernier appel d'un agent CORE il y a 3 j - personne ne lit la question

 19 h  INFRA doit répondre à CORE                                     INFRA-7
       DATABASE_URL manquant sur staging ?

  6 h  CORE doit répondre à WEB                                       CORE-42
       Pagination : offset ou curseur sur /v1/events ?

  4 h  CORE a une réponse non lue de WEB                               WEB-23
       Faut-il garder le champ legacy_id dans le payload ?
------------------------------------------------------------------------------
 À L'ARRÊT                                                         2 en dette
------------------------------------------------------------------------------
  4 j  WEB    in_progress                                              WEB-19
       Migrer le store de session vers /v1
   (!) aucune note depuis 4 j - session vraisemblablement interrompue

  2 j  CORE   blocked                                                 CORE-12
       Refonte du calcul d'idempotence
   (!) bloquée et aucune question ouverte - n'attend rien d'un repo frère
------------------------------------------------------------------------------
 [1-9] filtrer un repo   [r] recharger   [q] quitter
```

**Hiérarchie visuelle — dans quel ordre l'œil tombe, et pourquoi c'est le bon ordre :**

1. **Les deux titres de section en capitales, et leur compteur à droite.** Deux chiffres, `4` et
   `2`, répondent à « c'est grave ? » avant qu'un seul mot ait été lu. Ce sont les **seuls**
   chiffres de l'écran ; toute autre métrique volerait cette place.
2. **La colonne d'âge, cadrée à gauche, décroissante.** Une bande verticale unique et régulière,
   donc lisible en périphérie. La première ligne est la pire chose du système, garanti par le tri.
3. **La ligne `agents`.** L'humain sait *avant de lire une ligne de dette* quel repo est mort.
4. **Les lignes `(!)`.** Les seules phrases de l'écran, et ce ne sont pas des données : ce sont des
   **conclusions calculées**. Si on les retire, il ne reste qu'un tableau, et un tableau ne mérite
   pas deux dépendances. Chacune est écrite en deux temps — observation vérifiable, puis conséquence
   interprétée — parce que la ligne est une inférence et que l'humain doit pouvoir la contredire.
5. **Les refs, alignées à droite.** Nécessaires pour agir, inutiles pour décider.

**L'état sain — l'écran le plus important du produit :**

```
 flowlio  team omiros                     données à 14:32:04  -  [r] recharger
 agents   CORE vu il y a 3 j (!)  WEB vu il y a 2 min   INFRA vu il y a 18 min
==============================================================================


   Rien en dette.

   4 tâches en cours, 2 issues en vol, aucune immobile depuis plus d'1 h.


==============================================================================
 [1-9] filtrer un repo   [r] recharger   [q] quitter
```

La seconde ligne existe pour prouver que l'outil a bien regardé — un écran vide sans preuve se lit
comme une panne. Elle compte de l'**en vol**, jamais du terminé.

### Écran 2 — LE DÉTAIL, polymorphe

**Variante issue :**

```
 CORE-41   issue   ouverte par WEB le 30 juil. à 09:14   sans réponse : 3 j
==============================================================================
 Le contrat /v1/sessions a-t-il changé ?
------------------------------------------------------------------------------
 WEB                                                    30 juil. 09:14  (3 j)
   Depuis hier POST /v1/sessions renvoie 422 sur un corps qui passait avant.
   Le champ `device` est-il devenu obligatoire ? Nos logs montrent `device_id`
   alors que le client envoie `device`. Si le contrat a changé je migre WEB,
   sinon je cherche la régression chez nous.
------------------------------------------------------------------------------
 (!) dernier appel d'un agent CORE il y a 3 j
     Cette question n'a pas de lecteur. Ouvrir une session dans le repo CORE
     et lui donner la ref : CORE-41
==============================================================================
 LECTURE SEULE - pour répondre, c'est l'agent CORE qui écrit, pas vous
 [entrée] retour   [y] copier la ref   [r] recharger   [q] quitter
```

**Variante tâche, même touche, même écran :**

```
 CORE-12   tâche   projet CORE   blocked   priorité high   immobile : 2 j
==============================================================================
 Refonte du calcul d'idempotence
------------------------------------------------------------------------------
 Le compteur next_number est incrémenté hors transaction dans le chemin de
 rejeu. Référence : docs/DECISION-idempotence.md.
------------------------------------------------------------------------------
 notes                                                                      3
   31 juil. 16:02   Reproduit : deux create_task concurrents, même numéro.
   31 juil. 17:40   Le correctif suppose de trancher entre garder le compteur
                    transactionnel ou passer par une SEQUENCE. Je bloque là.
   1er août 09:11   Toujours bloqué, j'attends un arbitrage.
------------------------------------------------------------------------------
 (!) bloquée depuis 2 j, aucune issue ouverte par CORE vers un repo frère
     L'agent attend un arbitrage qu'il n'a demandé à personne.
==============================================================================
 [entrée] retour   [y] copier la ref   [r] recharger   [q] quitter
```

C'est ici que le produit rembourse : l'humain lit « j'attends un arbitrage » et découvre que c'est
**lui** qu'on attend. Aucun outil actuel ne produit ce moment.

Le pied de page enseigne le chemin d'écriture au lieu de le simuler. `[y]` copie la ref via OSC 52
— séquence que **nous** émettons, pas du texte serveur — et n'existe qu'en phase 2. L'incrément 1
rend le même contenu sans les lignes de touches.

---

## `internal/pkg/termtext` — l'évier unique

```go
// Line neutralise un champ d'une ligne (titre, clé, nom d'auteur) et le borne en CELLULES
// d'affichage. Ordre non négociable : neutraliser PUIS tronquer PUIS styler.
func Line(s string, cells int) string

// Block neutralise un corps multi-lignes : conserve '\n', convertit '\t' en deux espaces,
// supprime tout le reste.
func Block(s string) string

// Cells mesure la largeur d'affichage, pas le nombre de runes.
func Cells(s string) int
```

**Liste blanche : un rune est conservé si et seulement si `unicode.IsGraphic(r)`.** `IsGraphic`
couvre L, M, N, P, S, Zs. Il exclut donc d'un seul prédicat tout ce qui suit :

| Famille supprimée | Exemple | Pourquoi c'est une attaque, pas un artefact |
| ----------------- | ------- | ------------------------------------------- |
| CSI | `\x1b[2J\x1b[H` | Repeindre l'écran, c'est **forger l'état de la team**. Un titre d'issue qui réécrit la ligne du dessus inverse la proposition de valeur du produit |
| OSC 52 | `\x1b]52;c;<b64>\x07` | **Écrit le presse-papier système.** Supporté par iTerm2/kitty/wezterm/foot et tmux avec `set-clipboard on` — l'environnement exact d'une console de supervision |
| OSC 8 / OSC 0 | `\x1b]8;;http://…\x07` | Hyperlien cliquable, titre de fenêtre, chaîne titre → réinjection en stdin |
| DSR / DA / DCS | `\x1b[6n`, `\x1bP+q…` | **Le terminal ÉCRIT une réponse sur stdin.** Un TUI lit stdin comme des touches : du texte rendu devient des frappes synthétiques. Le seul item de la liste qui va jusqu'à l'exécution |
| C0 nu | `tout va bien\rALERTE` | **Aucun ESC.** Tue tout filtre qui ne cherche que `0x1b` |
| C1 | `0x80`–`0x9f` | Introducteurs CSI/OSC mono-octet en mode 8 bits |
| `\n` dans un titre | `ligne1\nligne2` | Insère une fausse rangée dans le tableau |
| Contrôles bidi (`Cf`) | `‮`, `⁦`–`⁩` | Trojan Source : le titre affiché ne dit pas ce que le titre contient |

**Ce que la liste blanche ne couvre pas, et qu'il faut écrire plutôt que le taire :** les
homoglyphes purs (`СORE` avec un `С` cyrillique) sont graphiques, de largeur normale, et visuellement
identiques. La seule parade serait une liste blanche de scripts Unicode, qu'on refuse d'imposer à
des titres d'issues écrits en français par des agents. Risque connu, non couvert.

**Le contrôle réel n'est pas le filtre, c'est le test qui prouve qu'aucune vue ne l'oublie** — voir
mutation 17.

> Durcissement complémentaire, spécification et non défense : `validateTitle` refuse tout rune non
> `IsGraphic`. Son propre commentaire dit déjà « il doit tenir sur une ligne » ; on le rend
> exécutable. Les corps gardent `\n`. Ça ne protège pas le corpus déjà en base, ce qui est
> exactement pourquoi la propriété appartient à l'évier.

---

## Garanties de sécurité — chacune avec la mutation qui doit faire tomber son test

`U` = test unitaire, dans `make check`. `I` = test d'intégration, `t.Skip` sans
`FLOWLIO_TEST_DATABASE_URL`, lancé par **`make test-integration` uniquement**.

| # | Garantie | Test | Mutation qui doit le faire passer au ROUGE |
| - | -------- | ---- | ------------------------------------------ |
| 1 | Un token de projet n'atteint aucune route d'overview | `U` `TestOverviewRefusesProjectToken` — les 2 routes → 403 | dans `overview/module.go`, `admin := m.auth.AdminOnly` → `m.auth.Middleware` |
| 2 | Matrice de portée complète | `U` `TestScopeRouteMatrix` — 3 principaux × 4 préfixes : `project` → task/issue/inbox 200, overview **403** ; `admin` → workspace 200, overview 200, task/issue/inbox **403** ; absent → 401 partout | `requireProjectScope` accepte `\|\| p.IsAdmin()` → case admin×task rouge. `AdminOnly` accepte un scope projet → case project×overview rouge |
| 3 | La team ne vient jamais du principal pour un admin, et le slug ne peut pas la remplacer | `U` `TestOverviewTeamComesFromResolvedSlug` — service factice capturant l'entrée | faire lire un `?team_id=` UUID au handler |
| 4 | Un admin porteur d'une team est enfermé dedans | `I` `TestTeamScopedAdminIsLockedToItsTeam` — insérer en SQL brut `('admin', A, NULL, …)`, appeler `?team=<slug B>` → 404 | supprimer `if p.TeamID != uuid.Nil && p.TeamID != team.ID` de `teamFor`, dans `workspace` **et** dans `overview` |
| 5 | Aucune issue d'une autre team dans la file | `I` `TestOverviewNeverCrossesTeams` — assertion sur l'**ensemble exact** des refs attendues | retirer `i.team_id = @team_id` de `OverviewIssueDebts`. ⚠ l'assertion doit être un ensemble exact : « ne contient rien de B » passerait aussi sur un résultat vide |
| 6 | Contrôle positif de 5 | `I` `TestOverviewSeesEveryDebtOfItsTeam` | remplacer `@team_id` par `uuid.Nil` dans le service — sans ce test, une query qui ne renvoie jamais rien passe le test d'isolation |
| 7 | Pas de fuite par la jointure projet | `I` `TestOverviewJoinIsTeamScoped` — deux teams avec la MÊME clé `CORE` | retirer `AND p.team_id = i.team_id` d'un des deux joins. *Faible* : la FK composite la rend presque inobservable — le vrai contrôle est la FK, testée par 8 |
| 8 | La FK composite est le contrôle réel sur `issues` | `I` `TestIssueCannotReferenceForeignProject` — insertion directe → violation | supprimer `issues_project_fk` de la migration |
| 9 | Les compteurs ignorent les autres teams | `I` `TestOverviewCountersAreTeamScoped` — 10 issues créées dans B, compteurs de A inchangés | retirer `i.team_id = p.team_id` d'une sous-requête scalaire |
| 10 | Un repo sans rien en vol reste affiché | `I` `TestOverviewKeepsIdleProjects` — `DOCS`, zéro issue zéro tâche, compteurs à 0 | remplacer une sous-requête scalaire par `JOIN issues i ON i.project_id = p.id` |
| 11 | La liste des projets n'est jamais tronquée | `I` `TestOverviewNeverTruncatesProjects` — 40 projets, 100 dettes → 40 lignes `projects[]` | appliquer la borne à `OverviewProjects` |
| 12 | Le fil d'une autre team est introuvable | `I` `TestOverviewThreadCannotCrossTeams` | retirer `i.team_id = @team_id` d'`OverviewIssueByRef` |
| 13 | Non-vacuité de 12 : l'overview voit une issue dont personne n'est l'appelant | `I` `TestOverviewThreadIsVisibleToNeitherParticipant` — issue WEB→CORE lue via overview | ajouter `AND (i.project_id = @caller OR i.author_project_id = @caller)` à `OverviewIssueByRef` |
| 14 | Les messages ne fuient pas — **la clause load-bearing** | `I` `TestOverviewMessagesRejectForeignAuthor` — insérer un `issue_messages` dont `author_project_id` est un projet d'une autre team (possible : FK simple), la ligne ne doit pas remonter | retirer `AND ap.team_id = i.team_id` d'`OverviewIssueMessages`. C'est la **seule** clause de join du dépôt dont la mutation est observable |
| 15 | Le backlog d'une autre team est introuvable | `I` `TestOverviewTaskDebtsCannotCrossTeams` | retirer `AND p.team_id = c.team_id` du join d'`OverviewTaskDebts` |
| 16 | `truncated` est exact | `I` `TestOverviewTruncatedCountsWhatIsHidden` — 60 dettes, borne 50 → 50 lignes, `truncated: 10` | supprimer `count(*) OVER ()` et renvoyer 0. Sans ce test, l'écran est faux d'une manière silencieuse et crédible : c'est le défaut le plus probable du lot |
| 17 | **Aucune chaîne serveur brute n'atteint le renderer** | `U` `TestNoRawServerStringReachesTheRenderer` — chaque champ de chaque modèle vaut une chaîne hostile, passée dans la fonction de vue racine des deux écrans ; assertion : la sortie ne contient **aucun `\x1b`, aucun `\r`** | passer un seul champ brut dans une seule vue. **Le meilleur test du lot** : c'est lui, pas le filtre, qui rend la propriété vraie |
| 18 | Le filtre neutralise chaque famille | `U` `TestLineNeutralisesHostileText`, une ligne par famille du tableau `termtext` | ne filtrer que `0x1b` → ligne `\r` rouge · ignorer C1 → ligne C1 rouge · garder les bidi → ligne RLO rouge · mesurer en runes → lignes CJK/emoji rouges · **tronquer AVANT de filtrer** → ligne « CSI coupée » rouge |
| 19 | La commande de supervision ne retombe pas sur le mauvais token | `U` `TestWatchRefusesNonAdminToken` — `/whoami` renvoie `scope: project` → sortie en erreur, code 2 | supprimer la vérification de scope. Ferme le piège `FromCredentials`, qui exige `FLOWLIO_API_URL` **et** `FLOWLIO_TOKEN` ensemble et retombe sinon **silencieusement** sur le token admin du fichier |
| 20 | Aucune écriture par cette surface | `U` `TestOverviewExposesOnlyGET` — POST/PATCH/PUT/DELETE sur chaque route → 405 · `make lint` refuse tout `INSERT`/`UPDATE`/`DELETE` dans `overview.sql` | monter une route d'écriture |
| 21 | Aucun outil MCP ne touche `/api/overview` | `U` `TestMCPToolsNeverCallOverview` — parcours de la table d'outils, aucun chemin ne commence par `/api/overview` | ajouter un outil `team_overview` |

> **Mutation déclarée non tuable, et il faut le dire plutôt qu'écrire un test qui ment.** Retirer
> `AND p.team_id = i.team_id` d'un join vers `projects` sur `issues` ou `tasks` : la FK composite
> `(project_id, team_id) → projects(id, team_id)` rend la clause mathématiquement redondante, aucun
> jeu de données insérable ne la rend observable. Elle reste écrite — défense en profondeur si la
> résolution du projet change un jour — mais le contrôle réel est la FK, et c'est la FK qu'on teste
> (mutation 8). **L'exception est `issue_messages`, dont la FK est simple : là, la clause porte
> réellement, et la mutation 14 la couvre.**

---

## Le harnais de test n'existe pas — il faut le budgéter

`auth.contextKey` est privé au paquet (`middleware.go:25-27`) : **aucun test hors de
`package auth` ne peut injecter un `Principal` dans un contexte.** Et le seul test de handler
existant (`task/handler/handler_test.go`) est un test in-package de `writeJSON` — il n'exerce
aucune route.

Aucune des mutations 1, 2, 3, 19, 20, 21 n'est donc écrivable aujourd'hui. Il faut, dans sa propre
tâche :

- un faux `auth.Store` (l'interface est exportée, `auth/store.go:44-47`, deux méthodes) ;
- des tokens réellement frappés par `crypto.NewToken()` ;
- un `auth.New(fake)` et des requêtes portant un vrai `Authorization: Bearer` ;
- un `httptest.Server` monté sur le mux de l'engine.

Environ 60 lignes, réutilisables par tous les modules. Sans elles, le seul garde-fou contre la
décision #2 reste une intention.

> **`make check` ne prouve RIEN sur l'isolation.** `make test` = `go test ./...` **sans**
> `FLOWLIO_TEST_DATABASE_URL` : les `store_integration_test.go` font `t.Skip`. Une session future
> peut livrer `overview`, voir `make check` vert, et n'avoir exécuté aucun des tests de scope. La
> recette d'`overview` est **`make test-integration`**, pas `make check`, et la tâche du board doit
> l'écrire dans son « Fini quand ».

---

## Dépendance — tranché

| Phase | Livrable | Dépendance ajoutée | Vaut seule ? |
| ----- | -------- | ------------------ | ------------ |
| **1 — maintenant** | Module `overview` + `termtext` + `flowlio watch [--team] [--follow]` + `flowlio show <ref>`, rendu en `fmt.Fprint` | **aucune** (`x/text/width` est déjà en indirecte) | **Oui — c'est déjà la fonctionnalité demandée par FLWL-20.** Une commande, l'état des N repos |
| **2 — sous condition** | Les deux mêmes vues en TUI : navigation clavier, polling 10 s intégré, `[y]` | `bubbletea` + `lipgloss`, bornées à `cmd/flowlio/tui/`, CLAUDE.md § Stack mis à jour | Confort de lecture continue |

Réponse frontale d'abord : **quand le TUI interactif se fera, ce sera Charm.** Réécrire un moteur de
rendu ANSI, ce n'est pas « dessiner des cadres », c'est termios + `SIGWINCH` + parsing des
séquences clavier + restauration du terminal après panic. Plusieurs semaines pour des bugs que
Charm a déjà corrigés. Refusé.

Mais le périmètre de la phase 1 est intégralement réalisable en `fmt.Fprint`, y compris le
rafraîchissement (`\033[2J\033[H` + `time.Tick`, une quinzaine de lignes). Charm n'achète qu'une
chose : la navigation clavier entre la file et le détail. Ça vaut cher — **mais seulement une fois
qu'on sait que la file est juste.** Deux dépendances s'achètent avec une preuve d'usage, pas avec
une hypothèse. CLAUDE.md reste intact tant que la preuve n'existe pas.

**Contrainte de code qui rend la phase 2 quasi gratuite : écrire les rendus comme des fonctions
pures `func(TeamState, time.Time) string` dès la phase 1**, dans leur propre fichier, testées par
comparaison de chaîne littérale inline (pas de `testdata/` : un diff doit être lisible en revue).
Le `View()` de bubbletea devient un appel. Aucun `fmt.Printf` disséminé.

**Un seul binaire, pas trois.** L'alternative envisagée — un `cmd/flowlio-watch` séparé pour que
`flowlio mcp` ne linke jamais lipgloss — est refusée : le risque n'existe pas en phase 1 (zéro
dépendance Charm), et le jour où il existe, deux gardes greppables coûtent moins qu'un binaire de
plus à distribuer :

```bash
# scripts/check-tui-imports.sh, dans make lint
grep -rl 'charmbracelet' --include='*.go' . | grep -v '^./cmd/flowlio/tui/' && exit 1
grep -rn 'AdaptiveColor\|HasDarkBackground' --include='*.go' . && exit 1
```

Plus un test qui lance la poignée de main MCP avec `os.Stdout` redirigé vers un `os.Pipe` et
assert que rien d'étranger n'y transite. `newTestServer(out *bytes.Buffer)` (`mcp_test.go:14`) **ne
verrait pas** une écriture directe sur `os.Stdout` : sans ce test, la règle n'est qu'une convention,
et une convention se perd.

**`bubbles` est refusé définitivement**, même en phase 2. Son `go.mod` exige `atotto/clipboard`
(qui `exec` `pbcopy`/`xclip`/`wl-copy` depuis un process qui a lu `credentials.json`),
`MakeNowJust/heredoc`, `aymanbagabas/go-udiff`, `charmbracelet/harmonica`, `dustin/go-humanize`,
`sahilm/fuzzy`, `kylelemons/godebug`, et fait monter `x/ansi` ce qui fait entrer trois modules
`clipperhouse/*`. **Neuf propriétaires de plus** pour un curseur sur un slice (15 lignes) et une
fenêtre de défilement (20 lignes) qu'on écrit mieux à la main parce qu'on connaît nos données.

### Résolution de la team côté client

```
flowlio watch [--team <slug>] [--follow]
flowlio show <ref> [--team <slug>]
```

1. `--team` fourni → utilisé.
2. Sinon `GET /whoami`. **Si `scope != admin`, la commande sort en code 2** avec un message
   explicite — symétrique littéral de `mcp.go:117-120`. C'est ce refus qui ferme le piège
   `FromCredentials`.
3. Sinon `GET /teams` : une seule team → prise (le cas local écrasant, aucune friction) ; plusieurs
   → listées, sortie en code 2 avec `flowlio watch --team <slug>`.

**La première ligne de sortie affiche toujours l'identité résolue** (`api_url`, portée du token,
team). Un humain ne doit jamais découvrir après coup avec quel token il regarde.

Pas de `flowlio login`, pas de fichier de profils, pas de multi-compte, pas de `FLOWLIO_TEAM`.

---

## Périmètre v1 — critères vérifiables

La v1 est livrée quand tout ce qui suit est vrai. Pas quand le TUI existe : **le TUI n'est pas dans
la v1.**

1. `go build ./...`, `go vet ./...`, `make check`, `make lint` verts.
2. `make test-integration` vert, incluant les **13 tests `I`** du tableau des mutations.
3. `sql/queries/overview.sql` existe, contient exactement les 9 queries ci-dessus, et
   `scripts/check-overview-scope.sh` passe (bloc sans `team_id` → rouge ; `INSERT` → rouge ;
   `Overview` hors du module → rouge).
4. Migration `000006` appliquée en dev, `sql/schema/schema.sql` re-dumpé par `make schema`.
5. `internal/feature/overview/` respecte handler/service/store, chaque fichier < 300 lignes, chaque
   fichier ≥ 2 déclarations porte un `// SOMMAIRE` synchronisé.
6. `workspace/handler/handler.go` porte le garde `teamFor` **et** son test unitaire, commité
   **avant** le module `overview`.
7. `GET /api/overview/` avec un token de projet renvoie `403 {"error":"forbidden"}` sur les deux
   routes. Avec un token admin sans `?team=` : `400`. Avec un slug inconnu : `404`.
8. `internal/pkg/termtext` existe, `TestLineNeutralisesHostileText` couvre les 8 familles, et
   `TestNoRawServerStringReachesTheRenderer` passe sur les deux vues.
9. `flowlio watch --team <slug>` affiche l'écran 1 sur une team réelle à **trois repos avec au
   moins une issue ouverte depuis plus d'un jour**, en moins de 500 ms, sur ≤ 78 colonnes, sans
   défilement, et avec `NO_COLOR=1`.
10. `flowlio watch` avec un token de projet sort en code 2 avec un message actionnable.
11. `flowlio show CORE-41` et `flowlio show CORE-12` rendent respectivement la variante issue et la
    variante tâche, avec `kind` explicite.
12. Aucun outil MCP ne référence `/api/overview` (`TestMCPToolsNeverCallOverview`).
13. `docs/ARCHITECTURE.md` § Interfaces inter-modules porte les lignes d'`overview` et la mention
    de la **seconde règle de scope** ; l'en-tête de `sql/queries/tasks.sql` est amendé.

### Critères d'échec — écrits avant, pas après

Trois tests, sur deux semaines d'usage réel. **Un seul qui tombe suffit à ne pas acheter la phase 2.**

| # | Test | Seuil |
| - | ---- | ----- |
| 1 | **L'écran vide.** Nombre de lignes de dette affichées | médiane ≤ 5 et 80ᵉ centile ≤ 10. Au-delà, ce n'est plus une file de triage, c'est un backlog : les seuils sont faux ou `resume` produit des faux positifs en masse |
| 2 | **Le doigt.** Montrer l'écran 3 s, le masquer, demander « tu fais quoi maintenant ? » | l'humain cite **une** ref et **une** action. Sinon la hiérarchie visuelle est fausse — ce ne sont pas les couleurs, c'est l'ordre de tri et les lignes `(!)` |
| 3 | **La redondance.** Part des ouvertures se terminant sans aucune action | < 70 %. Au-delà, le TUI est un objet de confort |

> Conséquence à accepter d'avance : **si un critère tombe, la réponse n'est pas de retoucher le
> TUI, c'est de supprimer le TUI et de garder la route.** `flowlio watch` en une passe garde
> l'essentiel de la valeur pour zéro dépendance et zéro maintenance. C'est ce que le phasage protège.

---

## Ce qu'on ne fait pas

1. **Aucune route montée sous `auth.Middleware`.** La position « tout token valide, conséquence
   gratuite : un agent voit sa team » est l'inverse d'un avantage : c'est exactement l'exposition
   qu'on interdit en MCP, obtenue par la porte de derrière, et la CI ne la verrait pas.
2. **Aucune écriture.** Pas de réponse à une issue, pas de clôture, pas de changement de statut,
   pas de note. Le blocage est physique, pas budgétaire (décision #11). Le jour où ça arrive, c'est
   par un auteur humain de première classe dans le schéma, dans son propre jalon.
3. **Aucun outil MCP.** Ni `team_overview`, ni `list_team_issues`, ni rien.
4. **Aucun troisième scope de token en v1.** Bon sur le fond, hors périmètre d'une note de TUI.
5. **Aucun drapeau « nouveau », aucun curseur humain.** Pas de réutilisation de `token_cursors`,
   pas de table `human_cursors`. Le curseur appartient à un token d'agent (décision M3 #2) ; un
   « déjà vu » humain est un modèle de lecture par personne, donc une écriture.
6. **Ni SSE, ni ETag, ni GET conditionnel, ni `max(events.id)` comme jeton de fraîcheur.** `events`
   n'enregistre que des issues : un flux serait structurellement aveugle sur la moitié de l'écran.
   Ce n'est pas prématuré, c'est faux.
7. **Aucun cache.** `internal/pkg/cache` existe et sera proposé pour l'écran 1. Refuser : ce serait
   le premier état mutable partagé du produit à contenir des données de plusieurs projets, et une
   clé de cache qui oublie le `team_id` est une fuite inter-tenant silencieuse et **persistante** —
   bien pire qu'une query mal écrite, qui échoue de façon reproductible.
8. **Aucun champ dérivé côté serveur.** Pas de `health`, pas de `is_stale`, pas de durée en
   secondes, pas de couleur, pas de seuil, pas de `project.name`, pas d'extrait dans les listes,
   pas de `closed_at`.
9. **Aucun écran « backlog d'un repo ».** C'est `flowlio task list` en couleur. Un filtre `[1-9]`
   sur l'écran 1, pas un écran. Si une vue par repo revient un jour, le statut est **figé dans le
   SQL**, jamais un paramètre.
10. **Aucun écran « journal / timeline ».** `events` n'est alimenté que par les issues, ce serait
    un demi-produit ; il entre en concurrence d'attention avec la file de dette ; et le scrollback
    du terminal de l'agent répond déjà à la question. Conséquence assumée : **`events` reste
    invisible à l'humain**.
11. **Rien de terminé, clos ou archivé. Zéro pixel.** Pas de « 6 issues closes cette semaine », pas
    de compteur de `done`, pas de vélocité. C'est cette suppression qui garantit la propriété de
    l'écran vide.
12. **Aucune extraction de `httpx`/`pgerr`.** `overview/handler.go` refait `writeJSON`,
    `writeError`, `principal`, `teamFor` — quatrième copie, ~80 lignes. La décision M3 #24 a chiffré
    le commun à ~40 lignes et refusé l'extraction. À quatre copies l'arbitrage devient discutable :
    on le rouvre dans son propre ticket, pas au milieu de ce jalon.
13. **Aucun `--json` sur `flowlio watch`.** La surface machine du produit s'appelle MCP. Un second
    format de sortie est un second contrat à maintenir pour un `jq` qu'aucun agent n'appellera.
14. **Aucun fichier de configuration du TUI.** Ni thème, ni remappage de touches, ni intervalle. On
    le construit toujours, personne ne s'en sert, et chaque rapport de bug de rendu devient
    « qu'est-ce qu'il y a dans ta config ».
15. **Aucun support souris.** `tea.WithMouseCellMotion()` fait une ligne et casse la sélection de
    texte native du terminal, ce que les utilisateurs détestent au point de désinstaller.
16. **Aucun spinner.** L'API répond en moins de 20 ms sur la boucle locale.

---

## Ce que ça coûte plus tard — bloquants explicites de M7 (hosted)

> Cette surface fait passer le token admin **des métadonnées au contenu.** Aujourd'hui il lit des
> clés et des noms de projets (`workspace/service/service.go:92-97`). Après, il lit les titres de
> tâches, les notes de progression et les corps de messages d'issues **de toutes les teams du
> serveur**. En local c'est cohérent avec le modèle de menace déjà écrit dans `DESIGN-V1.md` (« en
> local, c'est le système de fichiers qui protège »). En hosted, **non**.

Trois bloquants, à traiter avant la première team payante, dans une tâche de backlog créée par ce
jalon :

| Bloquant | Pourquoi | Ce que cette note a fait pour l'anticiper |
| -------- | -------- | ----------------------------------------- |
| `AdminOnly` sur du contenu | Un admin de plateforme lirait les conversations de tous les clients | Le `team_id` ne vient jamais du principal : le changement se fait dans `teamFor` seul, le service et le store ne bougent pas |
| `GET /api/workspace/teams` n'a **aucun scope** (`sql/queries/teams.sql:12-13`) | La première chose que fait `flowlio watch` en auto-détection est d'énumérer les noms et slugs de tous les tenants | Rien. La route préexiste ; ce jalon en crée l'usage quotidien, donc c'est ici qu'il faut l'inscrire |
| Absence de scope `team` | Le credential le plus exposé du produit — celui qui vit des heures dans un terminal partagé, un `tmux`, un `asciinema`, un partage d'écran — est aujourd'hui le token racine | Le garde `teamFor` rend la migration additive au lieu d'être une réécriture |

---

## Documentation à mettre à jour, dans le même commit que le code

| Fichier | Modification |
| ------- | ------------ |
| `docs/ARCHITECTURE.md` § Domaines | une ligne `overview`, portée admin, lecture seule |
| `docs/ARCHITECTURE.md` § Interfaces inter-modules | ajouter `issue_messages`, `task_notes`, `tokens` au tableau des tables partagées ; **et** une ligne disant que la règle de scope d'`overview` est différente — le tableau actuel pose « toute query porte son scope de tenancy » et devient faux sans cette mention |
| `sql/queries/tasks.sql` en-tête | « aucune query de tâche sans scope » → « aucune query de CE fichier sans `project_id` ; la lecture team-scopée vit dans `overview.sql` » |
| `sql/queries/issues.sql` en-tête | même amendement sur la clause de visibilité canonique |
| `docs/DESIGN-V1.md` § Isolation et permissions | une ligne : le token admin lit le **contenu** d'une team via `/api/overview`, en lecture seule ; les portées projet sont inchangées |
| `docs/DESIGN-V1.md` § Binaires | `flowlio watch` et `flowlio show` |
| `CLAUDE.md` § Stack | **uniquement en phase 2**, si Charm est acheté |

---

## Découpage en tâches — dans l'ordre de dépendance

| # | Titre | Périmètre, en une ligne |
| - | ----- | ----------------------- |
| 1 | `fix(auth): un token admin porteur d'une team ne peut pas en désigner une autre` | 3 lignes dans `workspace/handler/teamFor`, son test unitaire, et l'interdiction écrite d'émettre `scope='admin' AND team_id IS NOT NULL` avant ce correctif — **bloquant, tout le reste en dépend** |
| 2 | `test(harness): injecter un Principal dans un test de route` | Faux `auth.Store`, tokens frappés par `crypto.NewToken()`, `auth.New(fake)`, `httptest.Server` sur le mux — ~60 lignes, réutilisables par tous les modules |
| 3 | `feat(pkg): termtext, évier unique de neutralisation terminale` | `Line`/`Block`/`Cells`, liste blanche `unicode.IsGraphic`, largeur via `x/text/width`, un test par famille hostile — prérequis de la première ligne de sortie du jalon |
| 4 | `feat(sql): overview.sql, queries team-scopées en lecture seule + index 000006` | Les 9 queries littérales, la migration des deux index, `make up-dev`, `make schema`, `make sqlc`, et `scripts/check-overview-scope.sh` dans `make lint` |
| 5 | `feat(overview): module de lecture team-scopée derrière AdminOnly` | `handler/` + `service/` + `store/`, 2 routes, classification des 4 types de dette, projections sans UUID, ligne dans `buildModules()` |
| 6 | `test(overview): isolation inter-team prouvée par mutation` | Les 13 tests `I` et les 8 tests `U` du tableau des mutations, fixture à deux teams × trois projets avec clés homonymes — recette : `make test-integration` |
| 7 | `feat(cli): flowlio watch et flowlio show, zéro dépendance` | Résolution de team (`--team` → whoami → une seule team), refus en code 2 sur token non admin, rendus en fonctions pures testées par chaîne littérale, `--follow` à 10 s |
| 8 | `docs: acter la seconde règle de scope du dépôt` | `ARCHITECTURE.md`, en-têtes de `tasks.sql` et `issues.sql`, `DESIGN-V1.md` § Isolation et § Binaires |
| 9 | `chore(m7): bloquants de sécurité du mode hosted` | Tâche de backlog, colonne `Blocked / decision` : `AdminOnly` sur du contenu, `GET /teams` sans scope, scope `team` — trois lignes, aucun code |
| 10 | `spike: deux semaines de flowlio watch contre une team à trois repos` | Mesurer les trois critères d'échec. Sortie : acheter Charm, ou supprimer le TUI du plan |
| 11 | *(conditionnelle)* `feat(tui): écran 1 en bubbletea` | Seulement si (10) passe. `cmd/flowlio/tui/`, poll 10 s, `r`, `q`, `[1-9]`, goldens en profil `Ascii`, `check-tui-imports.sh` |
| 12 | *(conditionnelle)* `feat(tui): écran 2, détail polymorphe` | `[entrée]`, `[y]`, `Esc`. **Franchement supprimable** si (11) n'a pas prouvé que la navigation sert |

---

## Ce que la critique adversariale a corrigé, et les deux points où elle a dépassé

**Corrections intégrées, chacune était réelle :**

- La position « `Middleware`, tout token valide » était une fuite inter-projets que la CI n'aurait
  pas vue → décision #2.
- Les trois formes d'`oldest_wait` proposées par les angles étaient toutes inlivrables, vérifié en
  exécutant sqlc 1.30 : castée → `time.Time` non-nullable qui casse le `Scan` sur une team saine,
  non castée → `interface{}` interdit → décision #17 et `OverviewLastSeen` en `GROUP BY` dédié.
- L'escalade de privilège par `teamFor` était réelle et deux angles proposaient d'émettre le token
  avant le correctif → prérequis bloquant, tâche #1.
- `ap.team_id = i.team_id` sur `issue_messages` **est** load-bearing, FK simple vérifiée dans
  `schema.sql:503` → mutation 14, la seule de sa famille qui se teste.
- `client.FromCredentials` exige les deux variables ensemble et retombe silencieusement sur le
  token admin → mutation 19.
- Le harnais de test de route n'existe pas et `auth.contextKey` est privé → tâche #2, budgétée.
- `make check` ne prouve rien sur l'isolation → registres `U`/`I` explicites partout.
- `termtext` est un prérequis de la phase 1, pas du TUI → décision #20, tâche #3 avant la tâche #7.

**Deux points où elle a dépassé, et pourquoi la note ne la suit pas :**

1. **Le second binaire `cmd/flowlio-watch`.** Le risque décrit (lipgloss linké dans `flowlio mcp`)
   est réel mais n'existe qu'en phase 2, qui n'est pas achetée. Deux gardes greppables et un test de
   pipe MCP coûtent moins qu'un binaire de plus à construire, distribuer et documenter.
2. **« La clause `p.team_id = i.team_id` n'est pas testable, donc le contrôle réel est la FK. »**
   Exact comme diagnostic, incomplet comme conclusion : la clause reste écrite dans chaque join,
   parce qu'une revue doit pouvoir vérifier chaque `FROM issues` sans raisonner sur les FK. On ne
   teste pas la clause, on teste la FK (mutation 8) — et on garde la clause.

---

## Questions qui restent pour l'humain

Deux, chacune tranchée par défaut. **Si Maxence ne dit rien, la valeur par défaut s'applique.**

1. **Faut-il supprimer les quatre sous-commandes d'écriture de la CLI** (`flowlio task create`,
   `status`, `note`, `archive`) ? L'argument pour : elles existent pour un utilisateur que FLWL-20
   déclare inexistant, elles dupliquent quatre outils MCP, et si on justifie la lecture seule du TUI
   par « les agents écrivent, les humains lisent », on ne peut pas laisser la CLI faire l'inverse.
   L'argument contre : dépannage réel, ~80 lignes déjà livrées et testées.
   **Par défaut : on les garde.** Casser un usage réel pour la pureté d'une doctrine se vérifie
   avant, pas après.

2. **Le seuil de `resume` (24 h) est-il le bon ?** Si les agents laissent par habitude leurs tâches
   en `in_progress` pendant des jours, le bloc « À L'ARRÊT » sera en permanence plein de faux
   positifs, l'écran ne sera jamais vide, et la propriété qui fonde tout le design s'effondre.
   **Par défaut : 24 h, en constante nommée du service, mesurée par le critère d'échec 1.** Et si
   ça tombe, le correctif honnête n'est pas de monter le seuil jusqu'à ce que l'écran soit joli :
   c'est de **retirer `resume` du produit**.
