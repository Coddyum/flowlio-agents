package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | store.TaskByRef  | Yields an active task of the team, without its repo's token     | 26    |
// | store.TaskNotes  | Yields the last N notes, in reading order                       | 59    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// TaskByRef yields the task designated by its project key and its number. An archived task cannot
// be found this way: it calls for no action any more, and opening it from the overview would make
// no sense.
func (s *store) TaskByRef(ctx context.Context, teamID uuid.UUID, projectKey string, number int64) (Task, error) {
	row, err := s.q.OverviewTaskByRef(ctx, database.OverviewTaskByRefParams{
		TeamID:     teamID,
		ProjectKey: projectKey,
		Number:     number,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("overview store: task %s-%d of team %s: %w", projectKey, number, teamID, err)
	}

	task := Task{
		ID:         row.ID,
		Number:     row.Number,
		Status:     string(row.Status),
		Priority:   string(row.Priority),
		Title:      row.Title,
		BodyMd:     row.BodyMd,
		ProjectKey: row.ProjectKey,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
	if row.Deadline.Valid {
		deadline := row.Deadline.Time
		task.Deadline = &deadline
	}
	return task, nil
}

// TaskNotes yields the N most recent notes, in reading order, and the total before the bound.
// teamID bounds the read through the task: task_notes has no team_id column.
func (s *store) TaskNotes(ctx context.Context, teamID, taskID uuid.UUID, limit int32) ([]Note, int64, error) {
	rows, err := s.q.OverviewTaskNotes(ctx, database.OverviewTaskNotesParams{
		TeamID:  teamID,
		TaskID:  taskID,
		MaxRows: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: notes of task %s: %w", taskID, err)
	}

	out := make([]Note, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, Note{BodyMd: r.BodyMd, CreatedAt: r.CreatedAt})
	}
	return out, total, nil
}
