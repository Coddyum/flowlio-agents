package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | store.ClaimNumber  | Reserves the project's next readable number                  | 37    |
// | store.CreateTask   | Inserts a task whose number is already reserved              | 49    |
// | store.TaskByNumber | Reads a task by its number, scoped by team + project         | 68    |
// | store.ListTasks    | Reads the project backlog through a filter                   | 81    |
// | store.UpdateTask   | Applies a partial patch to an active task                    | 102   |
// | toTask             | Projects a generated row onto the domain type                | 122   |
// | nullTime           | Turns a date pointer into a nullable parameter               | 140   |
// | fromNullTime       | Turns a nullable date read from the database into a pointer  | 148   |
// | nullString         | Turns a string pointer into a nullable parameter             | 157   |
// | nullStatus         | Turns a filter status into a nullable parameter              | 166   |
// | nullStatusPtr      | Turns a patch status into a nullable parameter               | 174   |
// | nullPriorityPtr    | Turns a patch priority into a nullable parameter             | 182   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// ClaimNumber reserves the project's next readable number. The UPDATE ... RETURNING locks the
// project row, which serialises concurrent creations: two tasks cannot obtain the same number.
//
// A project that does not belong to the team is not found, so no number is reserved: nobody can
// push a third-party project's counter forward by guessing it.
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

// CreateTask inserts a task whose number is already reserved.
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

// TaskByNumber reads a task by its number, always within the team + project scope. A task of
// another project yields ErrNotFound: not found, not forbidden.
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

// ListTasks reads the project backlog, newest number first.
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

// UpdateTask applies a partial patch. An archived task is out of the query's reach and therefore
// yields ErrNotFound, like a non-existent number.
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

// toTask projects a generated row onto the domain type.
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

// nullTime turns a date pointer into a nullable parameter.
func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// fromNullTime turns a nullable date read from the database into a pointer.
func fromNullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}

// nullString turns a string pointer into a nullable parameter.
func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// nullStatus turns a filter status into a nullable parameter: the empty string means "every
// status".
func nullStatus(status string) database.NullTaskStatus {
	if status == "" {
		return database.NullTaskStatus{}
	}
	return database.NullTaskStatus{TaskStatus: database.TaskStatus(status), Valid: true}
}

// nullStatusPtr turns a patch status into a nullable parameter.
func nullStatusPtr(status *string) database.NullTaskStatus {
	if status == nil {
		return database.NullTaskStatus{}
	}
	return nullStatus(*status)
}

// nullPriorityPtr turns a patch priority into a nullable parameter.
func nullPriorityPtr(priority *string) database.NullTaskPriority {
	if priority == nil {
		return database.NullTaskPriority{}
	}
	return database.NullTaskPriority{TaskPriority: database.TaskPriority(*priority), Valid: true}
}
