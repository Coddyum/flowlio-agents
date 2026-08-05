package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | service.CreateToken | Issues an agent token scoped to a project                 | 28    |
// | service.ListTokens  | Lists a project's tokens, with no secret                  | 68    |
// | service.RevokeToken | Revokes one of the team's tokens                          | 93    |
// | toTokenInfo         | Projects a store token onto the listing view              | 101   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// CreateToken issues an agent token for one of the team's projects.
//
// The secret in clear exists only in the return value: it is neither persisted, nor logged, nor
// displayable again. A lost token is revoked and reissued, it is not recovered.
func (s *service) CreateToken(ctx context.Context, in CreateTokenInput) (CreatedToken, error) {
	key := strings.ToUpper(strings.TrimSpace(in.ProjectKey))
	name := strings.TrimSpace(in.Name)

	if in.TeamID == uuid.Nil {
		return CreatedToken{}, ErrInvalidInput
	}
	if err := validateKey(key); err != nil {
		return CreatedToken{}, err
	}
	if err := validateName("token name", name); err != nil {
		return CreatedToken{}, err
	}

	project, err := s.store.ProjectByKey(ctx, in.TeamID, key)
	if err != nil {
		return CreatedToken{}, translateStore(err, "project "+key)
	}

	token, err := crypto.NewToken()
	if err != nil {
		return CreatedToken{}, translateStore(err, "generate token")
	}

	created, err := s.store.CreateToken(ctx, in.TeamID, project.ID, name, token.Prefix, token.Hash)
	if err != nil {
		return CreatedToken{}, translateStore(err, "create token")
	}

	return CreatedToken{
		ID:         created.ID,
		Name:       created.Name,
		Prefix:     created.Prefix,
		ProjectKey: project.Key,
		Secret:     token.Plain,
	}, nil
}

// ListTokens lists the tokens of one of the team's projects. The secret exists nowhere in the
// database: only the public prefix and the dates are exposed.
func (s *service) ListTokens(ctx context.Context, teamID uuid.UUID, projectKey string) ([]TokenInfo, error) {
	key := strings.ToUpper(strings.TrimSpace(projectKey))
	if err := validateKey(key); err != nil {
		return nil, err
	}

	project, err := s.store.ProjectByKey(ctx, teamID, key)
	if err != nil {
		return nil, translateStore(err, "project "+key)
	}

	rows, err := s.store.ListTokens(ctx, teamID, project.ID)
	if err != nil {
		return nil, translateStore(err, "list tokens")
	}

	tokens := make([]TokenInfo, 0, len(rows))
	for _, row := range rows {
		tokens = append(tokens, toTokenInfo(row))
	}
	return tokens, nil
}

// RevokeToken revokes one of the team's tokens. Replaying the revocation yields ErrNotFound: the
// query only targets tokens that are still active.
func (s *service) RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) error {
	if _, err := s.store.RevokeToken(ctx, teamID, tokenID); err != nil {
		return translateStore(err, "revoke token")
	}
	return nil
}

// toTokenInfo projects a store token onto the listing view.
func toTokenInfo(t store.Token) TokenInfo {
	info := TokenInfo{
		ID:        t.ID,
		Name:      t.Name,
		Prefix:    t.Prefix,
		CreatedAt: t.CreatedAt,
		Revoked:   t.Revoked,
	}
	if !t.LastUsedAt.IsZero() {
		lastUsed := t.LastUsedAt.UTC()
		info.LastUsedAt = &lastUsed
	}
	return info
}
