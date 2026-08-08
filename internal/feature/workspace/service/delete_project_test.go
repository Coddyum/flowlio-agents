package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// The refusal is its OWN error, never ErrConflict.
//
// Reusing ErrConflict was the tempting shortcut, and it would have shipped a lie: the handler
// renders that sentinel as the body "already exists", which on a DELETE describes nothing that
// happened. This test says what the error IS on both counts — the sentinel it matches, and the one
// it must not.
func TestARefusedDeletionIsNotAConflict(t *testing.T) {
	st := newFakeStore()
	st.deletion = store.ProjectDeletion{
		Blockers: []store.Blocker{{Key: "CORE", Threads: 2}, {Key: "WEB", Threads: 1}},
	}
	svc := New(st)

	err := svc.DeleteProject(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, ErrProjectInUse) {
		t.Fatalf("error = %v, want it to match ErrProjectInUse", err)
	}
	if errors.Is(err, ErrConflict) {
		t.Error("the refusal matches ErrConflict, so the customer is told \"already exists\" on a delete")
	}

	var inUse *ProjectInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("error = %T, want a *ProjectInUseError carrying the siblings", err)
	}
	want := []ThreadHolder{{Key: "CORE", Threads: 2}, {Key: "WEB", Threads: 1}}
	if len(inUse.Holders) != len(want) {
		t.Fatalf("holders = %#v, want %#v", inUse.Holders, want)
	}
	for i, holder := range inUse.Holders {
		if holder != want[i] {
			t.Errorf("holder %d = %#v, want %#v", i, holder, want[i])
		}
	}
}

// The sentence the customer reads, in full. It is asserted literally because it is the product: it
// names every sibling, counts its threads, and ends on a move the human can actually make.
//
// The singular/plural is part of the assertion: "1 threads" is the kind of detail that tells a
// reader the message was assembled by a machine that was not paying attention.
func TestTheRefusalSaysWhoIsConcernedAndWhatToDo(t *testing.T) {
	err := &ProjectInUseError{Holders: []ThreadHolder{{Key: "CORE", Threads: 2}, {Key: "WEB", Threads: 1}}}

	const want = "this repo still holds questions with CORE (2 threads), WEB (1 thread), and " +
		"deleting it would erase those threads from their side too. Retire it instead: revoke its " +
		"tokens, then deny its trust edges."

	if got := err.Error(); got != want {
		t.Errorf("message =\n  %q\nwant\n  %q", got, want)
	}
}

// A deletion that went through returns nil, and that is read from the store's own report.
func TestADeletionThatWentThroughReturnsNil(t *testing.T) {
	st := newFakeStore()
	st.deletion = store.ProjectDeletion{Deleted: true}

	if err := New(st).DeleteProject(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}

// A store reporting NEITHER a deletion NOR a reason is an internal inconsistency, and it must
// surface as one. Answering 204 there would turn a delete that removed nothing into a success the
// caller has no way to question.
func TestADeletionThatRemovedNothingIsNotASuccess(t *testing.T) {
	st := newFakeStore()
	st.deletion = store.ProjectDeletion{Deleted: false}

	err := New(st).DeleteProject(context.Background(), uuid.New(), uuid.New())

	if err == nil {
		t.Fatal("error = nil: the caller is told the repo was deleted when no row was removed")
	}
	if errors.Is(err, ErrProjectInUse) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, want an internal error — this is a bug in the store, not a user case", err)
	}
}

// A repo absent from the team comes back as ErrNotFound, through the same translation as every
// other read. The store's sentinel must not reach the handler raw.
func TestDeletingAnAbsentProjectIsNotFound(t *testing.T) {
	st := newFakeStore()
	st.deleteErr = store.ErrNotFound

	err := New(st).DeleteProject(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// Neither identifier may be empty. The team comes from teamFor and the project from the path, so a
// nil here means the handler skipped a step — the store is not asked to sort that out.
func TestDeletingWithoutIdentifiersIsRefusedBeforeTheStore(t *testing.T) {
	cases := []struct {
		name      string
		teamID    uuid.UUID
		projectID uuid.UUID
	}{
		{"no team", uuid.Nil, uuid.New()},
		{"no project", uuid.New(), uuid.Nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The fake would report a deletion if it were called: the assertion below therefore
			// proves the refusal came BEFORE the store, not from it.
			st := newFakeStore()
			st.deletion = store.ProjectDeletion{Deleted: true}

			if err := New(st).DeleteProject(context.Background(), tc.teamID, tc.projectID); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
