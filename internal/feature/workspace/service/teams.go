package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | service.CreateTeam | Validates then creates a team                              | 25    |
// | service.ListTeams  | Lists the existing teams                                   | 64    |
// | service.TeamBySlug | Resolves a team by its slug                                | 86    |
// | toTeam             | Projects a store team onto the API view                    | 95    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
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

// ListTeams enumerates the teams visible to the caller. Admin tokens only: a project token has no
// reason to know about the other teams.
//
// THIS WAS THE ONLY READ OF THE REPOSITORY THAT CARRIED NO TENANCY SCOPE, and store.go's own
// contract calls that "an isolation hole, not an ergonomics oversight". Under a shared
// installation it enumerates every customer of the host, slug and name included, from a single
// admin token.
//
// `pinned` is the caller's own team. Not nil ⇒ exactly one team is returned, read by its
// identifier — the scope is the `WHERE id = $1` of the query, not a filter applied afterwards on
// a full list. Nil ⇒ the caller is bound to no team and reads the whole installation, which is
// what an unpinned admin token is today (constraint `tokens_scope_shape`, migration 000006).
//
// SO THIS BRANCH IS DEAD FOR EVERY TOKEN THE DATABASE CAN CURRENTLY ISSUE, and it is written all
// the same, for the same reason `teamFor` guards the same shape: the third scope (`team`) will
// reopen that constraint, and the day it does, this surface must already be behind the boundary
// rather than be discovered outside it. A defence resting on a constraint written in another file
// is not a defence.
//
// MUTATION: remove the `pinned != uuid.Nil` branch → `TestListTeamsIsScopedToAPinnedAdmin` goes
// red.
func (s *service) ListTeams(ctx context.Context, pinned uuid.UUID) ([]Team, error) {
	if pinned != uuid.Nil {
		own, err := s.store.TeamByID(ctx, pinned)
		if err != nil {
			return nil, translateStore(err, "list teams")
		}
		return []Team{toTeam(own)}, nil
	}

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
