package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                  | Ligne |
// |----------------------|---------------------------------------------------------|-------|
// | service.AllowTrust   | Opens one directed edge between two projects             | 51    |
// | service.RevokeTrust  | Cuts one directed edge, and that edge only               | 70    |
// | service.ListTrust    | Returns a team's trust graph, direction included         | 89    |
// | normaliseEdge        | Validates and normalises the two ends of an edge         | 120   |
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

// errSelfEdge is the message returned to a human pointing an edge from a project at itself.
//
// This is input validation, not tenancy: the database would refuse anyway (its self-edge CHECK,
// added by migration 000013 to replace the ordering constraint that used to exclude equality for
// free), but it would return a 500 or a `not found` where a readable 400 says what to do. A project
// asking itself a question has no need for the cross-project channel: it has tasks.
//
// The constraint is named in the migration, never here: naming it would put the graph's table in a
// service file, which is what scripts/check-trust-in-sql-only.sh refuses — and it refused exactly
// this line on the first attempt.
var errSelfEdge = errors.New(
	"a project cannot trust itself — a question to one's own repo is a task")

// AllowTrust opens ONE edge, in ONE direction: in.From may from now on open a question at in.To.
// Idempotent: replaying the command returns Changed as false.
//
// The reciprocal is NOT opened. `allow WEB CORE` followed by `allow CORE WEB` is two commands and
// two rows, and a graph that carries only the first is a legal, meaningful state — that is what
// migration 000013 bought.
//
// An unknown key, or one from another team, surfaces as ErrNotFound from the query — never from a
// check written here, which would have to resolve the keys itself to do so.
func (s *service) AllowTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	from, to, err := normaliseEdge(in)
	if err != nil {
		return TrustDecision{}, err
	}

	created, err := s.store.AllowTrust(ctx, in.TeamID, from, to)
	if err != nil {
		return TrustDecision{}, translateStore(err, "allow trust "+from+" "+to)
	}
	return TrustDecision{From: from, To: to, Changed: created}, nil
}

// RevokeTrust cuts ONE edge, and that edge only: the opposite direction stands if it was declared.
// Idempotent: replaying the command returns Changed as false.
//
// Removing a trust forbids OPENING a new issue, and nothing else. Threads already open stay
// readable and answerable: this is not a containment tool, it is a least-privilege declaration. The
// product's circuit breaker is token revocation.
func (s *service) RevokeTrust(ctx context.Context, in TrustPairInput) (TrustDecision, error) {
	from, to, err := normaliseEdge(in)
	if err != nil {
		return TrustDecision{}, err
	}

	removed, err := s.store.RevokeTrust(ctx, in.TeamID, from, to)
	if err != nil {
		return TrustDecision{}, translateStore(err, "revoke trust "+from+" "+to)
	}
	return TrustDecision{From: from, To: to, Changed: removed}, nil
}

// ListTrust returns a team's graph, sorted by keys, ONE ENTRY PER DIRECTION.
//
// This is the only surface where the truth of the graph is readable, and the first command a human
// types when an agent has just been handed `not found` on a create_issue. Since the graph is
// directed, "CORE appears in the list" is no longer an answer to "may CORE ask WEB" — which side of
// the edge CORE sits on is the answer, and this is where it comes from.
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
			From:      row.FromKey,
			To:        row.ToKey,
			CreatedAt: row.CreatedAt,
		})
	}
	return edges, nil
}

// normaliseEdge validates and normalises both ends. Uppercase, as everywhere: `frnt` and `FRNT`
// name the same project, and letting case decide whether an edge exists would produce two graphs
// for a single intention.
//
// It returns them IN THE ORDER GIVEN, and that is the point: the first is the sender, the second the
// recipient. Sorting them — which a "normalise" is tempted to do — would silently turn every command
// into the same edge, and `deny CORE WEB` would cut `WEB → CORE`.
//
// The equality comparison happens AFTER normalisation: without that, `trust allow frnt FRNT` would
// pass validation only to be refused by the database.
func normaliseEdge(in TrustPairInput) (string, string, error) {
	if in.TeamID == uuid.Nil {
		return "", "", ErrInvalidInput
	}

	from := strings.ToUpper(strings.TrimSpace(in.From))
	to := strings.ToUpper(strings.TrimSpace(in.To))

	if err := validateKey(from); err != nil {
		return "", "", err
	}
	if err := validateKey(to); err != nil {
		return "", "", err
	}
	if from == to {
		return "", "", errors.Join(ErrInvalidInput, errSelfEdge)
	}
	return from, to, nil
}
