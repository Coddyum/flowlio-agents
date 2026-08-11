package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | Position | The team journal head and the token read cursor, as two integers     | 32    |
// | Store    | Cold read that seeds the probe when its cache is empty               | 38    |
// | store    | Implementation backed by the sqlc-generated queries                  | 44    |
// | New      | Creates the wake store                                              | 49    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in position.go.
//
// The wake store exists for ONE query, and only on a cold cache: the probe answers from memory in
// steady state (D55, docs/DESIGN-WAKE.md §3). When the process has just started, or a signal has
// expired, this reads the two scalars once — max(events.id) of the team and the token cursor — so
// the probe can seed itself and go quiet again. It reuses the inbox's InboxCursor query verbatim:
// the same two integers the inbox already reads before its buckets.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Position carries the head of the team journal and the read position of the token. The probe has
// work to report when Head is strictly above Cursor.
type Position struct {
	Head   int64
	Cursor int64
}

// Store reads, on a cold cache only, the two integers the probe compares.
type Store interface {
	// Position returns the team journal head and the token cursor in a single query.
	Position(ctx context.Context, teamID, tokenID uuid.UUID) (Position, error)
}

// store backs the contract with the generated queries. No transaction: the probe only ever reads.
type store struct {
	q *database.Queries
}

// New creates the wake store.
func New(q *database.Queries) Store {
	return &store{q: q}
}
