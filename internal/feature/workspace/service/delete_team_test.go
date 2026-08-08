package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// A team that is not there comes back as ErrNotFound, translated: the store's own sentinel must not
// reach the handler raw, or the 404 would depend on two packages agreeing on an error value.
func TestDeletingAnAbsentTeamIsNotFound(t *testing.T) {
	st := newFakeStore()
	st.deleteTeamErr = store.ErrNotFound

	if err := New(st).DeleteTeam(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// A nil identifier is refused BEFORE the store. It can only mean the handler skipped teamFor, and a
// nil UUID handed to a delete is not something a persistence layer should be asked to sort out.
//
// The fake is armed to SUCCEED here: that is what makes the assertion mean "the store was never
// asked" rather than "the store happened to answer an error too".
func TestDeletingATeamWithoutAnIdentifierNeverReachesTheStore(t *testing.T) {
	st := newFakeStore()

	if err := New(st).DeleteTeam(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if st.deletedTeam != uuid.Nil {
		t.Errorf("the store was asked to delete %s on a nil identifier", st.deletedTeam)
	}
}

// THE POSITIVE CONTROL, and it says WHICH team was deleted rather than merely that no error came
// back. A service that dropped its argument and deleted something of its own choosing would satisfy
// an assertion on the error alone.
func TestDeletingATeamPassesTheIdentifierItWasGiven(t *testing.T) {
	st := newFakeStore()
	target := uuid.New()

	if err := New(st).DeleteTeam(context.Background(), target); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if st.deletedTeam != target {
		t.Errorf("the store deleted %s, the caller named %s", st.deletedTeam, target)
	}
}

// THE REFUSAL THAT PROTECTS A REPO DOES NOT EXIST HERE, and this test is where that decision is
// written down rather than merely absent.
//
// DeleteProject answers a *ProjectInUseError while a sibling repo holds a thread with the target,
// because that sibling outlives the deletion. A team leaves no survivor: both ends of every thread
// are inside it. A future edit that "harmonised" the two by adding a blocker check would make this
// go red instead of silently making a customer unable to delete their own project.
func TestDeletingATeamIsNeverRefusedForAThread(t *testing.T) {
	st := newFakeStore()
	// The situation DeleteProject refuses on, expressed in the only vocabulary this fake has.
	st.deletion = store.ProjectDeletion{Blockers: []store.Blocker{{Key: "CORE", Threads: 2}}}

	err := New(st).DeleteTeam(context.Background(), uuid.New())

	if err != nil {
		t.Fatalf("error = %v, want nil — a team's own repos talking to each other cannot block "+
			"the deletion of that team", err)
	}
}
