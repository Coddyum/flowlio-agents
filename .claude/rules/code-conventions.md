# Règle — conventions de code

Référencée par `CLAUDE.md`. Détail de la brique "Conventions de code".

## Langue — anglais

> Le dépôt part en open source. Un historique et des commentaires en français excluent les
> contributeurs et se lisent comme un projet interne.

- **Messages de commit : anglais.** Sans exception, depuis le 2026-08-05.
- **Tout texte porté par le code : anglais** — commentaires, descriptions de `// SOMMAIRE`, noms
  d'identifiants, messages d'erreur, aide CLI.
- **Un fichier neuf naît en anglais**, même si ses voisins sont en français. L'existant est un
  stock à écouler (carte FLWL-49 au board), pas une convention à suivre : ne jamais « harmoniser »
  un fichier neuf vers le français au nom de la cohérence locale.
- Exceptions, et elles seules : les **marqueurs littéraux** du bloc sommaire
  (`SOMMAIRE (lire en premier…)`, `Fin du sommaire.`), vérifiés tels quels par
  `scripts/check-sommaire.sh` ; les **descriptions de tâches Flowlio** et les échanges avec
  Maxence, qui ne sont pas du code.

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
