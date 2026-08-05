package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | store.IssueDebts | Yields the team's issues in flight, the oldest one first        | 28    |
// | store.TaskDebts  | Yields blocked or dormant tasks, the oldest one first           | 59    |
//
// Fin du sommaire.
// =====================================================================
//
// BOTH METHODS YIELD THE TOTAL BEFORE THE BOUND. The total comes from `count(*) OVER ()`, so from
// a count done by the same query, over the same snapshot: a second round trip could count a state
// different from the one it announces.

import (
	"context"
	"fmt"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// IssueDebts yields the team's issues in flight, the OLDEST one first, and the total before the
// bound. Zero rows yields a total of zero: the total only exists as carried by a row.
func (s *store) IssueDebts(ctx context.Context, teamID uuid.UUID, limit int32) ([]IssueDebt, int64, error) {
	rows, err := s.q.OverviewIssueDebts(ctx, database.OverviewIssueDebtsParams{
		TeamID:  teamID,
		MaxRows: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: issue debts of team %s: %w", teamID, err)
	}

	out := make([]IssueDebt, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, IssueDebt{
			Number:           r.Number,
			State:            string(r.State),
			Title:            r.Title,
			ProjectKey:       r.ProjectKey,
			AuthorProjectKey: r.AuthorProjectKey,
			UpdatedAt:        r.UpdatedAt,
		})
	}
	return out, total, nil
}

// TaskDebts yields the tasks a human can act on: every blocked one, and the tasks in progress
// whose last move precedes staleBefore.
//
// staleBefore is computed by the service, never here: the clock belongs to the service, the scope
// to the query. That is what makes the integration test deterministic and the threshold tunable
// without a migration.
func (s *store) TaskDebts(ctx context.Context, teamID uuid.UUID, staleBefore time.Time, limit int32) ([]TaskDebt, int64, error) {
	rows, err := s.q.OverviewTaskDebts(ctx, database.OverviewTaskDebtsParams{
		TeamID:      teamID,
		StaleBefore: staleBefore,
		MaxRows:     limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("overview store: task debts of team %s: %w", teamID, err)
	}

	out := make([]TaskDebt, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		debt := TaskDebt{
			Number:          r.Number,
			Status:          string(r.Status),
			Priority:        string(r.Priority),
			Title:           r.Title,
			ProjectKey:      r.ProjectKey,
			LastMove:        r.LastMove,
			HasOpenQuestion: r.HasOpenQuestion,
		}
		if r.Deadline.Valid {
			deadline := r.Deadline.Time
			debt.Deadline = &deadline
		}
		out = append(out, debt)
	}
	return out, total, nil
}
