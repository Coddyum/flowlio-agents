package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | service.BlockTask  | Opens a blocking edge between two tasks of the project       | 33    |
// | alreadyReached     | Tells whether a blocker already satisfies the condition      | 123   |
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

// BlockTask opens the edge "in.Number is blocked by in.Blocker until that one reaches in.Until".
//
// Everything happens in ONE transaction, and the order of the refusals is not indifferent: each
// returns a reason the agent can act on, rather than a constraint violation it would not know how
// to read. The database remains the guarantee — the composite constraint makes a cross-repo
// dependency inexpressible (D42), the CHECK refuses self-blocking, the partial unique index
// refuses the duplicate — but a guarantee is not an error message.
//
// The blocked task moves to `blocked` only if it was not already, and the edge remembers that IT
// is what put it there. Without that trace, the release could not tell "blocked by the edge" from
// "blocked by an agent for another reason".
func (s *service) BlockTask(ctx context.Context, in BlockTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}
	if in.Number == in.Blocker {
		return Task{}, fmt.Errorf("%w: a task cannot block itself", ErrInvalidInput)
	}

	until := strings.TrimSpace(in.Until)
	if until == "" {
		until = statusDone
	}
	if !slices.Contains(releaseStatuses, until) {
		return Task{}, fmt.Errorf("%w: release condition %q (expected: %s)",
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
			return fmt.Errorf("%w: task %d is archived", ErrInvalidInput, in.Number)
		}

		blocker, err := tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Blocker)
		if err != nil {
			return translateStore(err, "block task: blocker")
		}
		if blocker.ArchivedAt != nil {
			return fmt.Errorf("%w: blocking task %d is archived and will never reach %q",
				ErrInvalidInput, in.Blocker, until)
		}

		// An edge born released would be a block that does not block: the task would move to
		// `blocked` with nothing ever journalled to bring it back out. The refusal tells the agent
		// what it has just learnt — the blocker has already moved past.
		if alreadyReached(blocker.Status, until) {
			return fmt.Errorf("%w: task %d is already %s, there is nothing to wait for",
				ErrInvalidInput, in.Blocker, blocker.Status)
		}

		edges, err := tx.ActiveEdges(ctx, in.ProjectID)
		if err != nil {
			return translateStore(err, "block task: active edges")
		}
		if wouldCycle(edges, blocked.ID, blocker.ID) {
			return fmt.Errorf("%w: task %d already depends on %d, this edge would close a cycle "+
				"and leave both of them blocked", ErrInvalidInput, in.Blocker, in.Number)
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

// alreadyReached tells whether a blocker already satisfies the requested condition.
//
// "Reaching" is monotone, as in ReleaseDependenciesOfBlocker: a `done` task has moved past
// `in_progress`. Both readings of that rule must stay in agreement, otherwise an edge refused here
// could be created elsewhere and never be able to release.
func alreadyReached(status, until string) bool {
	if status == statusDone {
		return true
	}
	return status == statusInProgress && until == statusInProgress
}
