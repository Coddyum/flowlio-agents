package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | Identity       | Identité lisible d'un principal : team et projet              | 21    |
// | service.Whoami | Traduit des identifiants de principal en noms lisibles        | 30    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/google/uuid"
)

// Identity est la réponse de whoami : ce que l'agent doit savoir de lui-même pour travailler,
// et rien de plus.
type Identity struct {
	TeamSlug    string `json:"team,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
	ProjectKey  string `json:"project,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

// Whoami résout la team et le projet d'un principal. Un token admin n'a ni l'une ni l'autre :
// la réponse est alors vide, ce qui est l'information juste.
func (s *service) Whoami(ctx context.Context, teamID, projectID uuid.UUID) (Identity, error) {
	if teamID == uuid.Nil {
		return Identity{}, nil
	}

	team, err := s.store.TeamByID(ctx, teamID)
	if err != nil {
		return Identity{}, translateStore(err, "whoami team")
	}

	identity := Identity{TeamSlug: team.Slug, TeamName: team.Name}
	if projectID == uuid.Nil {
		return identity, nil
	}

	project, err := s.store.ProjectByID(ctx, teamID, projectID)
	if err != nil {
		return Identity{}, translateStore(err, "whoami project")
	}
	identity.ProjectKey = project.Key
	identity.ProjectName = project.Name

	return identity, nil
}
