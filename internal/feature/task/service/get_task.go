package service

import (
	"context"

	"github.com/google/uuid"
)

// GetTask renvoie une tâche et son fil de notes.
//
// Les deux lectures sont scopées indépendamment par la même paire team + projet : la seconde ne
// fait pas confiance au résultat de la première. Elles ne sont pas dans une transaction — une
// note ajoutée entre les deux apparaît, ce qui est le bon comportement pour une lecture.
func (s *service) GetTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (TaskDetail, error) {
	if err := validateScope(teamID, projectID); err != nil {
		return TaskDetail{}, err
	}

	task, err := s.store.TaskByNumber(ctx, teamID, projectID, number)
	if err != nil {
		return TaskDetail{}, translateStore(err, "task by number")
	}

	rows, err := s.store.ListNotes(ctx, teamID, projectID, number)
	if err != nil {
		return TaskDetail{}, translateStore(err, "list notes")
	}

	notes := make([]Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, toNote(row))
	}

	return TaskDetail{Task: toTask(task), Notes: notes}, nil
}
