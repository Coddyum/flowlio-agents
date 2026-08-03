package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément | Résumé                                              | Ligne |
// |---------|-----------------------------------------------------|-------|
// | toTask  | Projette une tâche du store en vue API               | 18    |
// | toNote  | Projette une note du store en vue API                | 33    |
//
// Fin du sommaire.
// =====================================================================

import "github.com/Coddyum/flowlio-agents/internal/feature/task/store"

// toTask projette une tâche du store en vue API. Les identifiants internes (UUID de tâche, de
// team et de projet) ne franchissent pas cette frontière : un agent travaille sur des numéros
// lisibles, jamais sur des UUID.
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

// toNote projette une note du store en vue API.
func toNote(n store.Note) Note {
	return Note{
		Body:      n.Body,
		CreatedAt: n.CreatedAt,
	}
}
