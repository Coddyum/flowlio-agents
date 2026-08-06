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
	// It only opens if NOTHING else needs to be written along with it — no note, no edge release,
	// no description. A transaction on that path would cost two more round trips on every edit.
	//
	// A body always takes the transaction, even one carrying no `#blocked-by` line: what the diff
	// compares it against is the STORED body, which is unknown until read. Editing a description is
	// rarer than editing a title, and the alternative — reading the task first, outside any
	// transaction — would compare against a body another write may already have replaced.
	releases := releasesOnPatch(patch)
	if in.Note == nil && !releases && in.Body == nil {
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
		// The stored description is read before anything is written to it: it is one half of the
		// `#blocked-by` diff, and the patch is about to overwrite it.
		previous := ""
		if in.Body != nil {
			current, err := tx.TaskByNumber(ctx, in.TeamID, in.ProjectID, in.Number)
			if err != nil {
				return translateStore(err, "update task: current description")
			}
			previous = current.Body
		}

		if note != "" {
			if _, err := tx.AddNote(ctx, in.TeamID, in.ProjectID, in.Number, note); err != nil {
				return translateStore(err, "update task")
			}
			// The quota is charged AFTER the note, inside the same transaction — FLWL-70, part 5.
			//
			// After, because AddNote is what proves the task exists and is still active: charging
			// first would take a write lock on the project row for every request naming a number
			// that does not exist, which is the shape a mistaken agent produces in bulk.
			//
			// Inside, because the two must fail together. A charge outside the transaction spends
			// quota on a note a later rollback erases; a note outside spends storage the counter
			// never saw. Either way the counter stops describing the thread, and a counter that
			// lies is worse than no quota at all.
			if err := tx.ChargeNoteBytes(ctx, in.TeamID, in.ProjectID, int64(len(note))); err != nil {
				return translateStore(err, "update task: note quota")
			}
		}

		var err error
		updated, err = tx.UpdateTask(ctx, patch)
		if err != nil {
			return translateStore(err, "update task")
		}

		// The description is compiled after the patch has written it: a line refused here rolls back
		// the body that carries it, so the text and the graph never disagree.
		if in.Body != nil {
			updated, err = s.syncBodyEdges(ctx, tx, bodyEdges{
				task:           updated,
				previous:       previous,
				next:           *in.Body,
				statusExplicit: in.Status != nil,
				archiving:      in.Archive,
			})
			if err != nil {
				return err
			}
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
