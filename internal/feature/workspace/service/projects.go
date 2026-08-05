package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                 | Ligne |
// |-----------------------|--------------------------------------------------------|-------|
// | service.CreateProject | Validates then creates a project inside a team          | 24    |
// | service.ListProjects  | Lists a team's projects                                 | 47    |
// | toProject             | Projects a store project onto the API view              | 61    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// CreateProject validates the key and the name, then creates the project in the team provided.
// The key is normalised to uppercase: `frnt` and `FRNT` name the same project.
func (s *service) CreateProject(ctx context.Context, in CreateProjectInput) (Project, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	name := strings.TrimSpace(in.Name)

	if in.TeamID == uuid.Nil {
		return Project{}, ErrInvalidInput
	}
	if err := validateKey(key); err != nil {
		return Project{}, err
	}
	if err := validateName("project name", name); err != nil {
		return Project{}, err
	}

	created, err := s.store.CreateProject(ctx, in.TeamID, key, name)
	if err != nil {
		return Project{}, translateStore(err, "create project "+key)
	}
	return toProject(created), nil
}

// ListProjects lists a team's projects. This is the only cross-project view a project token can
// reach: the metadata of the sibling repos, never their content.
func (s *service) ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error) {
	rows, err := s.store.ListProjects(ctx, teamID)
	if err != nil {
		return nil, translateStore(err, "list projects")
	}

	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, toProject(row))
	}
	return projects, nil
}

// toProject projects a store project onto the API view.
func toProject(p store.Project) Project {
	return Project{ID: p.ID, Key: p.Key, Name: p.Name, CreatedAt: p.CreatedAt}
}
