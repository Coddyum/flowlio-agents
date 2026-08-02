package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
)

// UpdateTask applique un patch partiel : un champ absent laisse la valeur en place.
//
// Une tâche archivée n'est pas modifiable et remonte ErrNotFound, exactement comme un numéro
// inexistant ou une tâche d'un autre projet. Cette indistinction est délibérée : distinguer
// « existe mais archivée » de « n'existe pas » dirait à un agent quels numéros sont utilisés
// dans un projet auquel il n'a pas accès.
//
// Une note fournie est écrite dans la MÊME transaction que le patch. Séparées, les deux
// écritures laissaient exister l'état « statut changé, motif perdu » : la note tombe alors que
// le statut est déjà passé, et la session suivante lit un done que rien n'explique.
func (s *service) UpdateTask(ctx context.Context, in UpdateTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}
	if err := validateDeadline(in.Deadline); err != nil {
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

	if in.Note == nil {
		updated, err := s.store.UpdateTask(ctx, patch)
		if err != nil {
			return Task{}, translateStore(err, "update task")
		}
		return toTask(updated), nil
	}

	note := strings.TrimSpace(*in.Note)
	if note == "" {
		return Task{}, fmt.Errorf("%w: note vide", ErrInvalidInput)
	}
	if err := validateBody("note", note); err != nil {
		return Task{}, err
	}

	// Le patch et la note partagent une transaction : la tâche archivée entre les deux, ou une
	// note refusée, annulent l'ensemble. Le scope reste porté par chacune des deux queries — la
	// transaction garantit l'atomicité, jamais la visibilité.
	var updated store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		if updated, err = tx.UpdateTask(ctx, patch); err != nil {
			return err
		}
		_, err = tx.AddNote(ctx, in.TeamID, in.ProjectID, in.Number, note)
		return err
	})
	if err != nil {
		return Task{}, translateStore(err, "update task")
	}
	return toTask(updated), nil
}
