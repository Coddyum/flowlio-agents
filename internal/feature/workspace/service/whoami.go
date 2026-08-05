package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | Identity       | Readable identity of a principal: team and project            | 21    |
// | service.Whoami | Turns a principal's identifiers into readable names           | 30    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/google/uuid"
)

// Identity is the whoami answer: what the agent needs to know about itself in order to work, and
// nothing more.
type Identity struct {
	TeamSlug    string `json:"team,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
	ProjectKey  string `json:"project,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

// Whoami resolves a principal's team and project. An admin token has neither: the answer is then
// empty, which is the accurate information.
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
