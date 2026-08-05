package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                   | Ligne |
// |-------------------|----------------------------------------------------------|-------|
// | store.CreateToken | Inserts a project token (public prefix + hash)            | 24    |
// | store.ListTokens  | Lists a project's tokens, secrets excluded                | 40    |
// | store.RevokeToken | Revokes one of the team's tokens, exactly once            | 58    |
// | toToken           | Projects an sqlc row onto the domain token                | 70    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateToken inserts a project token. The secret in clear never reaches this layer: only its hash
// is supplied by the service.
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

// ListTokens lists a project's tokens. No secret is returned — none exists in the database, only
// hashes.
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

// RevokeToken revokes one of the team's tokens. The query only touches project tokens not yet
// revoked: replaying the revocation yields ErrNotFound rather than lying.
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

// toToken projects a generated row onto the domain type.
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
