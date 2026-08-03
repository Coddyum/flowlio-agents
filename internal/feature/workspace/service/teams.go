package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | service.CreateTeam | Valide puis crée une team                                  | 23    |
// | service.ListTeams  | Liste les teams existantes                                 | 43    |
// | service.TeamBySlug | Résout une team par son slug                               | 57    |
// | toTeam             | Projette une team du store en vue API                      | 66    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
)

// CreateTeam valide le slug et le nom, puis crée la team. Un slug déjà pris remonte ErrConflict.
func (s *service) CreateTeam(ctx context.Context, in CreateTeamInput) (Team, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	name := strings.TrimSpace(in.Name)

	if err := validateSlug(slug); err != nil {
		return Team{}, err
	}
	if err := validateName("nom de team", name); err != nil {
		return Team{}, err
	}

	created, err := s.store.CreateTeam(ctx, slug, name)
	if err != nil {
		return Team{}, translateStore(err, "create team "+slug)
	}
	return toTeam(created), nil
}

// ListTeams liste les teams. Réservé aux tokens admin : un token de projet n'a aucune raison de
// connaître les autres teams.
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

// TeamBySlug résout une team par son slug, pour que la CLI n'ait jamais à manipuler d'UUID.
func (s *service) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	found, err := s.store.TeamBySlug(ctx, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return Team{}, translateStore(err, "team "+slug)
	}
	return toTeam(found), nil
}

// toTeam projette une team du store en vue API.
func toTeam(t store.Team) Team {
	return Team{ID: t.ID, Slug: t.Slug, Name: t.Name, CreatedAt: t.CreatedAt}
}
