package service

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
)

// ListIssues renvoie les issues visibles par le projet appelant : celles qu'il a ouvertes et
// celles qui lui sont adressées, jamais les autres.
//
// Le filtrage par rôle est une RESTRICTION de ce qui est déjà visible. La clause de visibilité,
// elle, reste inconditionnelle dans la query : aucune combinaison de paramètres ne peut
// l'élargir.
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

	// Demander explicitement l'état `closed` implique de vouloir les issues closes : les exclure
	// alors renverrait toujours une liste vide, ce qui se lit comme un bug.
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
