package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément | Résumé                                                                | Ligne |
// |---------|-----------------------------------------------------------------------|-------|
// | Store   | Contract: the one fact reference resolution cannot borrow from a peer   | 41    |
// | store   | Implementation backed by the sqlc-generated queries                     | 47    |
// | New     | Creates the ref store                                                   | 52    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation is in caller.go.
//
// THE SMALLEST STORE IN THE REPOSITORY, AND IT MUST STAY THAT WAY. The `ref` feature owns no
// table: everything it returns comes from `task` and `issue`, through the FeatureRegistry. It
// keeps a store for exactly one fact neither of them can give it — the caller's OWN project key,
// which is what tells CORE-34 (mine, so possibly a task) from FRNT-34 (a sibling's, so
// necessarily an issue).
//
// No Transactor: this surface reads, and reads only.

import (
	"context"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrNotFound signals a project that does not exist in the requested scope.
var ErrNotFound = errors.New("ref store: not found")

// Store reads the caller's own project key.
//
// A single method, and adding a second is a design decision, not a convenience: any other fact
// about a task or an issue belongs to the feature that owns it, and is reached through the
// registry. A query added here would be a second read path onto someone else's table, with its
// own chance of getting tenancy wrong.
type Store interface {
	// CallerProjectKey resolves the key of the project the token is scoped to.
	CallerProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error)
}

// store backs the contract with the generated queries. No *sql.DB: nothing here writes.
type store struct {
	q *database.Queries
}

// New creates the ref store.
func New(q *database.Queries) Store {
	return &store{q: q}
}
