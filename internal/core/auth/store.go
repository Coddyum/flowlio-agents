package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | TokenRecord | View of the token needed to authenticate, with no sqlc type         | 32    |
// | Store       | Persistence contract consumed by the auth service                   | 44    |
// | store       | Implementation backed by the generated queries                      | 50    |
// | NewStore    | Creates the authentication store                                    | 55    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRACT ONLY — the implementation lives in store_token.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ErrTokenNotFound signals an unknown prefix. The service translates it into ErrUnauthenticated:
// the outside must not learn whether the prefix existed.
var ErrTokenNotFound = errors.New("auth store: token not found")

// TokenRecord is the projection of the token useful to authentication. The sqlc types do not
// do not climb up to the service.
type TokenRecord struct {
	ID         uuid.UUID
	Scope      Scope
	TeamID     uuid.UUID
	ProjectID  uuid.UUID
	SecretHash string
	Revoked    bool
	LastUsedAt time.Time
}

// Store is the persistence contract of the authentication. Deliberately minimal: auth reads a
// token and records its use, nothing else.
type Store interface {
	TokenByPrefix(ctx context.Context, prefix string) (TokenRecord, error)
	TouchToken(ctx context.Context, id uuid.UUID) error
}

// store backs the contract with the sqlc-generated queries.
type store struct {
	q *database.Queries
}

// NewStore creates the authentication store.
func NewStore(q *database.Queries) Store {
	return &store{q: q}
}
