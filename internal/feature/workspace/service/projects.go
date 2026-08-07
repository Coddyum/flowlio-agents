package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                 | Ligne |
// |-----------------------|--------------------------------------------------------|-------|
// | service.CreateProject | Validates then creates a project, linked to its peers   | 32    |
// | service.ListProjects  | Lists a team's projects                                 | 55    |
// | toProject             | Projects a store project onto the API view              | 69    |
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
//
// The repo arrives CONNECTED: the same statement opens a trust edge towards every repo already in
// the team, so `create_issue` works from the newcomer to its peers and back at the first gesture.
// Before that, a fresh repo could talk to nobody and the refusal was a 404 with no cause attached.
//
// There is nothing to read here about how that happens, and that is the design: the edges are
// written by the query behind store.CreateProject, never by this service. Naming the table in Go
// would be the trust decision leaving the query — refused by scripts/check-trust-in-sql-only.sh.
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
