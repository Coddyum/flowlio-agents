package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | store.AddNote   | Ajoute une note de progression, via un SELECT scopé             | 27    |
// | store.ListNotes | Rend la fin du fil, bornée, et le total écrit                   | 52    |
// | toNote          | Projette une ligne générée en type domaine                      | 75    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AddNote ajoute une note de progression à une tâche.
//
// L'insertion est alimentée par un SELECT scopé sur la tâche : si la tâche n'appartient pas au
// projet, aucune ligne n'est produite, donc rien n'est inséré et l'appel remonte ErrNotFound.
// Le scope est porté par la query, pas par une vérification préalable qu'un appelant pourrait
// oublier.
func (s *store) AddNote(ctx context.Context, teamID, projectID uuid.UUID, number int64, body string) (Note, error) {
	row, err := s.q.CreateTaskNote(ctx, database.CreateTaskNoteParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
		BodyMd:    body,
	})
	if err != nil {
		return Note{}, translate(err, "add note")
	}
	// L'écriture et la lecture ne rendent plus la même ligne générée : ListTaskNotes porte en plus
	// le total du fil. Projeter ici plutôt que partager toNote évite d'inventer un type commun
	// pour deux formes qui n'ont aucune raison de rester identiques.
	return Note{ID: row.ID, Body: row.BodyMd, CreatedAt: row.CreatedAt}, nil
}

// ListNotes rend la FIN du fil d'une tâche — au plus limit notes — et le nombre total écrit.
//
// La query rend les plus récentes d'abord, parce que ce sont elles qui portent l'état ; cette
// fonction les remet dans l'ordre d'écriture, qui est celui dans lequel un journal se lit. Le
// retournement vit ici et pas dans le service : il fait partie du contrat de lecture annoncé par
// le type, pas d'une décision métier.
//
// Le total vient de la MÊME query (count(*) OVER ()), donc borner le fil n'a pas coûté un
// aller-retour de plus sur le chemin de lecture le plus appelé du produit.
func (s *store) ListNotes(ctx context.Context, teamID, projectID uuid.UUID, number int64, limit int32) ([]Note, int, error) {
	rows, err := s.q.ListTaskNotes(ctx, database.ListTaskNotesParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
		Lim:       limit,
	})
	if err != nil {
		return nil, 0, translate(err, "list notes")
	}
	if len(rows) == 0 {
		return []Note{}, 0, nil
	}

	total := int(rows[0].Total)
	notes := make([]Note, len(rows))
	for i, row := range rows {
		notes[len(rows)-1-i] = toNote(row)
	}
	return notes, total, nil
}

// toNote projette une ligne générée en type domaine.
func toNote(row database.ListTaskNotesRow) Note {
	return Note{
		ID:        row.ID,
		Body:      row.BodyMd,
		CreatedAt: row.CreatedAt,
	}
}
