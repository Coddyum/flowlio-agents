package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                    | Ligne |
// |---------------------|-----------------------------------------------------------|-------|
// | store.TokenByPrefix | Reads a token by its public part and projects it           | 24    |
// | store.TouchToken    | Records the last-use date of a token                       | 46    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// TokenByPrefix reads the token matching the presented prefix, revoked or not: the service is
// what decides, so that every failure costs the same time.
func (s *store) TokenByPrefix(ctx context.Context, prefix string) (TokenRecord, error) {
	row, err := s.q.GetTokenByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenRecord{}, ErrTokenNotFound
		}
		return TokenRecord{}, fmt.Errorf("auth store: token by prefix: %w", err)
	}

	return TokenRecord{
		ID:         row.ID,
		Scope:      Scope(row.Scope),
		TeamID:     row.TeamID.UUID,
		ProjectID:  row.ProjectID.UUID,
		SecretHash: row.SecretHash,
		Revoked:    row.RevokedAt.Valid,
		LastUsedAt: row.LastUsedAt.Time,
	}, nil
}

// TouchToken records the last-use date. Best effort: its failure must not reject an otherwise
// authenticated request.
func (s *store) TouchToken(ctx context.Context, id uuid.UUID) error {
	if err := s.q.TouchToken(ctx, id); err != nil {
		return fmt.Errorf("auth store: touch token %s: %w", id, err)
	}
	return nil
}
