package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                     | Ligne |
// |----------------------|-------------------------------------------------------------|-------|
// | bodyEdges            | The two bodies of one write, and what the patch says besides  | 48    |
// | service.syncBodyEdges| Compiles a description into edges, and releases what it drops | 67    |
// | diffRefs             | The refs of one list absent from the other                    | 160   |
// | service.dropBodyEdge | Releases the edge a removed line had opened                   | 181   |
// | service.openBodyEdge | Opens the edge a new line asks for, or refuses with a reason  | 207   |
//
// Fin du sommaire.
// =====================================================================
//
// WHAT A DESCRIPTION IS ALLOWED TO DO TO THE GRAPH, decided in D47.
//
// The line and block_task are two writing surfaces on the same object, and they do not share a
// lifecycle. block_task is an act: nothing undoes it but unblock_task. The line is text, and text
// gets rewritten, pasted over, reformatted — none of which is a decision about a dependency.
//
// So each surface owns what it opened. `origin` carries that ownership in the row, and this file
// is the only place that writes `body` into it. In practice:
//
//   - a line APPEARING in a body opens an edge, exactly as block_task would;
//   - a line DISAPPEARING releases that edge — and only if a body line had opened it;
//   - an edge opened through the API survives any description edit, whatever the text now says.
//
// Everything here works on the DIFF between the stored body and the one being written, never on
// the new body alone. That is not an optimisation, it is what keeps a released edge from being
// reopened: a line whose blocker has since moved on stays in the description as documentation, and
// re-reading it at every patch would refuse every later edit of that task — the blocker being done
// is precisely a refusal case of openBodyEdge.

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// bodyEdges carries one write's two bodies plus what the rest of the patch says.
//
// statusExplicit and archiving are not context passed around for convenience: they decide two
// refusals below. A patch that names a status has already said where the task goes, and no line
// gets to overrule it; a patch that archives the task closes it, and opening a dependency on a
// closed task is the undead state this feature exists to prevent.
type bodyEdges struct {
	task           store.Task
	previous       string
	next           string
	statusExplicit bool
	archiving      bool
}

// syncBodyEdges compiles the `#blocked-by` lines of a description into edges and releases the ones
// its removed lines had opened. It returns the task in its resulting state.
//
// It runs INSIDE the caller's transaction, and that is what makes the refusal total: a body whose
// twelfth line is unreadable rolls back the title, the note and the body itself. A description
// half-written next to an edge that was refused would be the worst of the three options this card
// weighed — the text and the graph disagreeing, with nobody told.
//
// Reading the project key costs one query, paid only when one of the two bodies carries a
// directive line: the nominal patch — a title, a priority, a body with no dependency in it — pays
// nothing.
func (s *service) syncBodyEdges(ctx context.Context, tx store.Store, in bodyEdges) (store.Task, error) {
	if len(scanBlockedByLines(in.previous)) == 0 && len(scanBlockedByLines(in.next)) == 0 {
		return in.task, nil
	}

	key, err := tx.ProjectKey(ctx, in.task.TeamID, in.task.ProjectID)
	if err != nil {
		return in.task, translateStore(err, "blocked-by: project key")
	}

	next, err := parseBlockedBy(in.next, key, in.task.Number)
	if err != nil {
		return in.task, err
	}
	previous := previousBlockedBy(in.previous, key, in.task.Number)

	added := diffRefs(next, previous)
	if len(added) > 0 && in.archiving {
		return in.task, fmt.Errorf(
			"%w: a task being archived cannot open a dependency — remove the #blocked-by line, or archive it later",
			ErrInvalidInput)
	}

	// Releases come first: a line whose release condition changed reads as one removal and one
	// addition on the same pair, and the partial unique index would refuse the second edge while
	// the first is still active.
	dropped := false
	for _, ref := range diffRefs(previous, next) {
		released, err := s.dropBodyEdge(ctx, tx, in.task, ref)
		if err != nil {
			return in.task, err
		}
		dropped = dropped || released
	}
	if len(added) == 0 {
		if !dropped {
			return in.task, nil
		}
		// Read back AFTER the release: it may have brought the task out of `blocked`, and returning
		// the state read before would have the caller see a status the database no longer holds.
		task, err := tx.TaskByNumber(ctx, in.task.TeamID, in.task.ProjectID, in.task.Number)
		if err != nil {
			return in.task, translateStore(err, "blocked-by: reread")
		}
		return task, nil
	}

	edges, err := tx.ActiveEdges(ctx, in.task.ProjectID)
	if err != nil {
		return in.task, translateStore(err, "blocked-by: active edges")
	}
	held, err := tx.TaskDependencies(ctx, in.task.ProjectID, in.task.ID)
	if err != nil {
		return in.task, translateStore(err, "blocked-by: task dependencies")
	}

	// One edge at most claims the block: the second line of a description finds the task already
	// `blocked`, and an edge claiming a transition it did not cause would bring the task back to
	// `todo` on its own release, while the other one still blocks it.
	//
	// A patch naming a status claims nothing at all: the agent has said where the task goes, and
	// D46 holds here too — the edge notifies, it does not decide.
	claims := !in.statusExplicit && in.task.Status != statusBlocked
	claimed := false
	for _, ref := range added {
		opened, err := s.openBodyEdge(ctx, tx, in.task, ref, held, &edges, claims && !claimed)
		if err != nil {
			return in.task, err
		}
		claimed = claimed || opened
	}
	if !claimed {
		return in.task, nil
	}

	status := statusBlocked
	blocked, err := tx.UpdateTask(ctx, store.TaskPatch{
		TeamID:    in.task.TeamID,
		ProjectID: in.task.ProjectID,
		Number:    in.task.Number,
		Status:    &status,
	})
	if err != nil {
		return in.task, translateStore(err, "blocked-by: set blocked")
	}
	return blocked, nil
}

// diffRefs returns the refs of `from` that `to` does not carry.
//
// The comparison covers the release condition as well as the blocker: changing `until #done` into
// `until #in_progress` is a different edge, not the same one edited — the condition is written
// once, at creation, and no query updates it afterwards.
func diffRefs(from, to []blockedByRef) []blockedByRef {
	kept := make(map[blockedByRef]bool, len(to))
	for _, ref := range to {
		kept[ref] = true
	}

	var diff []blockedByRef
	for _, ref := range from {
		if !kept[ref] {
			diff = append(diff, ref)
		}
	}
	return diff
}

// dropBodyEdge releases the edge a removed line had opened, and announces it through the SAME path
// as any other release: back to `todo` if that edge was what blocked, and a `task.unblocked`
// journalled either way.
//
// A blocker that no longer resolves is not an error: the line named a task that was never created,
// or the body predates this compiler. Nothing was opened, so there is nothing to release.
func (s *service) dropBodyEdge(ctx context.Context, tx store.Store, task store.Task, ref blockedByRef) (bool, error) {
	blocker, err := tx.TaskByNumber(ctx, task.TeamID, task.ProjectID, ref.blocker)
	if err != nil {
		// An unresolvable blocker opened no edge: there is nothing to release, and refusing here
		// would make an already-written description impossible to clean up.
		return false, nil //nolint:nilerr
	}

	freed, err := tx.ReleaseBodyEdge(ctx, task.ProjectID, task.ID, blocker.ID)
	if err != nil {
		return false, translateStore(err, "blocked-by: release edge")
	}
	if len(freed) == 0 {
		return false, nil
	}
	return true, s.announceFreed(ctx, tx, task.TeamID, task.ProjectID, freed)
}

// openBodyEdge opens the edge a new line asks for, and returns whether it is the one that blocked
// the task. Every refusal names the line's own vocabulary, so that what has to be fixed is the
// text the human just wrote.
//
// An edge already active on the pair is NOT a conflict: the line is then documenting what
// block_task opened, which is the point of writing it down. It is only refused when the two
// disagree on the release condition — the description would claim a condition the database does
// not hold, and silently believing either one is how a text and a graph drift apart.
func (s *service) openBodyEdge(
	ctx context.Context,
	tx store.Store,
	task store.Task,
	ref blockedByRef,
	held []store.Dependency,
	edges *[]store.Edge,
	blocks bool,
) (bool, error) {
	blocker, err := tx.TaskByNumber(ctx, task.TeamID, task.ProjectID, ref.blocker)
	if err != nil {
		return false, translateStore(err, fmt.Sprintf("blocked-by: task %d", ref.blocker))
	}
	if blocker.ArchivedAt != nil {
		return false, fmt.Errorf("%w: task %d is archived and will never reach %q",
			ErrInvalidInput, ref.blocker, ref.until)
	}

	for _, dep := range held {
		if dep.BlockerTaskID != blocker.ID {
			continue
		}
		if dep.UntilStatus == ref.until {
			return false, nil
		}
		return false, fmt.Errorf(
			"%w: task %d is already blocked by %d until %q, and the line says %q — release it with unblock_task before writing another condition",
			ErrConflict, task.Number, ref.blocker, dep.UntilStatus, ref.until)
	}

	if alreadyReached(blocker.Status, ref.until) {
		return false, fmt.Errorf("%w: task %d is already %s, there is nothing to wait for",
			ErrInvalidInput, ref.blocker, blocker.Status)
	}
	if wouldCycle(*edges, task.ID, blocker.ID) {
		return false, fmt.Errorf("%w: task %d already depends on %d, this edge would close a cycle "+
			"and leave both of them blocked", ErrInvalidInput, ref.blocker, task.Number)
	}

	if _, err := tx.CreateDependency(ctx, store.NewDependency{
		TeamID:        task.TeamID,
		ProjectID:     task.ProjectID,
		TaskID:        task.ID,
		BlockerTaskID: blocker.ID,
		UntilStatus:   ref.until,
		SetBlocked:    blocks,
		Origin:        store.OriginBody,
	}); err != nil {
		return false, translateStore(err, "blocked-by: create dependency")
	}

	*edges = append(*edges, store.Edge{TaskID: task.ID, BlockerTaskID: blocker.ID})
	return blocks, nil
}
