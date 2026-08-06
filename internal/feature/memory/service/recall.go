package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                        | Ligne |
// |----------------|---------------------------------------------------------------|-------|
// | service.Recall | Lists or searches a project's memory, bounded                   | 32    |
// | service.Get    | Reads one entry by its slug                                     | 66    |
// | service.Index  | Titles in force, for the MCP handshake                          | 87    |
// | trim           | Trims surrounding whitespace                                    | 105   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
	"github.com/google/uuid"
)

// Recall lists or searches, and the presence of a query decides which.
//
// One method for both because they answer the same question under the same scope and the same
// bound. Two would be two places to forget a predicate, and the predicate that matters here is the
// project.
//
// An empty result is an empty slice with a zero total, never an error: "this repository has
// remembered nothing about that" is an answer, and a 404 would have an agent conclude its search
// was malformed.
func (s *service) Recall(ctx context.Context, in RecallInput) (Recalled, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Recalled{}, err
	}
	if in.Kind != "" {
		if err := validateKind(in.Kind); err != nil {
			return Recalled{}, err
		}
	}

	entries, total, err := s.store.List(ctx, store.Filter{
		TeamID:            in.TeamID,
		ProjectID:         in.ProjectID,
		Kind:              in.Kind,
		Query:             trim(in.Query),
		IncludeSuperseded: in.IncludeSuperseded,
		Limit:             boundLimit(in.Limit),
	})
	if err != nil {
		return Recalled{}, translateStore(err, "recall")
	}

	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = toEntry(e)
	}
	return Recalled{Entries: out, Total: total}, nil
}

// Get reads one entry by its slug.
//
// A slug of another project is an ErrNotFound, exactly like one that was never written: the query
// carries the scope, so the row is UNFINDABLE rather than forbidden. Telling the two apart would
// let a sibling's registry be probed one slug at a time.
func (s *service) Get(ctx context.Context, teamID, projectID uuid.UUID, slug string) (Entry, error) {
	if err := validateScope(teamID, projectID); err != nil {
		return Entry{}, err
	}
	if err := validateSlug(slug); err != nil {
		return Entry{}, err
	}

	found, err := s.store.BySlug(ctx, teamID, projectID, slug)
	if err != nil {
		return Entry{}, translateStore(err, "get "+slug)
	}
	return toEntry(found), nil
}

// Index returns the titles in force, for the MCP handshake.
//
// Bounded much tighter than a reading, and for a different reason: this is paid on EVERY session,
// in the agent's context, before its first message. It is also why it carries titles and no
// bodies — an index holding the bodies IS the memory, and there would be nothing left to read on
// demand.
func (s *service) Index(ctx context.Context, teamID, projectID uuid.UUID) ([]IndexLine, error) {
	if err := validateScope(teamID, projectID); err != nil {
		return nil, err
	}

	lines, err := s.store.Index(ctx, teamID, projectID, indexLimit)
	if err != nil {
		return nil, translateStore(err, "memory index")
	}

	out := make([]IndexLine, len(lines))
	for i, l := range lines {
		out[i] = IndexLine{Slug: l.Slug, Kind: l.Kind, Title: l.Title}
	}
	return out, nil
}

// trim removes the whitespace around a value written by hand.
func trim(v string) string { return strings.TrimSpace(v) }
