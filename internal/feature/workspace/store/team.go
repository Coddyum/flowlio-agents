package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                    | Ligne |
// |------------------|-----------------------------------------------------------|-------|
// | store.CreateTeam | Inserts a team and returns its domain shape                | 25    |
// | store.TeamByID   | Reads a team by its identifier                             | 34    |
// | store.TeamBySlug | Reads a team by its slug                                   | 43    |
// | store.ListTeams  | Lists every team, oldest first                             | 52    |
// | store.DeleteTeam | Removes a team and everything inside it, in cascade         | 76    |
// | toTeam           | Projects an sqlc row onto the domain team                  | 84    |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// CreateTeam inserts a team. A slug already taken yields ErrConflict.
func (s *store) CreateTeam(ctx context.Context, slug, name string) (Team, error) {
	row, err := s.q.CreateTeam(ctx, database.CreateTeamParams{Slug: slug, Name: name})
	if err != nil {
		return Team{}, translate(err, "create team")
	}
	return toTeam(row), nil
}

// TeamByID reads a team by its identifier.
func (s *store) TeamByID(ctx context.Context, id uuid.UUID) (Team, error) {
	row, err := s.q.GetTeamByID(ctx, id)
	if err != nil {
		return Team{}, translate(err, "team by id")
	}
	return toTeam(row), nil
}

// TeamBySlug reads a team by its slug, the readable identifier used in the CLI.
func (s *store) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	row, err := s.q.GetTeamBySlug(ctx, slug)
	if err != nil {
		return Team{}, translate(err, "team by slug")
	}
	return toTeam(row), nil
}

// ListTeams lists the teams, oldest first.
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

// DeleteTeam removes a team and, by cascade, everything that belongs to it.
//
// THE ABSENCE OF A BLOCKER REPORT IS THE DESIGN, not an omission. DeleteProject returns a
// ProjectDeletion because a repo can be talked to by a repo that survives it; a team cannot. Every
// thread has both of its ends inside one team — the composite foreign keys of `issues` see to that
// — so a team's deletion takes both halves of everything it touches and leaves nobody holding an
// empty shell.
//
// sql.ErrNoRows here means "no such team", and `translate` turns it into ErrNotFound. That is the
// only reason the query returns the identifier it just deleted: without a row to miss, a delete
// that matched nothing would be reported as a success.
func (s *store) DeleteTeam(ctx context.Context, id uuid.UUID) error {
	if _, err := s.q.DeleteTeam(ctx, id); err != nil {
		return translate(err, "delete team")
	}
	return nil
}

// toTeam projects a generated row onto the domain type, so that sqlc never spills out of the store.
func toTeam(row database.Team) Team {
	return Team{
		ID:        row.ID,
		Slug:      row.Slug,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}
}
