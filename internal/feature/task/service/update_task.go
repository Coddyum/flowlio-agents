package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// UpdateTask applies a partial patch: an absent field leaves the value in place.
//
// An archived task cannot be modified and returns ErrNotFound, exactly like a non-existent number
// or a task of another project. That indistinction is deliberate: telling "exists but archived"
// apart from "does not exist" would tell an agent which numbers are in use in a project it has no
// access to.
//
// A note, when provided, is written in the SAME transaction as the patch. Kept apart, the two
// writes allowed the state "status changed, reason lost" to exist: the note fails once the status
// has already moved, and the next session reads a done that nothing explains.
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

	// The transaction-free path is the one of the nominal patch: a title, a priority, a deadline.
	// It only opens if NOTHING else needs to be written along with it — no note, no edge release. A
	// transaction on that path would cost two more round trips on every edit.
	releases := releasesOnPatch(patch)
	if in.Note == nil && !releases {
		updated, err := s.store.UpdateTask(ctx, patch)
		if err != nil {
			return Task{}, translateStore(err, "update task")
		}
		return toTask(updated), nil
	}

	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
		if note == "" {
			return Task{}, fmt.Errorf("%w: empty note", ErrInvalidInput)
		}
		if err := validateBody("note", note); err != nil {
			return Task{}, err
		}
	}

	// The note is written BEFORE the patch, and that order is not indifferent: ever since archiving
	// became a field of the patch, patching first would archive the task, and CreateTaskNote —
	// whose query carries `t.archived_at IS NULL` — would refuse to write into the thread of a task
	// just closed. The most common end-of-session call, "move to done, here is why, and archive",
	// would fail entirely. Written first, the note lands while the task is still active, which is
	// exactly the moment it makes sense.
	//
	// Both share a transaction: a rejected note, or a task archived in the meantime, rolls back the
	// whole thing. The scope stays carried by each of the two queries — the transaction guarantees
	// atomicity, never visibility.
	var updated store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		if note != "" {
			if _, err := tx.AddNote(ctx, in.TeamID, in.ProjectID, in.Number, note); err != nil {
				return translateStore(err, "update task")
			}
		}

		var err error
		updated, err = tx.UpdateTask(ctx, patch)
		if err != nil {
			return translateStore(err, "update task")
		}
		if !releases {
			return nil
		}

		// The release follows the patch in the SAME transaction, and that is what makes the state
		// "the blocker is done, the blocked task ignores it" impossible. An archived task forces the
		// release: it will never reach anything, and leaving its edges in place would manufacture
		// tasks nothing can ever unblock.
		return s.releaseBlocker(ctx, tx, updated, patch.Archive)
	})
	if err != nil {
		return Task{}, err
	}
	return toTask(updated), nil
}
