package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                 | Ligne |
// |---------------------|--------------------------------------------------------|-------|
// | store.CreateProject | Inserts a project into a team                           | 24    |
// | store.ProjectByID   | Reads a project by its identifier, scoped by the team   | 37    |
// | store.ProjectByKey  | Reads a project by its key, within the team scope       | 50    |
// | store.ListProjects  | Lists a team's projects                                 | 62    |
// | toProject           | Projects an sqlc row onto the domain project            | 76    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateProject inserts a project. A key already taken inside the team yields ErrConflict.
func (s *store) CreateProject(ctx context.Context, teamID uuid.UUID, key, name string) (Project, error) {
	row, err := s.q.CreateProject(ctx, database.CreateProjectParams{
		TeamID: teamID,
		Key:    key,
		Name:   name,
	})
	if err != nil {
		return Project{}, translate(err, "create project")
	}
	return toProject(row), nil
}

// ProjectByID reads a project by its identifier, always scoped by the team.
func (s *store) ProjectByID(ctx context.Context, teamID, id uuid.UUID) (Project, error) {
	row, err := s.q.GetProjectByID(ctx, database.GetProjectByIDParams{
		ID:     id,
		TeamID: teamID,
	})
	if err != nil {
		return Project{}, translate(err, "project by id")
	}
	return toProject(row), nil
}

// ProjectByKey reads a project by its key. The team_id is part of the query: a key from another
// team is not found, not merely forbidden.
func (s *store) ProjectByKey(ctx context.Context, teamID uuid.UUID, key string) (Project, error) {
	row, err := s.q.GetProjectByKey(ctx, database.GetProjectByKeyParams{
		TeamID: teamID,
		Key:    key,
	})
	if err != nil {
		return Project{}, translate(err, "project by key")
	}
	return toProject(row), nil
}

// ListProjects lists a team's projects, sorted by key.
func (s *store) ListProjects(ctx context.Context, teamID uuid.UUID) ([]Project, error) {
	rows, err := s.q.ListProjectsByTeam(ctx, teamID)
	if err != nil {
		return nil, translate(err, "list projects")
	}

	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, toProject(row))
	}
	return projects, nil
}

// toProject projects a generated row onto the domain type.
func toProject(row database.Project) Project {
	return Project{
		ID:        row.ID,
		TeamID:    row.TeamID,
		Key:       row.Key,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}
}
