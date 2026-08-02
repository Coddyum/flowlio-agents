package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | store.AddNote   | Ajoute une note de progression, via un SELECT scopé             | 27    |
// | store.ListNotes | Lit le fil d'une tâche dans l'ordre d'écriture                  | 42    |
// | toNote          | Projette une ligne générée en type domaine                      | 60    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-ia/internal/database"
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
	return toNote(row), nil
}

// ListNotes lit le fil d'une tâche, du plus ancien au plus récent : c'est un journal de
// progression, il se lit dans l'ordre où il a été écrit.
func (s *store) ListNotes(ctx context.Context, teamID, projectID uuid.UUID, number int64) ([]Note, error) {
	rows, err := s.q.ListTaskNotes(ctx, database.ListTaskNotesParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
	})
	if err != nil {
		return nil, translate(err, "list notes")
	}

	notes := make([]Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, toNote(row))
	}
	return notes, nil
}

// toNote projette une ligne générée en type domaine.
func toNote(row database.TaskNote) Note {
	return Note{
		ID:        row.ID,
		Body:      row.BodyMd,
		CreatedAt: row.CreatedAt,
	}
}
