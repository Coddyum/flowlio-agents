package store

import (
	"context"

	"github.com/Coddyum/flowlio-ia/internal/database"
)

// AppendEvent écrit une entrée du journal.
//
// Toujours appelé dans la transaction de ce qui la produit : un événement écrit à part pourrait
// manquer alors que l'issue existe, et le correspondant ne serait jamais prévenu.
//
// En v1 le journal ne sert qu'au drapeau « nouveau » de l'inbox — l'état de référence reste
// issues.state — mais cette propriété est ce qui autorise à ne PAS payer une livraison
// exactement-une-fois. Ne pas s'appuyer dessus pour autre chose sans relire docs/DESIGN-M3.md.
func (s *store) AppendEvent(ctx context.Context, event Event) error {
	err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:         event.TeamID,
		ProjectID:      event.ProjectID,
		ActorProjectID: event.ActorProjectID,
		Kind:           event.Kind,
		SubjectType:    database.EventSubjectIssue,
		SubjectID:      event.SubjectID,
	})
	return translate(err, "append event")
}
