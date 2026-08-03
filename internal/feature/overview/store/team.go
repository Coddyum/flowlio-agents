package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TeamBySlug résout le scope de tout le module.
//
// C'est le seul point d'entrée d'un identifiant fourni par le client, et il n'accepte qu'un slug :
// aucun UUID ne traverse jamais cette surface, ni en entrée ni en sortie. Un slug inconnu rend
// ErrNotFound, que le handler traduit en 404 — le même 404 qu'une team qui existe mais n'est pas
// la sienne.
func (s *store) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	row, err := s.q.OverviewTeamBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Team{}, ErrNotFound
		}
		return Team{}, fmt.Errorf("overview store: team by slug %q: %w", slug, err)
	}

	return Team{ID: row.ID, Slug: row.Slug, Name: row.Name}, nil
}
