package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                 | Ligne |
// |---------------------|--------------------------------------------------------|-------|
// | store.CreateProject | Inserts a project into a team, edges to its peers included | 34  |
// | store.ProjectByID   | Reads a project by its identifier, scoped by the team   | 53    |
// | store.ProjectByKey  | Reads a project by its key, within the team scope       | 66    |
// | store.ListProjects  | Lists a team's projects                                 | 78    |
// | store.DeleteProject | Removes a project unless a sibling holds a thread with it  | 100 |
// | toProject           | Projects an sqlc row onto the domain project            | 126   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateProject inserts a project and, in the SAME statement, opens the repo's trust edges towards
// every repo already in the team. A key already taken inside the team yields ErrConflict.
//
// The store neither computes nor names those edges: what happens is entirely in the query, and this
// method could not tell how many rows it wrote. That placement is the rule, enforced by
// scripts/check-trust-in-sql-only.sh.
//
// The generated call returns its own row type, not database.Project: the query now selects from a
// CTE, which sqlc does not recognise as the projects table. The fields are the same ones, so the
// mapping is written here rather than routed through toProject, which keeps its single input type.
func (s *store) CreateProject(ctx context.Context, teamID uuid.UUID, key, name string) (Project, error) {
	row, err := s.q.CreateProject(ctx, database.CreateProjectParams{
		TeamID: teamID,
		Key:    key,
		Name:   name,
	})
	if err != nil {
		return Project{}, translate(err, "create project")
	}
	return Project{
		ID:        row.ID,
		TeamID:    row.TeamID,
		Key:       row.Key,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}, nil
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

// DeleteProject removes a project, and refuses while a sibling repo holds a thread with it. The
// whole decision is the query's: this method reads the outcome, it does not compute it.
//
// ZERO ROWS MEANS "NO SUCH PROJECT IN THIS TEAM", and only that. The query returns its target row
// whether the deletion happened or was refused, so an empty result can carry no other meaning —
// which is what lets a missing project answer 404 and a refused one answer with its reason.
//
// A row WITHOUT a sibling key is the deletion itself; the rows WITH one are the refusal. They never
// come back together: both are read from the same relation inside the query.
func (s *store) DeleteProject(ctx context.Context, teamID, projectID uuid.UUID) (ProjectDeletion, error) {
	rows, err := s.q.DeleteProject(ctx, database.DeleteProjectParams{
		ProjectID: projectID,
		TeamID:    teamID,
	})
	if err != nil {
		return ProjectDeletion{}, translate(err, "delete project")
	}
	if len(rows) == 0 {
		return ProjectDeletion{}, ErrNotFound
	}

	outcome := ProjectDeletion{Deleted: rows[0].Deleted}
	for _, row := range rows {
		if !row.SiblingKey.Valid {
			continue
		}
		outcome.Blockers = append(outcome.Blockers, Blocker{
			Key:     row.SiblingKey.String,
			Threads: row.Threads.Int64,
		})
	}
	return outcome, nil
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
