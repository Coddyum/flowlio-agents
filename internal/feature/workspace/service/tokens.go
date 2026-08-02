package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | service.CreateToken | Émet un token d'agent scopé à un projet                   | 28    |
// | service.ListTokens  | Liste les tokens d'un projet, sans secret                 | 68    |
// | service.RevokeToken | Révoque un token de la team                               | 93    |
// | toTokenInfo         | Projette un token du store en vue de listing              | 101   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/store"
	"github.com/Coddyum/flowlio-ia/internal/pkg/crypto"
	"github.com/google/uuid"
)

// CreateToken émet un token d'agent pour un projet de la team.
//
// Le secret en clair existe uniquement dans la valeur de retour : il n'est ni persisté, ni
// journalisé, ni réaffichable. Un token perdu se révoque et se réémet, il ne se retrouve pas.
func (s *service) CreateToken(ctx context.Context, in CreateTokenInput) (CreatedToken, error) {
	key := strings.ToUpper(strings.TrimSpace(in.ProjectKey))
	name := strings.TrimSpace(in.Name)

	if in.TeamID == uuid.Nil {
		return CreatedToken{}, ErrInvalidInput
	}
	if err := validateKey(key); err != nil {
		return CreatedToken{}, err
	}
	if err := validateName("nom de token", name); err != nil {
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

// ListTokens liste les tokens d'un projet de la team. Le secret n'existe nulle part en base :
// seuls le préfixe public et les dates sont exposés.
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

// RevokeToken révoque un token de la team. Rejouer la révocation remonte ErrNotFound : la
// query ne cible que les tokens encore actifs.
func (s *service) RevokeToken(ctx context.Context, teamID, tokenID uuid.UUID) error {
	if _, err := s.store.RevokeToken(ctx, teamID, tokenID); err != nil {
		return translateStore(err, "revoke token")
	}
	return nil
}

// toTokenInfo projette un token du store en vue de listing.
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
