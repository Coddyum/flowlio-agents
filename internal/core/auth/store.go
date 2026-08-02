package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | TokenRecord | Vue du token nécessaire à l'authentification, sans type sqlc        | 32    |
// | Store       | Contrat de persistance consommé par le service d'auth               | 44    |
// | store       | Implémentation adossée aux queries générées                         | 50    |
// | NewStore    | Crée le store d'authentification                                    | 55    |
//
// Fin du sommaire.
// =====================================================================
//
// CONTRAT UNIQUEMENT — l'implémentation est dans store_token.go.

import (
	"context"
	"errors"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ErrTokenNotFound signale un préfixe inconnu. Le service le traduit en ErrUnauthenticated :
// l'extérieur ne doit pas savoir si le préfixe existait.
var ErrTokenNotFound = errors.New("auth store: token not found")

// TokenRecord est la projection du token utile à l'authentification. Les types sqlc ne
// remontent pas jusqu'au service.
type TokenRecord struct {
	ID         uuid.UUID
	Scope      Scope
	TeamID     uuid.UUID
	ProjectID  uuid.UUID
	SecretHash string
	Revoked    bool
	LastUsedAt time.Time
}

// Store est le contrat de persistance de l'authentification. Volontairement minimal : l'auth
// lit un token et note son usage, rien d'autre.
type Store interface {
	TokenByPrefix(ctx context.Context, prefix string) (TokenRecord, error)
	TouchToken(ctx context.Context, id uuid.UUID) error
}

// store adosse le contrat aux queries générées par sqlc.
type store struct {
	q *database.Queries
}

// NewStore crée le store d'authentification.
func NewStore(q *database.Queries) Store {
	return &store{q: q}
}
