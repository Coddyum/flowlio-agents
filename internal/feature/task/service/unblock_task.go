package service

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// UnblockTask libère à la main l'arête entre in.Number et in.Blocker, sans attendre que la
// bloquante avance.
//
// Le retour à `todo` et le `task.unblocked` passent par le MÊME chemin que la libération
// automatique : deux chemins auraient fini par diverger, et c'est exactement la divergence qui
// laisserait une tâche bloquée pour toujours après un déblocage manuel.
//
// Une arête absente ou déjà libérée n'est pas une erreur : `unblock_task` rejoué rend la tâche
// dans son état courant. Refuser aurait fait échouer une reprise de session sur une action déjà
// faite — un agent qui rejoue ne fait pas une faute, il a perdu son contexte.
func (s *service) UnblockTask(ctx context.Context, in UnblockTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}
	if in.Number == in.Blocker {
		return Task{}, fmt.Errorf("%w: une tâche ne peut pas se bloquer elle-même", ErrInvalidInput)
	}

	var blocked store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		var err error
		blocked, err = tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Number)
		if err != nil {
			return translateStore(err, "unblock task: blocked task")
		}
		blocker, err := tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Blocker)
		if err != nil {
			return translateStore(err, "unblock task: blocker")
		}

		freed, err := tx.ReleaseEdge(ctx, in.ProjectID, blocked.ID, blocker.ID)
		if err != nil {
			return translateStore(err, "unblock task: release edge")
		}
		if len(freed) == 0 {
			return nil
		}
		if err := s.announceFreed(ctx, tx, in.TeamID, in.ProjectID, freed); err != nil {
			return err
		}

		// Relu APRÈS la libération : announceFreed a pu ramener la tâche à `todo`, et rendre l'état
		// d'avant ferait lire à l'agent un `blocked` que la base ne porte plus.
		blocked, err = tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Number)
		return translateStore(err, "unblock task: reread")
	})
	if err != nil {
		return Task{}, err
	}
	return toTask(blocked), nil
}
