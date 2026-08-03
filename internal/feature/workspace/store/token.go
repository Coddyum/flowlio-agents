package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                   | Ligne |
// |-------------------|----------------------------------------------------------|-------|
// | store.CreateToken | Insère un token de projet (préfixe public + hash)         | 24    |
// | store.ListTokens  | Liste les tokens d'un projet, secrets exclus              | 40    |
// | store.RevokeToken | Révoque un token de la team, une seule fois               | 58    |
// | toToken           | Projette une ligne sqlc en token domaine                  | 70    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateToken insère un token de projet. Le secret en clair n'atteint jamais cette couche :
// seul son hash est fourni par le service.
func (s *store) CreateToken(ctx context.Context, teamID, projectID uuid.UUID, name, prefix, hash string) (Token, error) {
	row, err := s.q.CreateProjectToken(ctx, database.CreateProjectTokenParams{
		TeamID:     uuid.NullUUID{UUID: teamID, Valid: true},
		ProjectID:  uuid.NullUUID{UUID: projectID, Valid: true},
		Name:       name,
		Prefix:     prefix,
		SecretHash: hash,
	})
	if err != nil {
		return Token{}, translate(err, "create token")
	}
	return toToken(row), nil
}

// ListTokens liste les tokens d'un projet. Aucun secret n'est renvoyé — il n'en existe aucun
// en base, seulement des hashs.
func (s *store) ListTokens(ctx context.Context, teamID, projectID uuid.UUID) ([]Token, error) {
	rows, err := s.q.ListProjectTokens(ctx, database.ListProjectTokensParams{
		TeamID:    uuid.NullUUID{UUID: teamID, Valid: true},
		ProjectID: uuid.NullUUID{UUID: projectID, Valid: true},
	})
	if err != nil {
		return nil, translate(err, "list tokens")
	}

	tokens := make([]Token, 0, len(rows))
	for _, row := range rows {
		tokens = append(tokens, toToken(row))
	}
	return tokens, nil
}

// RevokeToken révoque un token de la team. La query ne touche que les tokens de projet non
// encore révoqués : rejouer la révocation remonte ErrNotFound plutôt que de mentir.
func (s *store) RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) (Token, error) {
	row, err := s.q.RevokeProjectToken(ctx, database.RevokeProjectTokenParams{
		ID:     tokenID,
		TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	})
	if err != nil {
		return Token{}, translate(err, "revoke token")
	}
	return toToken(row), nil
}

// toToken projette une ligne générée en type domaine.
func toToken(row database.Token) Token {
	return Token{
		ID:         row.ID,
		TeamID:     row.TeamID.UUID,
		ProjectID:  row.ProjectID.UUID,
		Name:       row.Name,
		Prefix:     row.Prefix,
		CreatedAt:  row.CreatedAt,
		LastUsedAt: row.LastUsedAt.Time,
		Revoked:    row.RevokedAt.Valid,
	}
}
