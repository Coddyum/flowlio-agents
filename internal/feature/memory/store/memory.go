package store

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | store.Create     | Inserts an entry through a scoped SELECT on projects             | 39    |
// | store.BySlug     | Reads one entry of the caller's project                          | 59    |
// | store.List       | Lists or searches, depending on the filter                       | 86    |
// | store.list       | Plain listing, entries in force unless asked otherwise           | 94    |
// | store.search     | Full-text search, ranked                                         | 127   |
// | store.Supersede  | Marks an entry as replaced by another                            | 165   |
// | store.Index      | Titles only, for the MCP handshake                               | 192   |
// | store.ChargeBytes| Debits the memory quota, or refuses the write                    | 218   |
// | splitSlugs       | Turns the aggregated supersedes column back into a slice         | 243   |
// | nullKind         | Turns an optional kind into the nullable sqlc parameter          | 252   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/google/uuid"
)

// Create inserts an entry.
//
// The insert is fed by a SELECT scoped on `projects`: a pair (team, project) that does not exist
// produces no row, hence nothing is inserted and the call yields ErrNotFound. The scope is carried
// by the statement, not by a check some caller could forget.
//
// The returned entry carries no supersession: it was just born, nothing replaced it and it has not
// yet replaced anything. The service fills that in when it supersedes.
func (s *store) Create(ctx context.Context, teamID, projectID uuid.UUID, slug, kind, title, body string) (Entry, error) {
	row, err := s.q.CreateMemory(ctx, database.CreateMemoryParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Slug:      slug,
		Kind:      database.MemoryKind(kind),
		Title:     title,
		BodyMd:    body,
	})
	if err != nil {
		return Entry{}, translate(err, "create memory")
	}
	return Entry{
		ID: row.ID, Slug: row.Slug, Kind: string(row.Kind),
		Title: row.Title, Body: row.BodyMd,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// BySlug reads one entry of the caller's project.
func (s *store) BySlug(ctx context.Context, teamID, projectID uuid.UUID, slug string) (Entry, error) {
	row, err := s.q.MemoryBySlug(ctx, database.MemoryBySlugParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Slug:      slug,
	})
	if err != nil {
		return Entry{}, translate(err, "memory by slug")
	}
	return Entry{
		ID: row.ID, Slug: row.Slug, Kind: string(row.Kind),
		Title: row.Title, Body: row.BodyMd,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		SupersededBy: row.SupersededBy,
		Supersedes:   splitSlugs(row.Supersedes),
	}, nil
}

// List returns the entries matching the filter, and the total BEFORE the bound.
//
// One entry point for two queries, and the branch is the presence of a search expression. They are
// kept apart in SQL because only one of them can use the GIN index, and folded together here
// because they answer the same question under the same scope: a caller that had to choose would be
// a caller that could choose wrong.
//
// The total comes from `count(*) OVER ()` in both, so knowing how much was left out costs no
// second round trip.
func (s *store) List(ctx context.Context, filter Filter) ([]Entry, int, error) {
	if filter.Query != "" {
		return s.search(ctx, filter)
	}
	return s.list(ctx, filter)
}

// list is the plain reading: the entries of a project, most recent first.
func (s *store) list(ctx context.Context, filter Filter) ([]Entry, int, error) {
	rows, err := s.q.ListMemories(ctx, database.ListMemoriesParams{
		TeamID:            filter.TeamID,
		ProjectID:         filter.ProjectID,
		IncludeSuperseded: filter.IncludeSuperseded,
		Kind:              nullKind(filter.Kind),
		MaxRows:           filter.Limit,
	})
	if err != nil {
		return nil, 0, translate(err, "list memories")
	}
	if len(rows) == 0 {
		return []Entry{}, 0, nil
	}

	entries := make([]Entry, len(rows))
	for i, row := range rows {
		entries[i] = Entry{
			ID: row.ID, Slug: row.Slug, Kind: string(row.Kind),
			Title: row.Title, Body: row.BodyMd,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			SupersededBy: row.SupersededBy,
			Supersedes:   splitSlugs(row.Supersedes),
		}
	}
	return entries, int(rows[0].Total), nil
}

// search ranks the entries matching the expression, best first.
//
// The rank is deliberately NOT carried up: it is an artefact of one query against one corpus, it
// has no meaning across two searches, and exposing it would invite a caller to threshold on it.
// What the caller needs is the ORDER, and the order is in the slice.
func (s *store) search(ctx context.Context, filter Filter) ([]Entry, int, error) {
	rows, err := s.q.SearchMemories(ctx, database.SearchMemoriesParams{
		TeamID:            filter.TeamID,
		ProjectID:         filter.ProjectID,
		IncludeSuperseded: filter.IncludeSuperseded,
		Kind:              nullKind(filter.Kind),
		Query:             filter.Query,
		MaxRows:           filter.Limit,
	})
	if err != nil {
		return nil, 0, translate(err, "search memories")
	}
	if len(rows) == 0 {
		return []Entry{}, 0, nil
	}

	entries := make([]Entry, len(rows))
	for i, row := range rows {
		entries[i] = Entry{
			ID: row.ID, Slug: row.Slug, Kind: string(row.Kind),
			Title: row.Title, Body: row.BodyMd,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			SupersededBy: row.SupersededBy,
			Supersedes:   splitSlugs(row.Supersedes),
		}
	}
	return entries, int(rows[0].Total), nil
}

// Supersede marks oldSlug as replaced by newSlug.
//
// The new entry is resolved BY SLUG, under the caller's own scope, and its identifier never
// crosses this function's boundary. That is what makes "supersede a sibling's entry"
// inexpressible rather than merely refused: there is no shape of the call that names one.
//
// An entry already superseded yields ErrNotFound, because the query's `superseded_by IS NULL`
// matches nothing. That is the intended answer: an entry has exactly one successor, and a second
// attempt is either a mistake or a race.
func (s *store) Supersede(ctx context.Context, teamID, projectID uuid.UUID, oldSlug, newSlug string) error {
	successor, err := s.q.MemoryBySlug(ctx, database.MemoryBySlugParams{
		TeamID: teamID, ProjectID: projectID, Slug: newSlug,
	})
	if err != nil {
		return translate(err, "supersede: successor")
	}
	target, err := s.q.MemoryBySlug(ctx, database.MemoryBySlugParams{
		TeamID: teamID, ProjectID: projectID, Slug: oldSlug,
	})
	if err != nil {
		return translate(err, "supersede: target")
	}

	_, err = s.q.SupersedeMemory(ctx, database.SupersedeMemoryParams{
		TeamID:       teamID,
		ProjectID:    projectID,
		ID:           target.ID,
		SupersededBy: uuid.NullUUID{UUID: successor.ID, Valid: true},
	})
	if err != nil {
		return translate(err, "supersede")
	}
	return nil
}

// Index returns titles only, for the MCP handshake — entries in force, nothing else.
func (s *store) Index(ctx context.Context, teamID, projectID uuid.UUID, limit int32) ([]IndexLine, error) {
	rows, err := s.q.MemoryIndex(ctx, database.MemoryIndexParams{
		TeamID:    teamID,
		ProjectID: projectID,
		MaxRows:   limit,
	})
	if err != nil {
		return nil, translate(err, "memory index")
	}

	lines := make([]IndexLine, len(rows))
	for i, row := range rows {
		lines[i] = IndexLine{Slug: row.Slug, Kind: string(row.Kind), Title: row.Title}
	}
	return lines, nil
}

// ChargeBytes debits the project's memory quota, and refuses the debit that would cross
// ProjectMemoryBytesQuota.
//
// The size is the byte length of the text, measured the way Postgres measures it: `len()` on a Go
// string counts UTF-8 bytes, exactly like `octet_length()`. Counting runes would let the counter
// drift below the real size on every accented character.
//
// ZERO ROWS MEANS THE QUOTA and nothing else: the project identifier comes from the authenticated
// token, so the row exists.
func (s *store) ChargeBytes(ctx context.Context, teamID, projectID uuid.UUID, bytes int64) error {
	_, err := s.q.ChargeProjectMemoryBytes(ctx, database.ChargeProjectMemoryBytesParams{
		TeamID:    teamID,
		ProjectID: projectID,
		Bytes:     bytes,
		Quota:     ProjectMemoryBytesQuota,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrQuotaExceeded
	}
	if err != nil {
		return translate(err, "charge memory bytes")
	}
	return nil
}

// splitSlugs turns the aggregated `supersedes` column back into a slice.
//
// The column is a comma-joined string and not an array because sqlc would map `text[]` onto
// github.com/lib/pq — a dependency this repository does not have and must not gain. The separator
// is safe because `memories_slug_shape` forbids a comma inside a slug; the query says so too, and
// the two comments must move together.
//
// An empty column yields an EMPTY SLICE and never nil: the field is serialised to JSON, and `[]`
// says "this entry replaced nothing" where `null` reads as "unknown".
func splitSlugs(joined string) []string {
	if joined == "" {
		return []string{}
	}
	return strings.Split(joined, ",")
}

// nullKind turns an optional kind into the nullable parameter sqlc expects. Empty means "every
// kind", which the query expresses as a NULL rather than as a second statement.
func nullKind(kind string) database.NullMemoryKind {
	if kind == "" {
		return database.NullMemoryKind{}
	}
	return database.NullMemoryKind{MemoryKind: database.MemoryKind(kind), Valid: true}
}
