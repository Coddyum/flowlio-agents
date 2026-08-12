package store

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
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
	id, err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:          event.TeamID,
		ProjectID:       event.ProjectID,
		ActorProjectID:  event.ActorProjectID,
		NotifyProjectID: event.NotifyProjectID,
		Kind:            event.Kind,
		SubjectType:     database.EventSubjectIssue,
		SubjectID:       event.SubjectID,
	})
	if err != nil {
		return translate(err, "append event")
	}

	// Bump the in-memory probe head of the project this event is addressed to, so a sleeping sibling
	// learns there is something to answer without a query (D55). Keyed by the notify target, not the
	// team: the author's own event never lifts the author's head. Kept inside the transaction on
	// purpose: the id we hand the probe is the durable one. A rollback would leave the head one wake
	// too high — a wasted wake, never a wrong state (see internal/core/probe).
	probe.RecordEvent(s.cache, event.TeamID, event.NotifyProjectID, id)
	return nil
}
