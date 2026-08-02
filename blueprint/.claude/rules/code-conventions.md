# Règle — conventions de code

Référencée par `CLAUDE.md`. Détail de la brique "Conventions de code".

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
