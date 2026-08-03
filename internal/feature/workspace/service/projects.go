package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                 | Ligne |
// |-----------------------|--------------------------------------------------------|-------|
// | service.CreateProject | Valide puis crée un projet dans une team                | 24    |
// | service.ListProjects  | Liste les projets d'une team                            | 47    |
// | toProject             | Projette un projet du store en vue API                  | 61    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// CreateProject valide la clé et le nom, puis crée le projet dans la team fournie.
// La clé est normalisée en majuscules : `frnt` et `FRNT` désignent le même projet.
func (s *service) CreateProject(ctx context.Context, in CreateProjectInput) (Project, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	name := strings.TrimSpace(in.Name)

	if in.TeamID == uuid.Nil {
		return Project{}, ErrInvalidInput
	}
	if err := validateKey(key); err != nil {
		return Project{}, err
	}
	if err := validateName("nom de projet", name); err != nil {
		return Project{}, err
	}

	created, err := s.store.CreateProject(ctx, in.TeamID, key, name)
	if err != nil {
		return Project{}, translateStore(err, "create project "+key)
	}
	return toProject(created), nil
}

// ListProjects liste les projets d'une team. C'est la seule vue inter-projets accessible à un
// token de projet : les métadonnées des repos frères, jamais leur contenu.
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

// toProject projette un projet du store en vue API.
func toProject(p store.Project) Project {
	return Project{ID: p.ID, Key: p.Key, Name: p.Name, CreatedAt: p.CreatedAt}
}
