package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | service.BlockTask  | Ouvre une arête de blocage entre deux tâches du projet       | 34    |
// | alreadyReached     | Dit si une bloquante satisfait déjà la condition demandée    | 124   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// BlockTask ouvre l'arête « in.Number est bloquée par in.Blocker jusqu'à ce que celle-ci atteigne
// in.Until ».
//
// Tout se passe dans UNE transaction, et l'ordre des refus n'est pas indifférent : chacun rend un
// motif que l'agent peut corriger, plutôt qu'une violation de contrainte qu'il ne saurait pas
// lire. La base reste la garantie — la contrainte composite rend la dépendance inter-repos
// inexprimable (D42), le CHECK refuse l'auto-blocage, l'index unique partiel refuse le doublon —
// mais une garantie n'est pas un message d'erreur.
//
// La tâche bloquée passe `blocked` seulement si elle ne l'était pas déjà, et l'arête retient que
// c'est ELLE qui l'y a mise. Sans cette trace, la libération ne saurait pas distinguer « bloquée
// par l'arête » de « bloquée par un agent pour une autre raison ».
func (s *service) BlockTask(ctx context.Context, in BlockTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}
	if in.Number == in.Blocker {
		return Task{}, fmt.Errorf("%w: une tâche ne peut pas se bloquer elle-même", ErrInvalidInput)
	}

	until := strings.TrimSpace(in.Until)
	if until == "" {
		until = statusDone
	}
	if !slices.Contains(releaseStatuses, until) {
		return Task{}, fmt.Errorf("%w: condition de libération %q (attendu: %s)",
			ErrInvalidInput, until, strings.Join(releaseStatuses, ", "))
	}

	var blocked store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		blocked, err = tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Number)
		if err != nil {
			return translateStore(err, "block task: blocked task")
		}
		if blocked.ArchivedAt != nil {
			return fmt.Errorf("%w: la tâche %d est archivée", ErrInvalidInput, in.Number)
		}

		blocker, err := tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Blocker)
		if err != nil {
			return translateStore(err, "block task: blocker")
		}
		if blocker.ArchivedAt != nil {
			return fmt.Errorf("%w: la tâche bloquante %d est archivée et n'atteindra jamais %q",
				ErrInvalidInput, in.Blocker, until)
		}

		// Une arête née libérée serait un blocage qui ne bloque pas : la tâche passerait `blocked`
		// sans que rien ne soit jamais journalisé pour l'en sortir. Le refus dit à l'agent ce qu'il
		// vient d'apprendre — la bloquante est déjà passée.
		if alreadyReached(blocker.Status, until) {
			return fmt.Errorf("%w: la tâche %d est déjà %s, il n'y a rien à attendre",
				ErrInvalidInput, in.Blocker, blocker.Status)
		}

		edges, err := tx.ActiveEdges(ctx, in.ProjectID)
		if err != nil {
			return translateStore(err, "block task: active edges")
		}
		if wouldCycle(edges, blocked.ID, blocker.ID) {
			return fmt.Errorf("%w: la tâche %d dépend déjà de %d, cette arête fermerait un cycle "+
				"et les laisserait bloquées toutes les deux", ErrInvalidInput, in.Blocker, in.Number)
		}

		setBlocked := blocked.Status != statusBlocked
		if _, err := tx.CreateDependency(ctx, store.NewDependency{
			TeamID:        in.TeamID,
			ProjectID:     in.ProjectID,
			TaskID:        blocked.ID,
			BlockerTaskID: blocker.ID,
			UntilStatus:   until,
			SetBlocked:    setBlocked,
		}); err != nil {
			return translateStore(err, "block task: create dependency")
		}

		if !setBlocked {
			return nil
		}

		status := statusBlocked
		blocked, err = tx.UpdateTask(ctx, store.TaskPatch{
			TeamID:    in.TeamID,
			ProjectID: in.ProjectID,
			Number:    in.Number,
			Status:    &status,
		})
		return translateStore(err, "block task: set blocked")
	})
	if err != nil {
		return Task{}, err
	}
	return toTask(blocked), nil
}

// alreadyReached dit si une bloquante satisfait déjà la condition demandée.
//
// « Atteindre » est monotone, comme dans ReleaseDependenciesOfBlocker : une tâche `done` a dépassé
// `in_progress`. Les deux lectures de cette règle doivent rester d'accord, sinon une arête refusée
// ici pourrait être créée ailleurs sans jamais pouvoir se libérer.
func alreadyReached(status, until string) bool {
	if status == statusDone {
		return true
	}
	return status == statusInProgress && until == statusInProgress
}
