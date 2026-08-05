package store

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
)

// AppendEvent writes a journal entry.
//
// Always called inside the transaction of whatever produces it: an event written apart could go
// missing while the issue exists, and the correspondent would never be told.
//
// In v1 the journal only serves the inbox's "new" flag — the reference state stays issues.state —
// but that property is what allows NOT paying for exactly-once delivery. Do not lean on it for
// anything else without re-reading docs/DESIGN-M3.md.
func (s *store) AppendEvent(ctx context.Context, event Event) error {
	err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:         event.TeamID,
		ProjectID:      event.ProjectID,
		ActorProjectID: event.ActorProjectID,
		Kind:           event.Kind,
		SubjectType:    database.EventSubjectIssue,
		SubjectID:      event.SubjectID,
	})
	return translate(err, "append event")
}
