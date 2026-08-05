package store

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
)

// AppendEvent écrit une entrée du journal, avec `task` comme type de sujet.
//
// Toujours appelé dans la transaction de ce qui la produit : un événement écrit à part pourrait
// manquer alors que l'arête est déjà libérée, et la tâche débloquée ne l'apprendrait jamais — le
// manque exact que cette feature comble.
//
// La feature task porte sa propre écriture plutôt que d'emprunter celle de la feature issue : un
// module n'importe jamais un autre module. Les deux passent par la même query générée, donc la
// table n'a qu'une définition.
//
// Le journal ne sert qu'au drapeau « nouveau » de l'inbox — l'état de référence reste
// task_dependencies.released_at et tasks.status. C'est cette propriété qui autorise à ne PAS payer
// une livraison exactement-une-fois : un événement manqué coûte un `new: false`, jamais une tâche
// qui ignore qu'elle est débloquée. Ne pas s'appuyer dessus pour autre chose sans relire
// docs/DESIGN-M3.md.
func (s *store) AppendEvent(ctx context.Context, event Event) error {
	err := s.q.AppendEvent(ctx, database.AppendEventParams{
		TeamID:         event.TeamID,
		ProjectID:      event.ProjectID,
		ActorProjectID: event.ActorProjectID,
		Kind:           event.Kind,
		SubjectType:    database.EventSubjectTask,
		SubjectID:      event.SubjectID,
	})
	return translate(err, "append event")
}
