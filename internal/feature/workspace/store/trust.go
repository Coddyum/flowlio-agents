package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | store.AllowTrust     | Opens one directed edge, idempotent                      | 32    |
// | store.RevokeTrust    | Cuts one directed edge and no other, idempotent          | 50    |
// | store.ListTrustEdges | Lists a team's graph, in readable keys, with directions   | 64    |
//
// Fin du sommaire.
// =====================================================================
//
// This file never NAMES the graph table: it calls the generated queries. The trust decision lives
// in the WHERE of CreateIssue, and administration in sql/queries/trust.sql. A `.go` needing the
// table name would be the sign that the decision has left the query — that is what
// scripts/check-trust-in-sql-only.sh guards, and it is why this very comment does not write that
// name either: an absolute rule applies, a rule with exceptions gets negotiated.

import (
	"context"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// AllowTrust opens ONE edge, in ONE direction: fromKey may from now on open a question at toKey.
// The reciprocal is a separate call, and the human decides whether to make it.
//
// created tells "created" from "already allowed" without a second round trip. An unknown key, or
// one from another team, does not resolve: the query returns zero rows, hence ErrNotFound.
func (s *store) AllowTrust(ctx context.Context, teamID uuid.UUID, fromKey, toKey string) (bool, error) {
	created, err := s.q.AllowTrust(ctx, database.AllowTrustParams{
		TeamID:  teamID,
		FromKey: fromKey,
		ToKey:   toKey,
	})
	if err != nil {
		return false, translate(err, "allow trust "+fromKey+" → "+toKey)
	}
	return created, nil
}

// RevokeTrust cuts that edge, and that edge only: the opposite direction stands if it was declared.
// removed is false when the edge existed but was not declared; a key that does not resolve yields
// ErrNotFound, not "nothing to remove".
//
// Removing a trust only forbids OPENING a new issue. Threads already open stay answerable: the
// product's circuit breaker is token revocation.
func (s *store) RevokeTrust(ctx context.Context, teamID uuid.UUID, fromKey, toKey string) (bool, error) {
	removed, err := s.q.RevokeTrust(ctx, database.RevokeTrustParams{
		TeamID:  teamID,
		FromKey: fromKey,
		ToKey:   toKey,
	})
	if err != nil {
		return false, translate(err, "revoke trust "+fromKey+" → "+toKey)
	}
	return removed, nil
}

// ListTrustEdges returns a team's graph in readable keys, sorted, one row PER DIRECTION. An
// administration read: it is never served to a project token.
func (s *store) ListTrustEdges(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error) {
	rows, err := s.q.ListTrustEdges(ctx, teamID)
	if err != nil {
		return nil, translate(err, "list trust edges")
	}

	edges := make([]TrustEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, TrustEdge{
			FromKey:   row.FromKey,
			ToKey:     row.ToKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}
