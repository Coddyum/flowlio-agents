package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | service.CreateTeam | Validates then creates a team                              | 23    |
// | service.ListTeams  | Lists the existing teams                                   | 43    |
// | service.TeamBySlug | Resolves a team by its slug                                | 57    |
// | toTeam             | Projects a store team onto the API view                    | 66    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// CreateTeam validates the slug and the name, then creates the team. A slug already taken yields
// ErrConflict.
func (s *service) CreateTeam(ctx context.Context, in CreateTeamInput) (Team, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	name := strings.TrimSpace(in.Name)

	if err := validateSlug(slug); err != nil {
		return Team{}, err
	}
	if err := validateName("team name", name); err != nil {
		return Team{}, err
	}

	created, err := s.store.CreateTeam(ctx, slug, name)
	if err != nil {
		return Team{}, translateStore(err, "create team "+slug)
	}
	return toTeam(created), nil
}

// ListTeams lists the teams. Admin tokens only: a project token has no reason to know about the
// other teams.
func (s *service) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := s.store.ListTeams(ctx)
	if err != nil {
		return nil, translateStore(err, "list teams")
	}

	teams := make([]Team, 0, len(rows))
	for _, row := range rows {
		teams = append(teams, toTeam(row))
	}
	return teams, nil
}

// TeamBySlug resolves a team by its slug, so that the CLI never has to handle a UUID.
func (s *service) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	found, err := s.store.TeamBySlug(ctx, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return Team{}, translateStore(err, "team "+slug)
	}
	return toTeam(found), nil
}

// toTeam projects a store team onto the API view.
func toTeam(t store.Team) Team {
	return Team{ID: t.ID, Slug: t.Slug, Name: t.Name, CreatedAt: t.CreatedAt}
}
