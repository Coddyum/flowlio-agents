package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                        | Ligne |
// |-----------------|---------------------------------------------------------------|-------|
// | store.AddNote   | Appends a progress note, through a scoped SELECT                | 26    |
// | store.ListNotes | Returns the bounded tail of the thread and the total written    | 52    |
// | toNote          | Projects a generated row onto the domain type                   | 75    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AddNote appends a progress note to a task.
//
// The insert is fed by a SELECT scoped on the task: if the task does not belong to the project, no
// row is produced, hence nothing is inserted and the call yields ErrNotFound. The scope is carried
// by the query, not by a prior check a caller could forget.
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
	// The write and the read no longer return the same generated row: ListTaskNotes also carries the
	// thread total. Projecting here rather than sharing toNote avoids inventing a common type for
	// two shapes that have no reason to stay identical.
	return Note{ID: row.ID, Body: row.BodyMd, CreatedAt: row.CreatedAt}, nil
}

// ListNotes returns the TAIL of a task's thread — at most limit notes — and the total number
// written.
//
// The query returns the most recent ones first, because those are the ones carrying the state;
// this function puts them back in write order, which is how a journal is read. The reversal lives
// here and not in the service: it belongs to the read contract announced by the type, not to a
// business decision.
//
// The total comes from the SAME query (count(*) OVER ()), so bounding the thread did not cost one
// more round trip on the product's most-called read path.
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

// toNote projects a generated row onto the domain type.
func toNote(row database.ListTaskNotesRow) Note {
	return Note{
		ID:        row.ID,
		Body:      row.BodyMd,
		CreatedAt: row.CreatedAt,
	}
}
