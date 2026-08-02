package service

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
)

// CreateTask ouvre une tâche dans le backlog du projet du token.
//
// La réservation du numéro et l'insertion se font dans UNE transaction : si l'insertion échoue,
// le compteur revient en arrière et le numéro n'est pas brûlé. Sans transaction, chaque
// création ratée laisserait un trou définitif dans la suite CORE-1, CORE-2, …
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

	// Un agent qui ouvre une tâche sans préciser l'état veut le cas nominal : à faire, priorité
	// normale. Exiger ces deux champs ne rendrait aucune écriture plus sûre.
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
		return nil
	})
	if err != nil {
		return Task{}, err
	}

	return toTask(created), nil
}
