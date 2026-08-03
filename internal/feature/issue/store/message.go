package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                  | Ligne |
// |-----------------------|---------------------------------------------------------|-------|
// | store.AddFirstMessage | Écrit le message qui ouvre le fil d'une issue             | 25    |
// | store.ListMessages    | Lit le fil, scopé par jointure sur son issue              | 38    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AddFirstMessage écrit le message qui ouvre le fil.
//
// Il n'est pas scopé par une clause de visibilité, contrairement à tous les autres accès : il
// n'est appelé qu'immédiatement après CreateIssue, dans la même transaction, sur l'identifiant
// que celle-ci vient de produire. Le scope a déjà été appliqué à l'insertion de l'issue.
func (s *store) AddFirstMessage(ctx context.Context, issueID, authorProjectID uuid.UUID, body string) error {
	_, err := s.q.AppendFirstMessage(ctx, database.AppendFirstMessageParams{
		IssueID:         issueID,
		AuthorProjectID: authorProjectID,
		BodyMd:          body,
	})
	return translate(err, "append first message")
}

// ListMessages lit le fil d'une issue, du plus ancien au plus récent.
//
// La query joint l'issue et y applique la clause de visibilité : impossible de lire les messages
// d'une issue qu'on ne voit pas, même en connaissant son identifiant interne.
func (s *store) ListMessages(ctx context.Context, ref Ref, issueID uuid.UUID) ([]Message, error) {
	rows, err := s.q.ListIssueMessages(ctx, database.ListIssueMessagesParams{
		TeamID:          ref.TeamID,
		IssueID:         issueID,
		CallerProjectID: ref.CallerProjectID,
	})
	if err != nil {
		return nil, translate(err, "list messages")
	}

	messages := make([]Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, Message{
			AuthorKey: row.AuthorKey,
			Body:      row.BodyMd,
			CreatedAt: row.CreatedAt,
		})
	}
	return messages, nil
}
