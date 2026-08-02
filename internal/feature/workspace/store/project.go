package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                 | Ligne |
// |---------------------|--------------------------------------------------------|-------|
// | store.CreateProject | Insère un projet dans une team                          | 24    |
// | store.ProjectByID   | Lit un projet par son identifiant, scopé par la team    | 37    |
// | store.ProjectByKey  | Lit un projet par sa clé, dans le scope de la team      | 50    |
// | store.ListProjects  | Liste les projets d'une team                            | 62    |
// | toProject           | Projette une ligne sqlc en projet domaine               | 76    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// CreateProject insère un projet. Une clé déjà prise dans la team remonte en ErrConflict.
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

// ProjectByID lit un projet par son identifiant, toujours scopé par la team.
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

// ProjectByKey lit un projet par sa clé. Le team_id fait partie de la query : une clé d'une
// autre team est introuvable, pas seulement interdite.
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

// ListProjects liste les projets d'une team, triés par clé.
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

// toProject projette une ligne générée en type domaine.
func toProject(row database.Project) Project {
	return Project{
		ID:        row.ID,
		TeamID:    row.TeamID,
		Key:       row.Key,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}
}
