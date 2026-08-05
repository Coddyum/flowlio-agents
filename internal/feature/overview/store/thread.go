package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                      | Ligne |
// |---------------------|-------------------------------------------------------------|-------|
// | store.IssueByRef    | Yields an issue of the team, being neither author nor target  | 32  |
// | store.IssueMessages | Yields the last N messages, in reading order                  | 65    |
//
// Fin du sommaire.
// =====================================================================
//
// THIS IS WHERE THE NEW CAPABILITY IS, and therefore the risk. GetIssueByRef (issue feature)
// carries a visibility clause — you are the author or the recipient. Here it is ABSENT: a
// supervisor reads a WEB→CORE conversation while being neither. That is exactly what makes these
// two methods forbidden to any project-scoped principal, and it is the module's AdminOnly
// middleware that guarantees it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// IssueByRef yields the issue designated by its recipient's key and its number, provided it
// belongs to the resolved team. Outside the team it is ErrNotFound — the same one as for a
// reference that does not exist.
func (s *store) IssueByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Issue, error) {
	row, err := s.q.OverviewIssueByRef(ctx, database.OverviewIssueByRefParams{
		TeamID:     teamID,
		ProjectKey: projectKey,
		Number:     number,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Issue{}, ErrNotFound
		}
		return Issue{}, fmt.Errorf("overview store: issue %s-%d of team %s: %w", projectKey, number, teamID, err)
	}

	return Issue{
		ID:               row.ID,
		Number:           row.Number,
		State:            string(row.State),
		Title:            row.Title,
		ProjectKey:       row.ProjectKey,
		AuthorProjectKey: row.AuthorProjectKey,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}, nil
}

// IssueMessages yields the N MOST RECENT messages, rendered in reading order, and the total
// before the bound. The tail of a thread is the answer: that is what must be kept when something
// has to be cut.
//
// teamID is not decorative here. issue_messages has no team_id column and its foreign key towards
// projects is SIMPLE: nothing at the schema level stops a message of another team from pointing
// at this thread. The query rejects it, and it is the only join clause of the repo whose removal
// is observable.
func (s *store) IssueMessages(ctx context.Context, teamID, issueID uuid.UUID, limit int32) ([]Message, int64, error) {
	rows, err := s.q.OverviewIssueMessages(ctx, database.OverviewIssueMessagesParams{
		TeamID:  teamID,
		IssueID: issueID,
		MaxRows: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: messages of issue %s: %w", issueID, err)
	}

	out := make([]Message, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, Message{
			AuthorKey: r.AuthorKey,
			BodyMd:    r.BodyMd,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, total, nil
}
