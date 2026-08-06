package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                 | Ligne |
// |------------------------|--------------------------------------------------------|-------|
// | store.AddNote          | Appends a progress note, through a scoped SELECT         | 29    |
// | store.ChargeNoteBytes  | Debits the project quota, or refuses the write           | 56    |
// | store.ListNotes        | Returns the bounded tail of the thread and the total     | 82    |
// | toNote                 | Projects a generated row onto the domain type            | 105   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"

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

// ChargeNoteBytes debits the project's note quota by the size of one note, and refuses the debit
// that would cross ProjectNoteBytesQuota.
//
// The size charged is the byte length of the body, measured the same way Postgres measures it in
// the backfill of migration 000011 — `len()` on a Go string is `octet_length()` on a text column,
// both counting UTF-8 bytes. Counting runes here and octets there would let the counter drift on
// every accented character, which is to say on every French note the repository still carries.
//
// ZERO ROWS MEANS THE QUOTA, and nothing else: the project identifier comes from the authenticated
// token, so the row exists. Reporting ErrNotFound here would tell an agent its own project is
// gone, and send it looking in the wrong place.
func (s *store) ChargeNoteBytes(ctx context.Context, teamID, projectID uuid.UUID, bytes int64) error {
	_, err := s.q.ChargeProjectNoteBytes(ctx, database.ChargeProjectNoteBytesParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Bytes:     bytes,
		Quota:     ProjectNoteBytesQuota,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrQuotaExceeded
	}
	if err != nil {
		return translate(err, "charge note bytes")
	}
	return nil
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
