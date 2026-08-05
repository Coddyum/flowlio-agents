package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | store.AllowTrust     | Opens a trust pair, idempotent                           | 31    |
// | store.RevokeTrust    | Closes a trust pair, idempotent                          | 48    |
// | store.ListTrustEdges | Lists a team's graph, in readable keys                    | 62    |
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

// AllowTrust opens a pair, in both directions since the edge is symmetric.
//
// created tells "created" from "already allowed" without a second round trip. An unknown key, or
// one from another team, does not resolve: the query returns zero rows, hence ErrNotFound.
func (s *store) AllowTrust(ctx context.Context, teamID uuid.UUID, firstKey, secondKey string) (bool, error) {
	created, err := s.q.AllowTrust(ctx, database.AllowTrustParams{
		TeamID:    teamID,
		FirstKey:  firstKey,
		SecondKey: secondKey,
	})
	if err != nil {
		return false, translate(err, "allow trust "+firstKey+" ↔ "+secondKey)
	}
	return created, nil
}

// RevokeTrust closes a pair. removed is false when the pair existed but was not declared; a key
// that does not resolve yields ErrNotFound, not "nothing to remove".
//
// Removing a trust only forbids OPENING a new issue. Threads already open stay answerable: the
// product's circuit breaker is token revocation.
func (s *store) RevokeTrust(ctx context.Context, teamID uuid.UUID, firstKey, secondKey string) (bool, error) {
	removed, err := s.q.RevokeTrust(ctx, database.RevokeTrustParams{
		TeamID:    teamID,
		FirstKey:  firstKey,
		SecondKey: secondKey,
	})
	if err != nil {
		return false, translate(err, "revoke trust "+firstKey+" ↔ "+secondKey)
	}
	return removed, nil
}

// ListTrustEdges returns a team's graph in readable keys, sorted. An administration read: it is
// never served to a project token.
func (s *store) ListTrustEdges(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error) {
	rows, err := s.q.ListTrustEdges(ctx, teamID)
	if err != nil {
		return nil, translate(err, "list trust edges")
	}

	edges := make([]TrustEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, TrustEdge{
			FirstKey:  row.FirstKey,
			SecondKey: row.SecondKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}
