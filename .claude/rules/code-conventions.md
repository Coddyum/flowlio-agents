# Règle — conventions de code

Référencée par `CLAUDE.md`. Détail de la brique "Conventions de code".

## Langue — anglais

> Le dépôt part en open source. Un historique et des commentaires en français excluent les
> contributeurs et se lisent comme un projet interne.

- **Messages de commit : anglais.** Sans exception, depuis le 2026-08-05.
- **Tout texte porté par le code : anglais** — commentaires, descriptions de `// SOMMAIRE`, noms
  d'identifiants, messages d'erreur, aide CLI.
- **Un fichier neuf naît en anglais**, même si ses voisins sont en français. Ne jamais
  « harmoniser » un fichier neuf vers le français au nom de la cohérence locale.
- **`cmd/` et `internal/` sont en anglais depuis le 2026-08-05** — aucun français n'y subsiste,
  marqueurs de sommaire exceptés. Une régression s'y traite comme une faute de style ordinaire,
  pas comme une dette.
- **Il reste `docs/` et `sql/`, et ils se traduisent AU PASSAGE, jamais en séance dédiée.** Arbitré
  par Maxence le 2026-08-06 : trois heures de traduction pure ne font pas avancer le produit. La
  règle mécanique qui remplace la carte : **un fichier de `docs/` ou `sql/` qu'on modifie pour une
  autre raison part en anglais dans le même commit.** On ne traduit pas un fichier qu'on n'avait
  pas de raison d'ouvrir.
- Exceptions, et elles seules : les **marqueurs littéraux** du bloc sommaire
  (`SOMMAIRE (lire en premier…)`, `Fin du sommaire.`), vérifiés tels quels par
  `scripts/check-sommaire.sh` ; les **descriptions de tâches Flowlio** et les échanges avec
  Maxence, qui ne sont pas du code.

> **Un titre traduit sans sa citation est pire qu'un titre français** : la citation pointe une
> section qui n'existe plus. Avant de renommer un `##` dans `docs/`, chercher qui le cite —
> `grep -rn '§ <titre>' --include='*.go' --include='*.sql' --include='*.md'` — et tout changer d'un
> geste. Les citations de `sql/queries/` sont recopiées par sqlc dans `internal/database/` : un
> `make sqlc` fait partie du geste.

### Les deux dettes précises, à solder le jour où ces fichiers s'ouvrent

Vérifiées le 2026-08-06 — elles sont réelles, pas un inventaire recopié. La carte qui les portait
(FLWL-49) est archivée : la règle ci-dessus fait le travail, une carte qui ne peut jamais être
finie ne le fait pas.

| Fichier | Ce qu'il faut faire au passage |
| --- | --- |
| `docs/DESIGN-TUI.md` l. 780 | Titre `## Garanties de sécurité — …` cité tel quel par 4 tests (`cmd/flowlio/mcp_overview_test.go`, `internal/feature/overview/{module,handler/handler,store/store_integration}_test.go`). Les 5 lignes bougent d'un seul geste — ce sont les seules occurrences de français sous `cmd/`+`internal/`. |
| `docs/DESIGN-M3.md` l. 41 et 596 | Citent `"transaction imbriquée"` ; le code dit `"nested transaction"` depuis les lots 6b/6c. Une doc qui cite une chaîne périmée est fausse — c'est une correction, pas une traduction. |
| `docs/DESIGN-M3.md` ~l. 846, `docs/DESIGN-TRUST.md` ~l. 556 | Citent les instructions de session, changées au lot 5. |

## Nommage

> Si un nom de variable, fonction ou fichier nécessite un commentaire pour être compris, le nom
> est mauvais. Renomme d'abord.

- Noms explicites (`userSessionStore` > `uss`, `createUserHandler` > `cuh`).
- Fichiers nommés par responsabilité unique et claire.

## Gestion des erreurs

- Un log doit permettre de savoir **quoi** a échoué, **où**, et **pourquoi** sans chercher dans
  le code.
- Toujours wrapper avec contexte : `fmt.Errorf("user store: get by id %s: %w", id, err)`.
- **`log.Fatal` interdit hors de `main.go`** et des initialisations au démarrage.
- Pas de `panic` dans la logique métier.

## Principes

- **Performance** : pas de travail inutile, pas d'allocation évitable.
- **DRY** : si un pattern se répète plus de deux fois, extraire.
- **SRP** : chaque fichier, fonction, type a une seule responsabilité.
- Pas d'over-engineering : si la solution simple suffit, c'est la bonne.

## Style général

- Conventions Go idiomatiques (`errors.Is`/`errors.As`, interfaces petites et ciblées,
  table-driven tests).
- Pas de `interface{}` / `any` sauf nécessité absolue justifiée.
- Pas d'ORM.
- Pas de `func init()`.
