package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | store.ClaimNumber  | Réserve le prochain numéro lisible du projet                 | 39    |
// | store.CreateTask   | Insère une tâche dont le numéro est déjà réservé              | 51    |
// | store.TaskByNumber | Lit une tâche par son numéro, scopée team + projet            | 70    |
// | store.ListTasks    | Lit le backlog du projet selon un filtre                      | 83    |
// | store.UpdateTask   | Applique un patch partiel à une tâche active                  | 104   |
// | toTask             | Projette une ligne générée en type domaine                    | 136   |
// | nullTime           | Convertit un pointeur de date en paramètre nullable           | 154   |
// | fromNullTime       | Convertit une date nullable lue en base en pointeur            | 162   |
// | nullString         | Convertit un pointeur de chaîne en paramètre nullable         | 171   |
// | nullStatus         | Convertit un statut de filtre en paramètre nullable            | 180   |
// | nullStatusPtr      | Convertit un statut de patch en paramètre nullable             | 188   |
// | nullPriorityPtr    | Convertit une priorité de patch en paramètre nullable          | 196   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/google/uuid"
)

// ClaimNumber réserve le prochain numéro lisible du projet. Le UPDATE ... RETURNING verrouille
// la ligne du projet, ce qui sérialise les créations concurrentes : deux tâches ne peuvent pas
// obtenir le même numéro.
//
// Un projet qui n'appartient pas à la team est introuvable, donc le numéro n'est pas réservé :
// on ne peut pas faire avancer le compteur d'un projet tiers en le devinant.
func (s *store) ClaimNumber(ctx context.Context, teamID, projectID uuid.UUID) (int64, error) {
	number, err := s.q.ClaimNextNumber(ctx, database.ClaimNextNumberParams{
		ID:     projectID,
		TeamID: teamID,
	})
	if err != nil {
		return 0, translate(err, "claim number")
	}
	return number, nil
}

// CreateTask insère une tâche dont le numéro est déjà réservé.
func (s *store) CreateTask(ctx context.Context, in NewTask) (Task, error) {
	row, err := s.q.CreateTask(ctx, database.CreateTaskParams{
		TeamID:    in.TeamID,
		ProjectID: in.ProjectID,
		Number:    in.Number,
		Title:     in.Title,
		BodyMd:    in.Body,
		Status:    database.TaskStatus(in.Status),
		Priority:  database.TaskPriority(in.Priority),
		Deadline:  nullTime(in.Deadline),
	})
	if err != nil {
		return Task{}, translate(err, "create task")
	}
	return toTask(row), nil
}

// TaskByNumber lit une tâche par son numéro, toujours dans le scope team + projet. Une tâche
// d'un autre projet remonte ErrNotFound : introuvable, pas interdite.
func (s *store) TaskByNumber(ctx context.Context, teamID, projectID uuid.UUID, number int64) (Task, error) {
	row, err := s.q.GetTask(ctx, database.GetTaskParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
	})
	if err != nil {
		return Task{}, translate(err, "task by number")
	}
	return toTask(row), nil
}

// ListTasks lit le backlog du projet, du numéro le plus récent au plus ancien.
func (s *store) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error) {
	rows, err := s.q.ListTasks(ctx, database.ListTasksParams{
		TeamID:          filter.TeamID,
		ProjectID:       filter.ProjectID,
		IncludeArchived: filter.IncludeArchived,
		Status:          nullStatus(filter.Status),
		MaxRows:         filter.Limit,
	})
	if err != nil {
		return nil, translate(err, "list tasks")
	}

	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, toTask(row))
	}
	return tasks, nil
}

// UpdateTask applique un patch partiel. Une tâche archivée est hors de portée de la query et
// remonte donc ErrNotFound, comme un numéro inexistant.
func (s *store) UpdateTask(ctx context.Context, patch TaskPatch) (Task, error) {
	row, err := s.q.UpdateTask(ctx, database.UpdateTaskParams{
		TeamID:        patch.TeamID,
		ProjectID:     patch.ProjectID,
		Number:        patch.Number,
		Title:         nullString(patch.Title),
		BodyMd:        nullString(patch.Body),
		Status:        nullStatusPtr(patch.Status),
		Priority:      nullPriorityPtr(patch.Priority),
		Deadline:      nullTime(patch.Deadline),
		ClearDeadline: patch.ClearDeadline,
		Archive:       patch.Archive,
	})
	if err != nil {
		return Task{}, translate(err, "update task")
	}
	return toTask(row), nil
}

// toTask projette une ligne générée en type domaine.
func toTask(row database.Task) Task {
	return Task{
		ID:         row.ID,
		TeamID:     row.TeamID,
		ProjectID:  row.ProjectID,
		Number:     row.Number,
		Title:      row.Title,
		Body:       row.BodyMd,
		Status:     string(row.Status),
		Priority:   string(row.Priority),
		Deadline:   fromNullTime(row.Deadline),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		ArchivedAt: fromNullTime(row.ArchivedAt),
	}
}

// nullTime convertit un pointeur de date en paramètre nullable.
func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// fromNullTime convertit une date nullable lue en base en pointeur.
func fromNullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}

// nullString convertit un pointeur de chaîne en paramètre nullable.
func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// nullStatus convertit un statut de filtre en paramètre nullable : la chaîne vide signifie
// « tous les statuts ».
func nullStatus(status string) database.NullTaskStatus {
	if status == "" {
		return database.NullTaskStatus{}
	}
	return database.NullTaskStatus{TaskStatus: database.TaskStatus(status), Valid: true}
}

// nullStatusPtr convertit un statut de patch en paramètre nullable.
func nullStatusPtr(status *string) database.NullTaskStatus {
	if status == nil {
		return database.NullTaskStatus{}
	}
	return nullStatus(*status)
}

// nullPriorityPtr convertit une priorité de patch en paramètre nullable.
func nullPriorityPtr(priority *string) database.NullTaskPriority {
	if priority == nil {
		return database.NullTaskPriority{}
	}
	return database.NullTaskPriority{TaskPriority: database.TaskPriority(*priority), Valid: true}
}
