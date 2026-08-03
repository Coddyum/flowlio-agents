package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                    | Ligne |
// |------------------|-----------------------------------------------------------|-------|
// | store.CreateTeam | Insère une team et renvoie sa forme domaine                | 24    |
// | store.TeamByID   | Lit une team par son identifiant                           | 33    |
// | store.TeamBySlug | Lit une team par son slug                                  | 42    |
// | store.ListTeams  | Liste toutes les teams, par ancienneté                     | 51    |
// | toTeam           | Projette une ligne sqlc en team domaine                    | 65    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateTeam insère une team. Un slug déjà pris remonte en ErrConflict.
func (s *store) CreateTeam(ctx context.Context, slug, name string) (Team, error) {
	row, err := s.q.CreateTeam(ctx, database.CreateTeamParams{Slug: slug, Name: name})
	if err != nil {
		return Team{}, translate(err, "create team")
	}
	return toTeam(row), nil
}

// TeamByID lit une team par son identifiant.
func (s *store) TeamByID(ctx context.Context, id uuid.UUID) (Team, error) {
	row, err := s.q.GetTeamByID(ctx, id)
	if err != nil {
		return Team{}, translate(err, "team by id")
	}
	return toTeam(row), nil
}

// TeamBySlug lit une team par son slug, l'identifiant lisible utilisé en CLI.
func (s *store) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	row, err := s.q.GetTeamBySlug(ctx, slug)
	if err != nil {
		return Team{}, translate(err, "team by slug")
	}
	return toTeam(row), nil
}

// ListTeams liste les teams par ancienneté.
func (s *store) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := s.q.ListTeams(ctx)
	if err != nil {
		return nil, translate(err, "list teams")
	}

	teams := make([]Team, 0, len(rows))
	for _, row := range rows {
		teams = append(teams, toTeam(row))
	}
	return teams, nil
}

// toTeam projette une ligne générée en type domaine, pour que sqlc ne dépasse pas du store.
func toTeam(row database.Team) Team {
	return Team{
		ID:        row.ID,
		Slug:      row.Slug,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}
}
