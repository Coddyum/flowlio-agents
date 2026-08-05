package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// What the SERVICE refuses on its own, before the database gets a say.
//
// These refusals do not replace the constraints — self-blocking has its CHECK, the duplicate its
// partial unique index, the cross-repo dependency its composite foreign key. They exist to return a
// REASON: an agent reading `pq: violates constraint task_dependencies_not_self` does not know what
// to fix, whereas it knows what to do with a sentence.
func TestBlockTaskRefusals(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	base := service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56}

	tests := []struct {
		name  string
		setup func(*fakeStore)
		in    service.BlockTaskInput
	}{
		{
			name: "a task does not block itself",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 55},
		},
		{
			name: "release condition outside the vocabulary",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "archived"},
		},
		{
			// `todo` and `blocked` are refused for the same reason: they are not progress. An edge
			// waiting for them would be born released, or would never be.
			name: "release condition that is not progress",
			in:   service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "todo"},
		},
		{
			name:  "the blocked task is archived",
			setup: func(f *fakeStore) { f.archivedNumbers = map[int64]bool{55: true} },
			in:    base,
		},
		{
			// Archived, it will never reach anything: the edge would be impossible to release other
			// than by hand, and the task would stay blocked with nothing to say so.
			name:  "the blocker is archived",
			setup: func(f *fakeStore) { f.archivedNumbers = map[int64]bool{56: true} },
			in:    base,
		},
		{
			// An edge born released is a block that does not block: the task would move to `blocked`
			// with nothing ever journalled to bring it back out.
			name:  "the blocker is already done",
			setup: func(f *fakeStore) { f.statusByNumber = map[int64]string{56: "done"} },
			in:    base,
		},
		{
			name:  "the blocker is already in_progress and that is what we were waiting for",
			setup: func(f *fakeStore) { f.statusByNumber = map[int64]string{56: "in_progress"} },
			in:    service.BlockTaskInput{TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56, Until: "in_progress"},
		},
		{
			name: "incomplete scope",
			in:   service.BlockTaskInput{ProjectID: projectID, Number: 55, Blocker: 56},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStore{}
			if tc.setup != nil {
				tc.setup(fake)
			}
			svc := service.New(fake)

			if _, err := svc.BlockTask(context.Background(), tc.in); !errors.Is(err, service.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			if fake.lastDependency != (store.NewDependency{}) {
				t.Error("an edge was written despite the refusal")
			}
		})
	}
}

// The cycle. A blocks B which blocks A would leave both `blocked` forever with nothing to say so —
// the exact opposite of what this feature promises.
//
// The traversal is a pure function called on the project's active graph, so this guarantee holds
// without a database: it cannot be lost to a test environment.
func TestBlockTaskRefusesCycles(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}

	// 56 is already blocked by 55. Blocking 55 on 56 would close the loop.
	fake.activeEdges = []store.Edge{{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(55)}}

	svc := service.New(fake)
	_, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if fake.lastDependency != (store.NewDependency{}) {
		t.Error("the cycle edge was written")
	}
}

// The INDIRECT cycle: 55 → 56 → 57, and 57 → 55 is attempted. A check looking only at immediate
// neighbours would let that one through, and it is exactly the shape a dependency graph takes after
// three cards.
func TestBlockTaskRefusesIndirectCycles(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}
	fake.activeEdges = []store.Edge{
		{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(56)},
		{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(57)},
	}

	svc := service.New(fake)
	_, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 57, Blocker: 55,
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput on a three-node cycle", err)
	}
}

// The counterpart: a chain that does not loop must go through. A cycle check refusing everything
// with a common ancestor would be green on the previous test and unusable in real life.
func TestBlockTaskAcceptsDiamond(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()
	fake := &fakeStore{}
	// 55 and 56 both depend on 57. Adding 55 → 56 closes nothing.
	fake.activeEdges = []store.Edge{
		{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(57)},
		{TaskID: fake.taskID(56), BlockerTaskID: fake.taskID(57)},
	}

	svc := service.New(fake)
	if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("BlockTask on a diamond: %v", err)
	}
	if fake.lastDependency.BlockerTaskID != fake.taskID(56) {
		t.Errorf("edge written towards %v, want task 56", fake.lastDependency.BlockerTaskID)
	}
}

// set_blocked is the field the first refactor will want to remove. This test says why it must not:
// it is the ONLY trace of who set the block, and it can only be computed at write time — afterwards,
// "blocked by the edge" and "blocked by an agent for another reason" are indistinguishable.
func TestBlockTaskRecordsWhoBlocked(t *testing.T) {
	teamID, projectID := uuid.New(), uuid.New()

	tests := []struct {
		name           string
		blockedStatus  string
		wantSetBlocked bool
		wantPatched    bool
	}{
		{"the task was free: this edge is what blocks it", "todo", true, true},
		{"the task was already blocked: the edge takes no credit for it", "blocked", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStore{statusByNumber: map[int64]string{55: tc.blockedStatus}}
			svc := service.New(fake)

			if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
				TeamID: teamID, ProjectID: projectID, Number: 55, Blocker: 56,
			}); err != nil {
				t.Fatalf("BlockTask: %v", err)
			}

			if fake.lastDependency.SetBlocked != tc.wantSetBlocked {
				t.Errorf("set_blocked = %v, want %v", fake.lastDependency.SetBlocked, tc.wantSetBlocked)
			}
			patched := fake.lastPatch.Status != nil
			if patched != tc.wantPatched {
				t.Errorf("status patched = %v, want %v", patched, tc.wantPatched)
			}
			if patched && *fake.lastPatch.Status != "blocked" {
				t.Errorf("status patched = %q, want blocked", *fake.lastPatch.Status)
			}
		})
	}
}

// The default condition is `done`, and it must reach the store as such: an edge written with no
// condition would be an edge the database fills in on its behalf, hence one more rule to go and
// read somewhere else.
func TestBlockTaskDefaultsToDone(t *testing.T) {
	fake := &fakeStore{}
	svc := service.New(fake)

	if _, err := svc.BlockTask(context.Background(), service.BlockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}
	if fake.lastDependency.UntilStatus != "done" {
		t.Errorf("until = %q, want done", fake.lastDependency.UntilStatus)
	}
}

// Replaying unblock_task on an already-released edge must not fail: an agent that lost its context
// and replays is not at fault, and refusing would break a session resume on an action already done.
func TestUnblockTaskIsReplayable(t *testing.T) {
	fake := &fakeStore{}
	svc := service.New(fake)

	task, err := svc.UnblockTask(context.Background(), service.UnblockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	})
	if err != nil {
		t.Fatalf("UnblockTask on a missing edge: %v", err)
	}
	if task.Number != 55 {
		t.Errorf("task returned = #%d, want #55", task.Number)
	}
	if len(fake.events) != 0 {
		t.Errorf("%d event(s) for an unblocking with no effect, want 0", len(fake.events))
	}
}

// A real unblocking journals, and the subject is the UNBLOCKED task — not the blocker. That is what
// check_inbox will hand back to the project: putting the blocker as subject would surface the event
// on the wrong card.
func TestUnblockTaskAnnouncesOnTheFreedTask(t *testing.T) {
	fake := &fakeStore{}
	fake.activeEdges = []store.Edge{{TaskID: fake.taskID(55), BlockerTaskID: fake.taskID(56)}}
	svc := service.New(fake)

	if _, err := svc.UnblockTask(context.Background(), service.UnblockTaskInput{
		TeamID: uuid.New(), ProjectID: uuid.New(), Number: 55, Blocker: 56,
	}); err != nil {
		t.Fatalf("UnblockTask: %v", err)
	}

	if len(fake.events) != 1 {
		t.Fatalf("%d event(s), want 1", len(fake.events))
	}
	if fake.events[0].Kind != "task.unblocked" {
		t.Errorf("kind = %q, want task.unblocked", fake.events[0].Kind)
	}
	if fake.events[0].SubjectID != fake.taskID(55) {
		t.Error("the event subject is not the unblocked task")
	}
}
