package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | store.TaskByRef  | Rend une tâche active de la team, sans le token de son repo     | 26    |
// | store.TaskNotes  | Rend les N dernières notes, dans l'ordre de lecture             | 59    |
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

// TaskByRef rend la tâche désignée par la clé de son projet et son numéro. Une tâche archivée est
// introuvable par cette voie : elle n'appelle plus d'action, et l'ouvrir depuis l'aperçu n'aurait
// pas de sens.
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

// TaskNotes rend les N notes les plus récentes, dans l'ordre de lecture, et le total avant la
// borne. teamID borne la lecture par la tâche : task_notes n'a pas de colonne team_id.
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
