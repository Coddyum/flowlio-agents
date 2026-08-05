package service

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// maxThreadMessages bounds the thread returned. A long exchange must not enter an agent's context
// in one block; the last messages are the ones carrying the state of the discussion.
const maxThreadMessages = 10

// GetIssue returns an issue and the tail of its thread.
//
// Both reads carry the same visibility clause, applied independently: the second one does not trust
// the result of the first.
func (s *service) GetIssue(ctx context.Context, ref Ref) (IssueDetail, error) {
	if err := validateScope(ref.TeamID, ref.CallerProjectID); err != nil {
		return IssueDetail{}, err
	}

	storeRef := store.Ref{
		TeamID:          ref.TeamID,
		CallerProjectID: ref.CallerProjectID,
		ProjectKey:      ref.ProjectKey,
		Number:          ref.Number,
	}

	found, err := s.store.IssueByRef(ctx, storeRef)
	if err != nil {
		return IssueDetail{}, translateStore(err, "issue by ref")
	}

	// The bound travels INSIDE the query: the store returns at most maxThreadMessages rows and the
	// real total. Slicing here, as before, left the database serialising and carrying a whole thread
	// of which the service threw away everything but the tail.
	rows, total, err := s.store.ListMessages(ctx, storeRef, found.ID, maxThreadMessages)
	if err != nil {
		return IssueDetail{}, translateStore(err, "list messages")
	}

	messages := make([]Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, toMessage(row))
	}

	return IssueDetail{
		Issue:         toIssue(found),
		Messages:      messages,
		MessagesTotal: total,
	}, nil
}
