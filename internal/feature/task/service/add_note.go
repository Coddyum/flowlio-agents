package service

import (
	"context"
	"fmt"
	"strings"
)

// AddNote ajoute une note de progression au fil d'une tâche.
//
// C'est l'écriture la plus fréquente d'un agent : elle doit rester sans cérémonie. Le scope est
// porté par la query d'insertion elle-même, donc aucune vérification préalable n'est nécessaire
// ici — et aucune ne peut être oubliée.
func (s *service) AddNote(ctx context.Context, in AddNoteInput) (Note, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Note{}, err
	}

	body := strings.TrimSpace(in.Body)
	if body == "" {
		return Note{}, fmt.Errorf("%w: note vide", ErrInvalidInput)
	}
	if err := validateBody("note", body); err != nil {
		return Note{}, err
	}

	note, err := s.store.AddNote(ctx, in.TeamID, in.ProjectID, in.Number, body)
	if err != nil {
		return Note{}, translateStore(err, "add note")
	}
	return toNote(note), nil
}
