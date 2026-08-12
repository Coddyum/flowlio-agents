package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | Position | The project relevance head and the token read cursor, as two integers | 32    |
// | Store    | Cold read that seeds the probe when its cache is empty               | 39    |
// | store    | Implementation backed by the sqlc-generated queries                  | 50    |
// | New      | Creates the wake store                                              | 55    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in position.go.
//
// The wake store exists for ONE query, and only on a cold cache: the probe answers from memory in
// steady state (D55, docs/DESIGN-WAKE.md §3). When the process has just started, or a signal has
// expired, this reads the two scalars once — max(events.id) addressed to the project and the token
// cursor — so the probe can seed itself and go quiet again. It reuses the inbox's InboxCursor query
// verbatim: the same two integers the inbox already reads before its buckets.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Position carries the project's relevance head (the latest event addressed to it) and the read
// position of the token. The probe has work to report when Head is strictly above Cursor.
type Position struct {
	Head   int64
	Cursor int64
}

// Store reads, on a cold cache only, the two integers the probe compares — and, when the journal has
// moved, whether that movement is actionable work and at what tier.
type Store interface {
	// Position returns the project relevance head and the token cursor in a single query.
	Position(ctx context.Context, teamID, projectID, tokenID uuid.UUID) (Position, error)
	// Actionable answers whether there is NEW work worth launching a session for — a new incoming
	// question, a new answer, a newly unblocked task — and the highest rigour tier among it. Read
	// ONLY once the probe knows head > cursor, so it never touches the idle path (FLWL-85). cursor is
	// the token's read position, the boundary "new" is measured from.
	Actionable(ctx context.Context, teamID, projectID uuid.UUID, cursor int64) (actionable bool, effort string, err error)
}

// store backs the contract with the generated queries. No transaction: the probe only ever reads.
type store struct {
	q *database.Queries
}

// New creates the wake store.
func New(q *database.Queries) Store {
	return &store{q: q}
}
