package store

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Position reads the project relevance head and the token cursor in one query.
//
// The head is the id of the latest event ADDRESSED to this project (its notify_project_id), not the
// team's whole activity: the probe must not report work for events the project authored or that
// concern a sibling. It reuses the inbox's InboxCursor query verbatim.
//
// A token that never called check_inbox has no cursor row: InboxCursor coalesces its position to 0,
// so a brand-new token reads Head > 0 = Cursor and the probe correctly reports work. There is no
// "not found" to handle — the absence of a cursor is a valid position, not an error.
func (s *store) Position(ctx context.Context, teamID, projectID, tokenID uuid.UUID) (Position, error) {
	row, err := s.q.InboxCursor(ctx, database.InboxCursorParams{
		TokenID:   tokenID,
		TeamID:    teamID,
		ProjectID: projectID,
	})
	if err != nil {
		return Position{}, fmt.Errorf("wake store: position: %w", err)
	}
	return Position{Head: row.HeadEventID, Cursor: row.LastEventID}, nil
}
