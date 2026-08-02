package service

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
)

// UpdateTask applique un patch partiel : un champ absent laisse la valeur en place.
//
// Une tâche archivée n'est pas modifiable et remonte ErrNotFound, exactement comme un numéro
// inexistant ou une tâche d'un autre projet. Cette indistinction est délibérée : distinguer
// « existe mais archivée » de « n'existe pas » dirait à un agent quels numéros sont utilisés
// dans un projet auquel il n'a pas accès.
func (s *service) UpdateTask(ctx context.Context, in UpdateTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}

	patch := store.TaskPatch{
		TeamID:        in.TeamID,
		ProjectID:     in.ProjectID,
		Number:        in.Number,
		Deadline:      in.Deadline,
		ClearDeadline: in.ClearDeadline,
	}

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if err := validateTitle(title); err != nil {
			return Task{}, err
		}
		patch.Title = &title
	}

	if in.Body != nil {
		if err := validateBody("description", *in.Body); err != nil {
			return Task{}, err
		}
		patch.Body = in.Body
	}

	if in.Status != nil {
		if err := validateStatus(*in.Status); err != nil {
			return Task{}, err
		}
		patch.Status = in.Status
	}

	if in.Priority != nil {
		if err := validatePriority(*in.Priority); err != nil {
			return Task{}, err
		}
		patch.Priority = in.Priority
	}

	updated, err := s.store.UpdateTask(ctx, patch)
	if err != nil {
		return Task{}, translateStore(err, "update task")
	}
	return toTask(updated), nil
}
