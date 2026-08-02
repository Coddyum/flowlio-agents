package service

import (
	"context"

	"github.com/google/uuid"
)

// maxThreadNotes borne le fil rendu par GetTask.
//
// `get CORE-34` est l'outil qu'un agent appelle pour REPRENDRE une tâche : le fil entier, c'est un
// appel qui remplit son contexte sur une lecture qu'il croyait anodine — mesuré à 62,6 Mio pour
// 1 000 notes de 64 KiB. Les dernières notes sont celles qui portent l'état ; NotesTotal dit à
// l'agent qu'il ne lit qu'une fenêtre.
//
// 10, comme maxThreadMessages côté issue : le fil d'une tâche et le fil d'une issue se lisent pour
// la même raison, deux bornes différentes n'auraient été qu'une chose de plus à retenir.
const maxThreadNotes = 10

// GetTask renvoie une tâche et la fin de son fil de notes.
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

	rows, total, err := s.store.ListNotes(ctx, teamID, projectID, number, maxThreadNotes)
	if err != nil {
		return TaskDetail{}, translateStore(err, "list notes")
	}

	notes := make([]Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, toNote(row))
	}

	return TaskDetail{Task: toTask(task), Notes: notes, NotesTotal: total}, nil
}
