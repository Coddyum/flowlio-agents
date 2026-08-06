package service

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// CreateTask opens a task in the backlog of the token's project.
//
// Reserving the number and inserting happen in ONE transaction: should the insert fail, the
// counter rolls back and the number is not burnt. Without a transaction, every failed creation
// would leave a permanent hole in the CORE-1, CORE-2, … sequence.
func (s *service) CreateTask(ctx context.Context, in CreateTaskInput) (Task, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Task{}, err
	}

	title := strings.TrimSpace(in.Title)
	if err := validateTitle(title); err != nil {
		return Task{}, err
	}
	if err := validateBody("description", in.Body); err != nil {
		return Task{}, err
	}
	if err := validateDeadline(in.Deadline); err != nil {
		return Task{}, err
	}

	// An agent opening a task without naming a state wants the nominal case: to do, normal
	// priority. Requiring those two fields would make no write any safer.
	status := in.Status
	if status == "" {
		status = "todo"
	}
	if err := validateStatus(status); err != nil {
		return Task{}, err
	}

	priority := in.Priority
	if priority == "" {
		priority = "normal"
	}
	if err := validatePriority(priority); err != nil {
		return Task{}, err
	}

	var created store.Task
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		number, err := tx.ClaimNumber(ctx, in.TeamID, in.ProjectID)
		if err != nil {
			return translateStore(err, "claim number")
		}

		created, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    in.TeamID,
			ProjectID: in.ProjectID,
			Number:    number,
			Title:     title,
			Body:      in.Body,
			Status:    status,
			Priority:  priority,
			Deadline:  in.Deadline,
		})
		if err != nil {
			return translateStore(err, "create task")
		}

		// The description is compiled AFTER the insert, and it cannot be otherwise: a `#blocked-by`
		// line names the task it blocks, which has no identifier until this point. An unreadable line
		// rolls the whole thing back, number included — the task is not created half-blocked.
		created, err = s.syncBodyEdges(ctx, tx, bodyEdges{
			task:           created,
			next:           in.Body,
			statusExplicit: in.Status != "",
		})
		return err
	})
	if err != nil {
		return Task{}, err
	}

	return toTask(created), nil
}
