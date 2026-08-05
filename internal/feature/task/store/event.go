package store

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
)

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
	err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:         event.TeamID,
		ProjectID:      event.ProjectID,
		ActorProjectID: event.ActorProjectID,
		Kind:           event.Kind,
		SubjectType:    database.EventSubjectTask,
		SubjectID:      event.SubjectID,
	})
	return translate(err, "append event")
}
