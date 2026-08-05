package service

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// ListIssues returns the issues the calling project can see: the ones it opened and the ones
// addressed to it, never any other.
//
// Filtering by role is a NARROWING of what is already visible. The visibility clause itself stays
// unconditional in the query: no combination of parameters can widen it.
func (s *service) ListIssues(ctx context.Context, in ListIssuesInput) ([]Issue, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := validateRole(in.Role); err != nil {
		return nil, err
	}
	if err := validateState(in.State); err != nil {
		return nil, err
	}

	// Explicitly asking for the `closed` state implies wanting closed issues: excluding them then
	// would always return an empty list, which reads as a bug.
	includeClosed := in.IncludeClosed || in.State == "closed"

	rows, err := s.store.ListIssues(ctx, store.IssueFilter{
		TeamID:        in.TeamID,
		ProjectID:     in.ProjectID,
		Role:          in.Role,
		State:         in.State,
		IncludeClosed: includeClosed,
		Limit:         clampLimit(in.Limit),
	})
	if err != nil {
		return nil, translateStore(err, "list issues")
	}
	return toIssues(rows), nil
}
