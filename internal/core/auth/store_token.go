package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                    | Ligne |
// |---------------------|-----------------------------------------------------------|-------|
// | store.TokenByPrefix | Lit un token par sa partie publique et le projette         | 24    |
// | store.TouchToken    | Note la date de dernier usage d'un token                   | 46    |
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

// TokenByPrefix lit le token correspondant au préfixe présenté, révoqué ou non : c'est le
// service qui décide, pour que tous les échecs coûtent le même temps.
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

// TouchToken note la date de dernier usage. Best effort : l'échec ne doit pas refuser une
// requête par ailleurs authentifiée.
func (s *store) TouchToken(ctx context.Context, id uuid.UUID) error {
	if err := s.q.TouchToken(ctx, id); err != nil {
		return fmt.Errorf("auth store: touch token %s: %w", id, err)
	}
	return nil
}
