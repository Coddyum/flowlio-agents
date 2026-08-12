package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                    | Ligne |
// |------------------------|-----------------------------------------------------------|-------|
// | releasesOnPatch        | Tells whether a patch can release edges                     | 50    |
// | service.releaseBlocker | Releases what a task frees by moving on, and notifies       | 66    |
// | service.announceFreed  | Brings back to `todo` what can be, and journals it          | 80    |
//
// Fin du sommaire.
// =====================================================================
//
// THE HEART OF THE FEATURE, and the point where it stops being a decorative link.
//
// Three things happen when a blocker moves on, in this order, and ALWAYS inside the transaction of
// the write that triggers them:
//
//  1. the edges it satisfies are marked released;
//  2. each task thus freed moves back to `todo` — but ONLY if all of its edges are released AND at
//     least one of them had blocked it. That rule lives in the ClearTaskBlock query, so that no
//     caller can forget a branch of it;
//  3. a `task.unblocked` is journalled, subject = the UNBLOCKED task, not the blocker. That is what
//     check_inbox will hand back to the project.
//
// Step 3 happens even when step 2 changed nothing: a task the agent had blocked itself for another
// reason must learn its obstacle is lifted, without anyone deciding its status on its behalf.
// Notifying and deciding are two distinct gestures, and only one of them is automated.
//
// Outside a transaction, the defect would be exactly the one this card exists to remove: the
// blocker committed `done`, and the blocked task ignoring it forever.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// eventTaskUnblocked is the journalled `kind`. The `domain.fact` shape is imposed by a CHECK
// constraint on the events table.
const eventTaskUnblocked = "task.unblocked"

// releasesOnPatch tells whether a patch is liable to release edges, and hence whether it has to go
// through a transaction.
//
// The point is NOT to pay for a transaction on the nominal patch — changing a title, a priority.
// `todo` and `blocked` release nothing: they are not progress, and an edge cannot wait for them
// (task_dependencies_until_is_progress constraint).
func releasesOnPatch(patch store.TaskPatch) bool {
	if patch.Archive {
		return true
	}
	if patch.Status == nil {
		return false
	}
	return *patch.Status == statusInProgress || *patch.Status == statusDone
}

// releaseBlocker releases the edges the blocker has just satisfied, then announces the outcome.
//
// force comes from archiving: an archived blocker will never reach its release status, so its
// edges are released whatever their condition. Without it, archiving a blocker would manufacture
// tasks nothing can ever unblock — undead ones, and the only defect of this design no later call
// would make up for.
func (s *service) releaseBlocker(ctx context.Context, tx store.Store, blocker store.Task, force bool) error {
	freed, err := tx.ReleaseBlockerEdges(ctx, blocker.ProjectID, blocker.ID, blocker.Status, force)
	if err != nil {
		return translateStore(err, "release blocker edges")
	}
	return s.announceFreed(ctx, tx, blocker.TeamID, blocker.ProjectID, freed)
}

// announceFreed brings back to `todo` what can be, and journals one `task.unblocked` per freed
// task.
//
// The deduplication is not caution: one blocker can carry TWO edges towards the same blocked task
// — one per `until_status` — and release them together. Without the set of seen tasks, that task
// would receive two events for a single unblocking, and the inbox would show it twice.
func (s *service) announceFreed(ctx context.Context, tx store.Store, teamID, projectID uuid.UUID, freed []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(freed))
	for _, taskID := range freed {
		if seen[taskID] {
			continue
		}
		seen[taskID] = true

		if _, err := tx.ClearBlock(ctx, teamID, projectID, taskID); err != nil {
			return translateStore(err, "clear block")
		}

		// The actor is the project itself: a dependency never crosses a repo (D42), so no case
		// exists where a third party is the author of the unblocking.
		if err := tx.AppendEvent(ctx, store.Event{
			TeamID:          teamID,
			ProjectID:       projectID,
			ActorProjectID:  projectID,
			NotifyProjectID: projectID, // the unblocked task lives in this same repo (D42): wake it
			Kind:            eventTaskUnblocked,
			SubjectID:       taskID,
		}); err != nil {
			return translateStore(err, "append unblocked event")
		}
	}
	return nil
}
