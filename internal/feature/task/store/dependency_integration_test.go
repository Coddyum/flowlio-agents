package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// THE intra-repo guarantee, proven where it lives: in the database.
//
// This test calls the store DIRECTLY, bypassing the service. That is the whole point — the card
// demands an edge towards another project be refused "in the database, not only in the service"
// (D42), and a guard that exists only in the service falls at the first write path added next to it.
//
// What refuses is not a predicate but the SHAPE of the table: both composite foreign keys of
// task_dependencies share the same project_id column. The cross-repo dependency is not forbidden,
// it is inexpressible.
func TestDependencyCannotCrossProjects(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	mine := newProject(t, db, "OWN")
	sibling := newProjectIn(t, db, mine.teamID, "SIB")

	blocked := createTask(t, st, mine, "what I have to do")
	foreign := createTask(t, st, sibling, "what the sibling has to do")

	_, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID:        mine.teamID,
		ProjectID:     mine.projectID,
		TaskID:        blocked.ID,
		BlockerTaskID: foreign.ID,
		UntilStatus:   "done",
		SetBlocked:    true,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict: the database must refuse a cross-project edge", err)
	}

	// And nothing was written: a refusal leaving a row behind would be worse than no refusal at all.
	var count int
	if err := db.QueryRow(
		"SELECT count(*) FROM task_dependencies WHERE task_id = $1", blocked.ID,
	).Scan(&count); err != nil {
		t.Fatalf("counting the edges: %v", err)
	}
	if count != 0 {
		t.Errorf("%d edge(s) written despite the refusal", count)
	}
}

// A task cannot block itself, and it is the CHECK that says so. The service makes the reason
// readable; the constraint is what holds should another write path appear one day.
func TestDependencyCannotBeSelfReferential(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "SELF")
	task := createTask(t, st, sc, "myself")

	_, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID:        sc.teamID,
		ProjectID:     sc.projectID,
		TaskID:        task.ID,
		BlockerTaskID: task.ID,
		UntilStatus:   "done",
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict on self-blocking", err)
	}
}

// An ACTIVE edge is unique per pair: replaying block_task does not manufacture a second block to
// release. Once released, however, the same pair becomes openable again — the uniqueness is partial
// for that exact reason, otherwise unblocking then reblocking would be refused forever.
func TestDependencyPairIsUniqueWhileActiveOnly(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "PAIR")
	blocked := createTask(t, st, sc, "blocked")
	blocker := createTask(t, st, sc, "blocker")

	edge := store.NewDependency{
		TeamID:        sc.teamID,
		ProjectID:     sc.projectID,
		TaskID:        blocked.ID,
		BlockerTaskID: blocker.ID,
		UntilStatus:   "done",
		SetBlocked:    true,
	}

	if _, err := st.CreateDependency(ctx, edge); err != nil {
		t.Fatalf("first edge: %v", err)
	}
	if _, err := st.CreateDependency(ctx, edge); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict on the active duplicate", err)
	}

	if _, err := st.ReleaseEdge(ctx, sc.projectID, blocked.ID, blocker.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := st.CreateDependency(ctx, edge); err != nil {
		t.Fatalf("reopening after release: %v — the uniqueness must be partial", err)
	}
}

// The rule for going back to `todo`, proven where it lives: in the ClearTaskBlock query. All three
// conditions hold together in there so that no caller can forget one, which is why they have to be
// checked here — not in an in-memory double, which would prove the reimplementation.
func TestClearBlockObeysItsThreeConditions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// setBlocked tells whether the edge is the one that set the block.
		setBlocked bool
		// pendingEdge adds a second, unreleased edge.
		pendingEdge bool
		// status is the blocked task's status at release time.
		status string
		want   bool
	}{
		{
			name:       "the edge had blocked, nothing else remains: back to todo",
			setBlocked: true,
			status:     "blocked",
			want:       true,
		},
		{
			// The case set_blocked exists to tell apart. Without it, we would overwrite a human
			// decision with a deduction.
			name:       "the agent had blocked for another reason: the status does not move",
			setBlocked: false,
			status:     "blocked",
			want:       false,
		},
		{
			name:        "another edge still blocks: nothing moves",
			setBlocked:  true,
			pendingEdge: true,
			status:      "blocked",
			want:        false,
		},
		{
			// An agent that already picked the task back up by hand must not be sent back to `todo`
			// by a release arriving after the fact.
			name:       "the task is no longer blocked: we do not drag it backwards",
			setBlocked: true,
			status:     "in_progress",
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, db := newStore(t)
			sc := newProject(t, db, "CLR")

			blocked := createTask(t, st, sc, "blocked")
			blocker := createTask(t, st, sc, "blocker")

			if _, err := st.CreateDependency(ctx, store.NewDependency{
				TeamID: sc.teamID, ProjectID: sc.projectID,
				TaskID: blocked.ID, BlockerTaskID: blocker.ID,
				UntilStatus: "done", SetBlocked: tc.setBlocked,
			}); err != nil {
				t.Fatalf("main edge: %v", err)
			}

			if tc.pendingEdge {
				other := createTask(t, st, sc, "other blocker")
				if _, err := st.CreateDependency(ctx, store.NewDependency{
					TeamID: sc.teamID, ProjectID: sc.projectID,
					TaskID: blocked.ID, BlockerTaskID: other.ID,
					UntilStatus: "done", SetBlocked: false,
				}); err != nil {
					t.Fatalf("secondary edge: %v", err)
				}
			}

			status := tc.status
			if _, err := st.UpdateTask(ctx, store.TaskPatch{
				TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Status: &status,
			}); err != nil {
				t.Fatalf("initial status of the blocked task: %v", err)
			}

			// The blocker reaches `done`, which releases the main edge.
			done := "done"
			if _, err := st.UpdateTask(ctx, store.TaskPatch{
				TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
			}); err != nil {
				t.Fatalf("blocker to done: %v", err)
			}
			freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "done", false)
			if err != nil {
				t.Fatalf("release: %v", err)
			}
			if len(freed) != 1 {
				t.Fatalf("%d edge(s) released, want 1", len(freed))
			}

			cleared, err := st.ClearBlock(ctx, sc.teamID, sc.projectID, blocked.ID)
			if err != nil {
				t.Fatalf("ClearBlock: %v", err)
			}
			if cleared != tc.want {
				t.Fatalf("back to todo = %v, want %v", cleared, tc.want)
			}

			after, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, blocked.Number)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			wantStatus := tc.status
			if tc.want {
				wantStatus = "todo"
			}
			if after.Status != wantStatus {
				t.Errorf("status = %q, want %q", after.Status, wantStatus)
			}
		})
	}
}

// "Reaching" a status is monotone rather than an equality: a blocker jumping from `todo` to `done`
// must also release the edges that were only waiting for `in_progress`.
//
// Strict equality is the natural trap of this query, and it would manufacture edges nothing can
// release any more — the undead task this feature exists to prevent.
func TestReleaseIsMonotone(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "MONO")
	waitsStart := createTask(t, st, sc, "waiting for the start")
	waitsEnd := createTask(t, st, sc, "waiting for the end")
	blocker := createTask(t, st, sc, "blocker")

	for _, edge := range []struct {
		task  store.Task
		until string
	}{
		{waitsStart, "in_progress"},
		{waitsEnd, "done"},
	} {
		if _, err := st.CreateDependency(ctx, store.NewDependency{
			TeamID: sc.teamID, ProjectID: sc.projectID,
			TaskID: edge.task.ID, BlockerTaskID: blocker.ID,
			UntilStatus: edge.until, SetBlocked: true,
		}); err != nil {
			t.Fatalf("edge %s: %v", edge.until, err)
		}
	}

	// The blocker moves straight to `done`, without ever having been `in_progress`.
	freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "done", false)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(freed) != 2 {
		t.Fatalf("%d edge(s) released, want 2: `done` has moved past `in_progress`", len(freed))
	}
}

// The converse, which bounds the previous rule: reaching `in_progress` does NOT release what was
// waiting for `done`. Without that bound, "blocked until it is finished" would mean "until it is
// started", and the feature's promise would be false in the most common case.
func TestReleaseDoesNotOvershoot(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	sc := newProject(t, db, "OVER")
	blocked := createTask(t, st, sc, "waiting for the end")
	blocker := createTask(t, st, sc, "blocker")

	if _, err := st.CreateDependency(ctx, store.NewDependency{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		TaskID: blocked.ID, BlockerTaskID: blocker.ID,
		UntilStatus: "done", SetBlocked: true,
	}); err != nil {
		t.Fatalf("edge: %v", err)
	}

	freed, err := st.ReleaseBlockerEdges(ctx, sc.projectID, blocker.ID, "in_progress", false)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(freed) != 0 {
		t.Fatalf("%d edge(s) released on `in_progress`, want 0", len(freed))
	}
}
