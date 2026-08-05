package service

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// ListTasks returns the backlog of the token's project, newest number first.
//
// The full description is stripped from the list: it can run to tens of thousands of bytes per
// task, and an agent scanning its backlog would pay in context for what it does not read. GetTask
// returns it when it is actually asked for.
func (s *service) ListTasks(ctx context.Context, in ListTasksInput) ([]Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return nil, err
	}
	if in.Status != "" {
		if err := validateStatus(in.Status); err != nil {
			return nil, err
		}
	}

	rows, err := s.store.ListTasks(ctx, store.TaskFilter{
		TeamID:          in.TeamID,
		ProjectID:       in.ProjectID,
		Status:          in.Status,
		IncludeArchived: in.IncludeArchived,
		Limit:           clampLimit(in.Limit),
	})
	if err != nil {
		return nil, translateStore(err, "list tasks")
	}

	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		task := toTask(row)
		task.Body = ""
		tasks = append(tasks, task)
	}
	return tasks, nil
}
