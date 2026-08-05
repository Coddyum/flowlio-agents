# Règle — sommaire en tête de fichier .go

Référencée par `CLAUDE.md`.

## Principe

Un fichier `.go` avec **≥ 2 déclarations top-level** (`func`/`type`) doit avoir, juste après
`package xxx`, un bloc commentaire `// SOMMAIRE` listant chaque déclaration avec une description
en une phrase et son numéro de ligne. Objectif : sauter directement au bon passage sans relire
tout le fichier.

Fichiers exclus : `internal/database/*` (généré sqlc), fichiers avec en-tête
`// Code generated ... DO NOT EDIT`.

## Format exact

```go
package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                      | Ligne |
// |------------|----------------------------------------------|-------|
// | NewService | Crée le service avec ses dépendances          | 14    |
// | CreateUser | Insère un utilisateur et renvoie son ID        | 30    |
//
// Fin du sommaire.
// =====================================================================

import (
	...
)
```

- Marqueur de début exact : `// SOMMAIRE (lire en premier, sauter directement au bon passage)`.
- Marqueur de fin : ligne `// ====...` (longueur libre, ≥ quelques `=`).
- **La ligne d'en-tête `| Élément | Résumé | Ligne |` reste en français**, même dans un fichier
  neuf. `check-sommaire.sh` l'écarte du compte par `grep -vE '^// \| *Élément'` : traduite, elle
  est comptée comme une déclaration et le hook bloque. Seules les **descriptions dans les cellules**
  suivent la langue du dépôt.
- Une ligne de tableau par déclaration top-level (func, méthode `Type.Method`, type).
- Colonne "Ligne" = numéro de ligne **final** (après insertion du bloc, donc décalé).
- Description = 1 phrase courte, écrite à partir de la compréhension du code, pas une extraction
  mécanique du nom de fonction.

## Maintenance obligatoire (non négociable)

À chaque création, modification ou suppression de déclaration top-level dans un fichier `.go` :

1. Mettre à jour le sommaire dans la même session (ajout/suppression de ligne, recalcul des
   numéros de ligne décalés).
2. Si le fichier passe sous 2 déclarations, retirer le bloc sommaire.
3. Si un nouveau fichier atteint 2 déclarations, créer le bloc.

## Garde-fou automatique (recommandé)

Un hook `PostToolUse` (après édition d'un `.go`) qui :

- compte les déclarations top-level (`grep -cE '^(func |type )'`),
- si ≥ 2 : vérifie présence du marqueur + nombre de lignes du tableau == nombre de déclarations,
- échec → bloque (exit 2).

Ce garde-fou vérifie la présence et la synchronisation structurelle, pas la *qualité* des
descriptions — celle-ci reste sous la responsabilité de Claude lors de l'édition.
