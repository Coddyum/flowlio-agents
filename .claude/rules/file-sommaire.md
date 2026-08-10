# Rule — the summary header of a .go file

Referenced by `CLAUDE.md`.

## Principle

A `.go` file with **≥ 2 top-level declarations** (`func`/`type`) must carry, right after
`package xxx`, a `// SOMMAIRE` comment block listing each declaration with a one-sentence
description and its line number. The point: jump straight to the right passage without rereading the
whole file.

Excluded files: `internal/database/*` (sqlc-generated), files headed
`// Code generated ... DO NOT EDIT`, and `_test.go`.

## Exact format

```go
package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                       | Ligne |
// |------------|----------------------------------------------|-------|
// | NewService | Builds the service with its dependencies      | 14    |
// | CreateUser | Inserts a user and returns its ID             | 30    |
//
// Fin du sommaire.
// =====================================================================

import (
	...
)
```

- Exact opening marker: `// SOMMAIRE (lire en premier, sauter directement au bon passage)`.
- Closing marker: a `// ====...` line (free length, a few `=` at least).
- **The three French strings stay French, even in a brand-new file**: the two markers and the
  `| Élément | Résumé | Ligne |` header row. `check-sommaire.sh` drops the header from the count
  with `grep -vE '^// \| *Élément'`; translated, it is counted as a declaration and the hook blocks.
  `sync-sommaire-lines.sh` matches the same three. They are also shared verbatim with `flowlio-core`
  and `Flowlio`, so changing them here would fail the guard in three repositories at once. Only the
  **descriptions in the cells** follow the repository's language, which is English.
- One table row per top-level declaration (func, `Type.Method` method, type).
- The "Ligne" column is the **final** line number — after the block has been inserted, so shifted.
- The description is one short sentence, written from understanding the code, not mechanically
  extracted from the function's name.

## Mandatory maintenance (not negotiable)

On every creation, change or removal of a top-level declaration in a `.go` file:

1. Update the summary in the same session (add or remove a row, recompute the shifted line numbers).
2. If the file drops below 2 declarations, remove the summary block.
3. If a new file reaches 2 declarations, create the block.

`make sommaire` (`scripts/sync-sommaire-lines.sh`) recomputes the line numbers on its own. It never
adds nor removes a row: writing the description of a new declaration is a judgement call and stays
with the author.

## The automatic guard

A `PostToolUse` hook (after a `.go` edit) that:

- counts the top-level declarations (`grep -cE '^(func |type )'`),
- if ≥ 2: checks the marker is present and that the number of table rows equals the number of
  declarations,
- on failure: blocks (exit 2).

This guard checks presence and structural synchronisation, not the *quality* of the descriptions —
that stays the responsibility of whoever edits the file.
