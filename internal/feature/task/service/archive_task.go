package service

import (
	"context"

	"github.com/google/uuid"
)

// ArchiveTask sort une tâche du backlog actif sans la supprimer : ses notes restent lisibles,
// c'est la trace de ce qui a été fait dans le repo.
//
// Rejouer l'archivage remonte ErrNotFound : la query ne cible que les tâches encore actives.
func (s *service) ArchiveTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (Task, error) {
	if err := validateScope(teamID, projectID); err != nil {
		return Task{}, err
	}

	archived, err := s.store.ArchiveTask(ctx, teamID, projectID, number)
	if err != nil {
		return Task{}, translateStore(err, "archive task")
	}
	return toTask(archived), nil
}
