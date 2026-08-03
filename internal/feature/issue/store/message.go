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

// ListMessages rend la fin du fil d'une issue — au plus limit messages — et le nombre total écrit.
//
// La query joint l'issue et y applique la clause de visibilité : impossible de lire les messages
// d'une issue qu'on ne voit pas, même en connaissant son identifiant interne.
//
// La query rend les messages du plus RÉCENT au plus ancien, parce que c'est la fin du fil qui
// porte l'état ; ils sont remis ici dans l'ordre d'écriture, qui est celui dans lequel une
// conversation se lit. Inverser au retour plutôt que dans le SQL garde la borne et le tri au même
// endroit, où ils se lisent ensemble.
//
// Un fil vide rend un total de zéro : la ligne de comptage n'existe pas quand aucune ligne ne
// sort, et il n'y a rien à en déduire d'autre.
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
