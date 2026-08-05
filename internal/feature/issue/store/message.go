package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                  | Ligne |
// |-----------------------|---------------------------------------------------------|-------|
// | store.AddFirstMessage | Writes the message that opens an issue's thread           | 25    |
// | store.ListMessages    | Reads the thread, scoped by the join on its issue         | 38    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AddFirstMessage writes the message that opens the thread.
//
// It is not scoped by a visibility clause, unlike every other access: it is only ever called
// immediately after CreateIssue, in the same transaction, on the identifier that call just
// produced. The scope was already applied when the issue was inserted.
func (s *store) AddFirstMessage(ctx context.Context, issueID, authorProjectID uuid.UUID, body string) error {
	_, err := s.q.AppendFirstMessage(ctx, database.AppendFirstMessageParams{
		IssueID:         issueID,
		AuthorProjectID: authorProjectID,
		BodyMd:          body,
	})
	return translate(err, "append first message")
}

// ListMessages returns the tail of an issue's thread — at most limit messages — and the total
// number written.
//
// The query joins the issue and applies the visibility clause to it: reading the messages of an
// issue one cannot see is impossible, even knowing its internal identifier.
//
// The query returns the messages from the MOST RECENT to the oldest, because it is the tail of the
// thread that carries the state; they are put back here in write order, which is how a conversation
// is read. Reversing on the way out rather than in the SQL keeps the bound and the sort in the same
// place, where they are read together.
//
// An empty thread returns a total of zero: the counting row does not exist when no row comes out,
// and there is nothing else to deduce from it.
func (s *store) ListMessages(ctx context.Context, ref Ref, issueID uuid.UUID, limit int32) ([]Message, int, error) {
	rows, err := s.q.ListIssueMessages(ctx, database.ListIssueMessagesParams{
		TeamID:          ref.TeamID,
		IssueID:         issueID,
		CallerProjectID: ref.CallerProjectID,
		Lim:             limit,
	})
	if err != nil {
		return nil, 0, translate(err, "list messages")
	}
	if len(rows) == 0 {
		return []Message{}, 0, nil
	}

	total := int(rows[0].Total)
	messages := make([]Message, len(rows))
	for i, row := range rows {
		messages[len(rows)-1-i] = Message{
			AuthorKey: row.AuthorKey,
			Body:      row.BodyMd,
			CreatedAt: row.CreatedAt,
		}
	}
	return messages, total, nil
}
