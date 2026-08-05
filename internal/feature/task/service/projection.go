package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément | Résumé                                              | Ligne |
// |---------|-----------------------------------------------------|-------|
// | toTask  | Projects a store task onto the API view              | 17    |
// | toNote  | Projects a store note onto the API view              | 32    |
//
// Fin du sommaire.
// =====================================================================

import "github.com/Coddyum/flowlio-agents/internal/feature/task/store"

// toTask projects a store task onto the API view. Internal identifiers (task, team and project
// UUIDs) do not cross this boundary: an agent works on readable numbers, never on UUIDs.
func toTask(t store.Task) Task {
	return Task{
		Number:    t.Number,
		Title:     t.Title,
		Body:      t.Body,
		Status:    t.Status,
		Priority:  t.Priority,
		Deadline:  t.Deadline,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
		Archived:  t.ArchivedAt != nil,
	}
}

// toNote projects a store note onto the API view.
func toNote(n store.Note) Note {
	return Note{
		Body:      n.Body,
		CreatedAt: n.CreatedAt,
	}
}
