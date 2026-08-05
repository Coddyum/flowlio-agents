package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | service.AllowTrust   | Opens a trust pair between two projects                  | 43    |
// | service.RevokeTrust  | Closes a trust pair between two projects                 | 61    |
// | service.ListTrust    | Returns a team's trust graph                             | 78    |
// | normalisePair        | Validates and normalises the two keys of a pair          | 105   |
//
// Fin du sommaire.
// =====================================================================
//
// THIS FILE DECIDES NO AUTHORISATION.
//
// It edits a declaration; it is the WHERE predicate of CreateIssue (sql/queries/issues.sql) that
// enforces it, and it alone. The only validation here is that of two strings typed by a human —
// tenancy lives in the query, where a caller reaching the store directly cannot work around it.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// errSelfPair is the message returned to a human allowing a project with itself.
//
// This is input validation, not tenancy: the database would refuse anyway (its ordering CHECK
// excludes equality, see migration 000007), but it would return a 500 or a `not found` where a
// readable 400 says what to do. A project asking itself a question has no need for the
// cross-project channel: it has tasks.
var errSelfPair = errors.New(
	"a project cannot allow itself — a question to one's own repo is a task")

// AllowTrust opens a pair. Idempotent: replaying the command returns Changed as false.
//
// An unknown key, or one from another team, surfaces as ErrNotFound from the query — never from a
// check written here, which would have to resolve the keys itself to do so.
func (s *service) AllowTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	first, second, err := normalisePair(in)
	if err != nil {
		return TrustDecision{}, err
	}

	created, err := s.store.AllowTrust(ctx, in.TeamID, first, second)
	if err != nil {
		return TrustDecision{}, translateStore(err, "allow trust "+first+" "+second)
	}
	return TrustDecision{First: first, Second: second, Changed: created}, nil
}

// RevokeTrust closes a pair. Idempotent: replaying the command returns Changed as false.
//
// Removing a trust forbids OPENING a new issue, and nothing else. Threads already open stay
// readable and answerable: this is not a containment tool, it is a least-privilege declaration. The
// product's circuit breaker is token revocation.
func (s *service) RevokeTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	first, second, err := normalisePair(in)
	if err != nil {
		return TrustDecision{}, err
	}

	removed, err := s.store.RevokeTrust(ctx, in.TeamID, first, second)
	if err != nil {
		return TrustDecision{}, translateStore(err, "revoke trust "+first+" "+second)
	}
	return TrustDecision{First: first, Second: second, Changed: removed}, nil
}

// ListTrust returns a team's graph, sorted by keys.
//
// This is the only surface where the truth of the graph is readable, and the first command a human
// types when an agent has just been handed `not found` on a create_issue.
func (s *service) ListTrust(ctx context.Context, teamID uuid.UUID) ([]TrustEdge, error) {
	if teamID == uuid.Nil {
		return nil, ErrInvalidInput
	}

	rows, err := s.store.ListTrustEdges(ctx, teamID)
	if err != nil {
		return nil, translateStore(err, "list trust")
	}

	edges := make([]TrustEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, TrustEdge{
			First:     row.FirstKey,
			Second:    row.SecondKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}

// normalisePair validates and normalises both keys. Uppercase, as everywhere: `frnt` and `FRNT`
// name the same project, and letting case decide whether an edge exists would produce two graphs
// for a single intention.
//
// The equality comparison happens AFTER normalisation: without that, `trust allow frnt FRNT` would
// pass validation only to be refused by the database.
func normalisePair(in TrustPairInput) (string, string, error) {
	if in.TeamID == uuid.Nil {
		return "", "", ErrInvalidInput
	}

	first := strings.ToUpper(strings.TrimSpace(in.First))
	second := strings.ToUpper(strings.TrimSpace(in.Second))

	if err := validateKey(first); err != nil {
		return "", "", err
	}
	if err := validateKey(second); err != nil {
		return "", "", err
	}
	if first == second {
		return "", "", errors.Join(ErrInvalidInput, errSelfPair)
	}
	return first, second, nil
}
