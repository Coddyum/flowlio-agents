# Règle — suivi du projet dans Flowlio (MCP `mcp__flowlio__*`)

Référencée par `CLAUDE.md`. Flowlio est le tracker qui porte l'état de **ce** projet entre les
sessions. Il remplace les fichiers de suivi en markdown : aucun `PROGRESS.md`, `TODO.md` ou
`NEXT-STEPS.md` ne doit exister dans ce dépôt.

Trois niveaux de mémoire, à ne pas mélanger :

| Niveau      | Outil                | Durée de vie                                  |
| ----------- | -------------------- | --------------------------------------------- |
| Session     | `TodoWrite`          | meurt avec la conversation                     |
| Projet      | **Flowlio**          | survit aux sessions — état réel du produit     |
| Conception  | `docs/` du dépôt     | décisions et architecture, versionnées avec le code |

Une décision d'architecture va dans `docs/`, pas dans une tâche. Une tâche dit **quoi faire**,
`docs/` dit **pourquoi c'est comme ça**.

---

## Le board

Team **Flowlio** (`FLOWL`) → projet **FLOWLIO_IA** (`FLWL`).

Résoudre les ids par nom à chaque session (`list_teams` → `list_projects` → `list_columns`).
**Ne jamais coder un id en dur** : le board peut être réorganisé entre deux sessions.

| Colonne              | Contenu                                                              |
| -------------------- | -------------------------------------------------------------------- |
| `Ready`              | Prêt à démarrer, périmètre clair. **C'est ici qu'on pioche.**         |
| `Unnamed column`     | Backlog — jalons pas encore ouverts (à renommer « Backlog » côté UI)  |
| `In progress`        | En cours dans la session courante. Une seule tâche à la fois.         |
| `Blocked / decision` | Attend un arbitrage de Maxence, ou une dépendance non livrée.         |
| `Done`               | Livré, testé, commité.                                                |

---

## Protocole de session — non négociable

**Au démarrage, avant toute autre chose :**

1. `list_project_tasks` sur FLOWLIO_IA — c'est la source de vérité de ce qui reste à faire.
2. Lire la colonne `In progress` : une tâche qui y traîne signale une session interrompue.
   Reprendre celle-là avant d'en ouvrir une nouvelle.
3. Lire `Blocked / decision` : si Maxence a tranché depuis, la débloquer.
4. `get_task` sur la tâche choisie pour récupérer le périmètre complet, puis `move_task` vers
   `In progress`.

**Pendant :** `TodoWrite` pour le découpage fin de la session. Ne pas dupliquer dans Flowlio.

**En fin de session, systématiquement :**

- `update_task` : compléter la description avec ce qui est fait, ce qui reste, et les décisions
  prises. C'est ce que lira la session suivante — écrire pour quelqu'un qui n'a aucun contexte.
- `move_task` vers `Done` si livré et commité, `Blocked / decision` si ça attend un arbitrage,
  sinon laisser dans `In progress` avec l'état à jour.
- Créer une tâche pour tout travail identifié en chemin et non fait.

> Une session qui se termine sans mise à jour du board fait perdre son contexte à la suivante.
> C'est le seul défaut réellement coûteux de ce dispositif.

---

## Archiver

`archive_task` dès qu'une tâche est **réellement** terminée : livrée, testée, commitée, et son
jalon clos. Pas « rangée » — terminée.

Garder dans `Done` le dernier jalon livré : il sert de point de repère à la session suivante.
Archiver les précédents. Une archive reste lisible (`list_project_archived_tasks`,
`get_archived_task`) et se rouvre avec `unarchive_task` en cas de régression.

Ne jamais chercher dans les archives ce qu'il faut faire maintenant : c'est le rôle de
`list_project_tasks`.

---

## Écrire une tâche

Markdown complet supporté, et le board est fait pour être scanné en quelques secondes :

- **Tableau** dès qu'il y a une correspondance à lister (brique/état, option/coût)
- **Bloc de code** pour une signature, une commande, une surface d'API — jamais décrit en prose
- **Blockquote** (`>`) pour isoler ce qui compte : contrainte de sécurité, dépendance bloquante
- `##` pour découper « Périmètre » / « Règles » / « Fini quand »

Toute tâche de développement porte une section **Fini quand**, exprimée en critères vérifiables
(`make check` vert, test d'intégration couvrant X), pas en intentions.

Si la description dépasse ce qui se lit en 30 secondes, la tâche est trop grosse : la découper,
ou renvoyer vers `docs/DESIGN-V1.md`.

---

## Secrets — spécifique à ce projet

> Ce projet **fabrique des tokens**. Un `flw_...` collé dans une description de tâche est un
> secret publié sur un board tiers, et il n'existe pas d'outil de suppression dans Flowlio :
> seulement l'archive, qui conserve le contenu.

Jamais de token, de DSN avec mot de passe, ni de contenu de `~/.config/flowlio/credentials.json`
dans une tâche. Pour illustrer, écrire `flw_<prefix>_<secret>`.

---

## Vocabulaire des labels

Le serveur refuse les labels inconnus. Constaté :

| Champ      | Valide      | Refusé                 |
| ---------- | ----------- | ---------------------- |
| `priority` | `urgent`    | `high`                 |
| `status`   | —           | `in-progress`          |

Par défaut : `no-priority` / `no-status`. Ne pas deviner un label — la colonne porte déjà l'état,
la priorité ne sert qu'à marquer ce qui passe avant le reste. Liste complète à confirmer avec
Maxence si le besoin se présente.

---

## Ce que Claude ne fait pas

- Démarrer à coder sans avoir lu le board — la session précédente y a laissé son état
- Deviner un id de team, projet, colonne ou tâche sans le `list_*` correspondant
- Créer une tâche pour un fix trivial fait dans la foulée (`TodoWrite` suffit)
- Recréer en markdown un suivi que le board porte déjà
- Mettre un secret, un token ou un DSN dans une description
- Archiver une tâche non terminée, ou modifier une archivée sans `unarchive_task` d'abord
- Laisser une tâche en `In progress` sans description à jour en fin de session
