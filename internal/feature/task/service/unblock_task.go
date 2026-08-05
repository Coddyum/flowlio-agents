package service

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// UnblockTask releases, by hand, the edge between in.Number and in.Blocker, without waiting for
// the blocker to move on.
//
// Going back to `todo` and the `task.unblocked` both go through the SAME path as an automatic
// release: two paths would have drifted apart eventually, and that very drift is what would leave
// a task blocked forever after a manual unblocking.
//
// A missing or already-released edge is not an error: `unblock_task` replayed returns the task in
// its current state. Refusing would have failed a session resume on an action already carried out
// — an agent replaying is not at fault, it has lost its context.
func (s *service) UnblockTask(ctx context.Context, in UnblockTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}
	if in.Number == in.Blocker {
		return Task{}, fmt.Errorf("%w: a task cannot block itself", ErrInvalidInput)
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

		// Read back AFTER the release: announceFreed may have brought the task back to `todo`, and
		// returning the prior state would have the agent read a `blocked` the database no longer holds.
		blocked, err = tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Number)
		return translateStore(err, "unblock task: reread")
	})
	if err != nil {
		return Task{}, err
	}
	return toTask(blocked), nil
}
