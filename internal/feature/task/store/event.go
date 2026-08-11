package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                    | Ligne |
// |-------------------|-----------------------------------------------------------|-------|
// | Event             | A journal entry the task feature writes                     | 27    |
// | store.AppendEvent | Writes the entry and bumps the in-memory probe head         | 49    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Event is a journal entry.
//
// The task feature carries its own event write rather than borrowing the issue feature's: a module
// never imports another module. Both go through the same generated query, which keeps a single
// definition of the table.
type Event struct {
	TeamID         uuid.UUID
	ProjectID      uuid.UUID
	ActorProjectID uuid.UUID
	Kind           string
	SubjectID      uuid.UUID
}

// AppendEvent writes a journal entry, with `task` as the subject type.
//
// Always called inside the transaction of whatever produces it: an event written apart could go
// missing while the edge is already released, and the unblocked task would never learn about it —
// the exact gap this feature fills.
//
// The task feature carries its own write rather than borrowing the issue feature's: a module never
// imports another module. Both go through the same generated query, so the table has a single
// definition.
//
// The journal only serves the inbox's "new" flag — the reference state stays
// task_dependencies.released_at and tasks.status. That property is what allows NOT paying for
// exactly-once delivery: a missed event costs a `new: false`, never a task unaware that it is
// unblocked. Do not lean on it for anything else without re-reading docs/DESIGN-M3.md.
func (s *store) AppendEvent(ctx context.Context, event Event) error {
	id, err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:         event.TeamID,
		ProjectID:      event.ProjectID,
		ActorProjectID: event.ActorProjectID,
		Kind:           event.Kind,
		SubjectType:    database.EventSubjectTask,
		SubjectID:      event.SubjectID,
	})
	if err != nil {
		return translate(err, "append event")
	}

	// Bump the in-memory probe head so an unblocked task's project learns of it without a query
	// (D55). Inside the transaction on purpose, like the issue feature: the id is the durable one,
	// and a rollback costs at worst a wasted wake (see internal/core/probe).
	probe.RecordEvent(s.cache, event.TeamID, id)

	// Push a wake to that project's local waker. Unlike an issue answer, a task unblock always
	// concerns the SAME project as the event (a dependency never crosses a repo, D42), so the store
	// knows exactly whom to wake — event.ProjectID — and can signal here. Fire-and-forget; the
	// ladder and the piggyback are the backstop (see internal/core/wakepush).
	wakepush.Signal(s.cache, event.TeamID, event.ProjectID)
	return nil
}
