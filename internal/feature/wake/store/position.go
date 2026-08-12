package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                       | Ligne |
// |---------------------|--------------------------------------------------------------|-------|
// | store.Position   | Reads the project head and the token cursor in one query          | 31    |
// | store.Actionable | Whether the journal's movement is new actionable work, and its tier | 52    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/pkg/effort"
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

// Actionable answers whether the journal's movement past the cursor is NEW work worth a session — a
// new incoming question still open, a new answer to one of mine, or a newly unblocked task — and the
// highest rigour tier among the issues in it (FLWL-85).
//
// It is read ONLY once the probe already knows head > cursor, so it never runs on the idle poll the
// zero-SQL model protects: one indexed read at the instant a launch is being decided. Returning
// actionable=false is what stops a full session boot for a closed-issue event or a sibling's
// traffic. The tier comes back as a rank because the tiers do not order as strings; effort.FromRank
// turns it into a name, and an absent tier already read as standard's rank in SQL.
func (s *store) Actionable(ctx context.Context, teamID, projectID uuid.UUID, cursor int64) (bool, string, error) {
	row, err := s.q.WakeActionable(ctx, database.WakeActionableParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Cursor:    cursor,
	})
	if err != nil {
		return false, "", fmt.Errorf("wake store: actionable: %w", err)
	}
	return row.Actionable, effort.FromRank(int(row.EffortRank)), nil
}
