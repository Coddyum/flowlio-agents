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
		Archive:       in.Archive,
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

	// La note s'écrit AVANT le patch, et cet ordre n'est pas indifférent : depuis que l'archivage
	// est un champ du patch, patcher d'abord archiverait la tâche, et CreateTaskNote — dont la
	// query porte `t.archived_at IS NULL` — refuserait d'écrire dans le fil d'une tâche qu'on vient
	// de fermer. L'appel le plus courant d'une fin de session, « passe en done, voilà pourquoi, et
	// archive », échouerait entièrement. Écrite d'abord, la note entre pendant que la tâche est
	// encore active, ce qui est exactement le moment où elle a un sens.
	//
	// Les deux partagent une transaction : une note refusée, ou une tâche archivée entre-temps,
	// annulent l'ensemble. Le scope reste porté par chacune des deux queries — la transaction
	// garantit l'atomicité, jamais la visibilité.
	var updated store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.AddNote(ctx, in.TeamID, in.ProjectID, in.Number, note); err != nil {
			return err
		}
		var err error
		updated, err = tx.UpdateTask(ctx, patch)
		return err
	})
	if err != nil {
		return Task{}, translateStore(err, "update task")
	}
	return toTask(updated), nil
}
