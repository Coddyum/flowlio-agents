package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TeamBySlug resolves the scope of the whole module.
//
// It is the only entry point for a client-supplied identifier, and it accepts nothing but a slug:
// no UUID ever crosses this surface, neither in nor out. An unknown slug yields ErrNotFound, which
// the handler translates into a 404 — the same 404 as a team that exists but is not the caller's.
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
