package service

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// ListTasks renvoie le backlog du projet du token, du numéro le plus récent au plus ancien.
//
// La description complète est retirée de la liste : elle peut faire des dizaines de milliers
// d'octets par tâche, et un agent qui parcourt son backlog paierait en contexte ce qu'il ne lit
// pas. GetTask la renvoie quand elle est réellement demandée.
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
