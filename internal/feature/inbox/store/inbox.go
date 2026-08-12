package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                  | Ligne |
// |------------------------|---------------------------------------------------------|-------|
// | store.ProjectKey       | Resolves the key of the token's project                   | 29    |
// | store.Cursor           | Reads the token cursor and the head of the journal        | 45    |
// | store.IncomingOpen     | The incoming questions waiting for an answer              | 61    |
// | store.OutgoingAnswered | My questions that got an answer                           | 89    |
// | store.InProgressTasks  | My tasks in progress, sign of an interrupted session      | 117   |
// | store.UnblockedTasks   | My tasks no internal dependency blocks any more           | 144   |
// | store.Advance          | Moves the token cursor forward, never backwards           | 173   |
// | translate              | Brings a database error back to a domain error            | 181   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ProjectKey resolves the key of the token's project, to compose readable references.
func (s *store) ProjectKey(ctx context.Context, teamID, projectID uuid.UUID) (string, error) {
	key, err := s.q.InboxProjectKey(ctx, database.InboxProjectKeyParams{
		ID:     projectID,
		TeamID: teamID,
	})
	if err != nil {
		return "", translate(err, "project key")
	}
	return key, nil
}

// Cursor reads the token position and the head of the team journal.
//
// A token that never called check_inbox has no cursor row: the query returns 0 rather than an
// absence, so everything shows up as new. That is exact, and it is what makes a token rotation
// painless.
func (s *store) Cursor(ctx context.Context, sc Scope) (Cursor, error) {
	row, err := s.q.InboxCursor(ctx, database.InboxCursorParams{
		TokenID:   sc.TokenID,
		TeamID:    sc.TeamID,
		ProjectID: sc.ProjectID,
	})
	if err != nil {
		return Cursor{}, translate(err, "cursor")
	}
	return Cursor{LastEventID: row.LastEventID, HeadEventID: row.HeadEventID}, nil
}

// IncomingOpen lists the questions I am the recipient of and that are waiting for an answer.
//
// In this bucket the last message is always the author's: my own answer would move the issue to
// `answered` and take it out of here.
func (s *store) IncomingOpen(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error) {
	rows, err := s.q.ListIncomingOpenIssues(ctx, database.ListIncomingOpenIssuesParams{
		TeamID:      sc.TeamID,
		ProjectID:   sc.ProjectID,
		LastEventID: lastEventID,
		MaxRows:     sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "incoming open")
	}

	lines := make([]IssueLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, IssueLine{
			Number:    row.Number,
			Title:     row.Title,
			PeerKey:   row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.IsNew,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// OutgoingAnswered lists my questions that got an answer: I was blocked, I no longer am. PeerKey
// is here the recipient's key, the one the reference carries.
func (s *store) OutgoingAnswered(ctx context.Context, sc Scope, lastEventID int64) ([]IssueLine, error) {
	rows, err := s.q.ListOutgoingAnsweredIssues(ctx, database.ListOutgoingAnsweredIssuesParams{
		TeamID:      sc.TeamID,
		ProjectID:   sc.ProjectID,
		LastEventID: lastEventID,
		MaxRows:     sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "outgoing answered")
	}

	lines := make([]IssueLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, IssueLine{
			Number:    row.Number,
			Title:     row.Title,
			PeerKey:   row.PeerKey,
			Excerpt:   row.Excerpt,
			Truncated: row.Truncated,
			New:       row.IsNew,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// InProgressTasks lists my tasks in progress: a task left there signals an interrupted session,
// to be picked up again before opening a new one.
func (s *store) InProgressTasks(ctx context.Context, sc Scope) ([]TaskLine, error) {
	rows, err := s.q.ListInProgressTasks(ctx, database.ListInProgressTasksParams{
		TeamID:    sc.TeamID,
		ProjectID: sc.ProjectID,
		MaxRows:   sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "in progress tasks")
	}

	lines := make([]TaskLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, TaskLine{
			Number:    row.Number,
			Title:     row.Title,
			Priority:  string(row.Priority),
			UpdatedAt: row.UpdatedAt,
		})
	}
	return lines, nil
}

// UnblockedTasks lists the tasks no internal dependency blocks any more.
//
// It is the only task bucket to carry a "new" flag: unlike my work in progress, the unblocking
// reaches me from the outside — it is ANOTHER task that moved on — and I may not have seen it go
// by.
func (s *store) UnblockedTasks(ctx context.Context, sc Scope, lastEventID int64) ([]UnblockedLine, error) {
	rows, err := s.q.ListUnblockedTasks(ctx, database.ListUnblockedTasksParams{
		TeamID:      sc.TeamID,
		ProjectID:   sc.ProjectID,
		LastEventID: lastEventID,
		MaxRows:     sc.Limit,
	})
	if err != nil {
		return nil, translate(err, "unblocked tasks")
	}

	lines := make([]UnblockedLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, UnblockedLine{
			Number:   row.Number,
			Title:    row.Title,
			Priority: string(row.Priority),
			Status:   string(row.Status),
			New:      row.IsNew,
		})
	}
	return lines, nil
}

// Advance moves the token cursor forward.
//
// The query takes the maximum of the old and the new value: two concurrent check_inbox calls of
// the same token therefore cannot make the position go backwards. No transaction is needed — the
// worst case is a lost "new" flag.
func (s *store) Advance(ctx context.Context, tokenID uuid.UUID, headEventID int64) error {
	return translate(s.q.AdvanceInboxCursor(ctx, database.AdvanceInboxCursorParams{
		TokenID:     tokenID,
		LastEventID: headEventID,
	}), "advance cursor")
}

// translate brings database errors back to the store's domain errors.
func translate(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return errors.Join(errors.New("inbox store: "+op), err)
}
