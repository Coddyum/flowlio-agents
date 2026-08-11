package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                         | Ligne |
// |---------------|----------------------------------------------------------------|-------|
// | service.Check | Returns the actionable state and moves the cursor forward        | 40    |
// | toIssueLines  | Projects store issues into inbox lines                           | 117   |
// | toTaskLines   | Projects store tasks into inbox lines                            | 138   |
// | toUnblockedLines | Projects unblocked tasks into inbox lines                     | 151   |
// | overflow      | Counts what did not fit in a bucket                              | 167   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/core/probe"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
	"github.com/google/uuid"
)

// bucketSize bounds every bucket. An agent that is starting up must be able to read everything:
// past that, the answer stops being a starting point and becomes one more problem.
//
// One line more than the bound is asked of the store to know whether any are left, without having
// to count separately — a COUNT per bucket would be three more queries for an indicative figure.
const bucketSize = 10

// Check returns the actionable state of the project, then moves the token cursor forward.
//
// The head of the journal is read BEFORE the buckets: an event written during the call therefore
// stays "new" on the next round, rather than being passed over without ever being shown.
//
// The cursor is moved forward AFTER the response is built, and its failure is not fatal: at worst
// an already-read event stays flagged "new" one more time. Refusing the response because a flag
// could not be updated would be trading an annoyance for an outage.
func (s *service) Check(ctx context.Context, in CheckInput) (Inbox, error) {
	if in.TeamID == uuid.Nil || in.ProjectID == uuid.Nil || in.TokenID == uuid.Nil {
		return Inbox{}, fmt.Errorf("%w: incomplete project scope", ErrInvalidInput)
	}

	scope := store.Scope{
		TokenID:   in.TokenID,
		TeamID:    in.TeamID,
		ProjectID: in.ProjectID,
		Limit:     bucketSize + 1,
	}

	projectKey, err := s.store.ProjectKey(ctx, in.TeamID, in.ProjectID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: project key: %w", err)
	}

	cursor, err := s.store.Cursor(ctx, scope)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: cursor: %w", err)
	}

	incoming, err := s.store.IncomingOpen(ctx, scope, cursor.LastEventID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: incoming: %w", err)
	}
	answered, err := s.store.OutgoingAnswered(ctx, scope, cursor.LastEventID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: answered: %w", err)
	}
	tasks, err := s.store.InProgressTasks(ctx, scope)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: tasks: %w", err)
	}
	unblocked, err := s.store.UnblockedTasks(ctx, scope, cursor.LastEventID)
	if err != nil {
		return Inbox{}, fmt.Errorf("inbox service: unblocked: %w", err)
	}

	inbox := Inbox{
		Project: projectKey,
		// An incoming issue carries MY key: my project owns it and gave it its number. An
		// outgoing issue carries the recipient's key, which is the peer.
		NeedsAnswer: toIssueLines(incoming, projectKey, true),
		Answered:    toIssueLines(answered, projectKey, false),
		InProgress:  toTaskLines(tasks, projectKey),
		// A dependency never crosses a repo (D42): both ends therefore carry MY key, with no
		// possible exception.
		Unblocked: toUnblockedLines(unblocked, projectKey),
	}

	if more := (More{
		NeedsAnswer: overflow(len(incoming)),
		Answered:    overflow(len(answered)),
		InProgress:  overflow(len(tasks)),
		Unblocked:   overflow(len(unblocked)),
	}); more != (More{}) {
		inbox.More = &more
	}

	// Warm the probe signal (D55): the head just read is the team's, and the cursor is about to
	// reach it. Keeping the cached cursor in step here is not optional — check_inbox is what moves
	// the durable cursor, so a stale cached cursor would leave the probe reporting phantom work and
	// waking the agent for nothing until the next reseed.
	probe.Seed(s.cache, in.TeamID, in.TokenID, cursor.HeadEventID, cursor.HeadEventID)

	if err := s.store.Advance(ctx, in.TokenID, cursor.HeadEventID); err != nil {
		// Best effort: the response is right, only the comfort of the next call is degraded.
		return inbox, nil
	}
	return inbox, nil
}

// toIssueLines projects store issues into inbox lines.
//
// mine says the reference carries MY project key — the case of incoming issues, of which I am the
// recipient. For outgoing ones, the reference carries the peer's key.
func toIssueLines(rows []store.IssueLine, projectKey string, mine bool) []IssueLine {
	lines := make([]IssueLine, 0, min(len(rows), bucketSize))
	for _, row := range rows[:min(len(rows), bucketSize)] {
		refKey := row.PeerKey
		if mine {
			refKey = projectKey
		}
		lines = append(lines, IssueLine{
			Ref:       fmt.Sprintf("%s-%d", refKey, row.Number),
			Title:     row.Title,
			Peer:      row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.New,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines
}

// toTaskLines projects store tasks into inbox lines.
func toTaskLines(rows []store.TaskLine, projectKey string) []TaskLine {
	lines := make([]TaskLine, 0, min(len(rows), bucketSize))
	for _, row := range rows[:min(len(rows), bucketSize)] {
		lines = append(lines, TaskLine{
			Ref:      fmt.Sprintf("%s-%d", projectKey, row.Number),
			Title:    row.Title,
			Priority: row.Priority,
		})
	}
	return lines
}

// toUnblockedLines projects unblocked store tasks into inbox lines.
func toUnblockedLines(rows []store.UnblockedLine, projectKey string) []UnblockedLine {
	lines := make([]UnblockedLine, 0, min(len(rows), bucketSize))
	for _, row := range rows[:min(len(rows), bucketSize)] {
		lines = append(lines, UnblockedLine{
			Ref:      fmt.Sprintf("%s-%d", projectKey, row.Number),
			Title:    row.Title,
			Priority: row.Priority,
			Status:   row.Status,
			New:      row.New,
		})
	}
	return lines
}

// overflow counts what did not fit in a bucket. The store returns one more than the bound: the
// presence of that line is enough to say "some are left", without counting any further.
func overflow(fetched int) int {
	if fetched > bucketSize {
		return fetched - bucketSize
	}
	return 0
}
