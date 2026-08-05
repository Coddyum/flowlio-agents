package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                           | Ligne |
// |----------------|------------------------------------------------------------------|-------|
// | store.Projects | Yields one counter row per repo, never omitting a single one      | 23    |
// | store.LastSeen | Yields the pulse of the repos whose token has already served      | 46    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Projects yields one row per repo of the team, ALWAYS, including for a repo with nothing in
// flight. No bound is applied here and none must ever be added: the list of projects is the
// supervisor's map.
func (s *store) Projects(ctx context.Context, teamID uuid.UUID) ([]ProjectCounters, error) {
	rows, err := s.q.OverviewProjects(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("overview store: projects of team %s: %w", teamID, err)
	}

	out := make([]ProjectCounters, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectCounters{
			Key:            r.Key,
			OwesAnswer:     r.OwesAnswer,
			AwaitingAnswer: r.AwaitingAnswer,
			AnsweredUnread: r.AnsweredUnread,
			TasksRunning:   r.TasksRunning,
			TasksBlocked:   r.TasksBlocked,
		})
	}
	return out, nil
}

// LastSeen yields the pulse of the repos at least one token of which has already served. A repo
// missing from the result has no pulse: merging by key is the service's job, not the query's job
// to fabricate a zero timestamp that would read like a date.
func (s *store) LastSeen(ctx context.Context, teamID uuid.UUID) ([]ProjectPulse, error) {
	rows, err := s.q.OverviewLastSeen(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("overview store: last seen of team %s: %w", teamID, err)
	}

	out := make([]ProjectPulse, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectPulse{Key: r.Key, LastSeen: r.LastSeen})
	}
	return out, nil
}
