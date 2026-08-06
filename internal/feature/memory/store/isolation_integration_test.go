package store_test

// THE CENTRAL CRITERION OF M5 (FLWL-7): "a project token CANNOT read a sibling's memory —
// REFUSED IN THE DATABASE, proven by mutation."
//
// WHY THESE TESTS RUN AGAINST POSTGRES AND CANNOT DO OTHERWISE. The guarantee is a WHERE clause.
// A double reproducing it would prove the double, and that is the failure mode this repository has
// already paid for twice: a guard that stops matching stays green.
//
// The two projects live in the SAME TEAM, which is the hard case. Two teams are kept apart by the
// team predicate as well, so a test across teams would pass even on a query that had lost its
// project predicate — and the project predicate is the one this feature rests on.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// scope is the tenancy pair of a test project: exactly what a project token carries.
type scope struct {
	teamID    uuid.UUID
	projectID uuid.UUID
}

// newStore opens the test database. Without FLOWLIO_TEST_DATABASE_URL the test is skipped: the
// unit suite has to stay runnable with no infrastructure.
func newStore(t *testing.T) (store.Store, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL not set — integration test skipped")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("database unreachable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return store.New(database.New(db), db), db
}

// newTeam creates a throwaway team. Deleting it takes its projects and their memories with it, by
// cascade, so no run leaves anything behind that could make the next one pass.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Test team",
	).Scan(&teamID); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", teamID, err)
		}
	})
	return teamID
}

// newProjectIn creates a project inside an existing team.
func newProjectIn(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) scope {
	t.Helper()

	var projectID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Test project",
	).Scan(&projectID); err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}
	return scope{teamID: teamID, projectID: projectID}
}

// siblings lays down two projects of the SAME team, each with one entry.
func siblings(t *testing.T) (store.Store, scope, scope) {
	t.Helper()

	st, db := newStore(t)
	teamID := newTeam(t, db)
	mine := newProjectIn(t, db, teamID, "MINE")
	other := newProjectIn(t, db, teamID, "OTHR")

	write(t, st, mine, "my-secret", "The reason we chose Postgres FTS")
	write(t, st, other, "their-secret", "The reason they chose something else")

	return st, mine, other
}

// write puts one entry in a project.
func write(t *testing.T, st store.Store, sc scope, slug, title string) store.Entry {
	t.Helper()

	e, err := st.Create(context.Background(), sc.teamID, sc.projectID,
		slug, store.KindDecision, title, "the body of "+slug)
	if err != nil {
		t.Fatalf("writing %s in %s: %v", slug, sc.projectID, err)
	}
	return e
}

// A sibling's entry is UNFINDABLE by slug, not merely forbidden. The distinction is the whole
// point: a 403 would confirm the slug exists, and a registry can be probed one slug at a time.
//
// MUTATION: remove `project_id = @project_id` from MemoryBySlug — this test goes red.
func TestASiblingsEntryIsUnfindableBySlug(t *testing.T) {
	st, mine, _ := siblings(t)

	_, err := st.BySlug(context.Background(), mine.teamID, mine.projectID, "their-secret")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound — a project read its sibling's memory", err)
	}
}

// A listing returns only the caller's own entries, even inside a shared team.
//
// MUTATION: remove `project_id = @project_id` from ListMemories — this test goes red on the count
// and names the leaked slug.
func TestAListingStopsAtTheProjectBoundary(t *testing.T) {
	st, mine, _ := siblings(t)

	entries, total, err := st.List(context.Background(), store.Filter{
		TeamID: mine.teamID, ProjectID: mine.projectID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if total != 1 || len(entries) != 1 {
		t.Fatalf("total = %d, entries = %d, want 1 and 1", total, len(entries))
	}
	if entries[0].Slug != "my-secret" {
		t.Errorf("read %q, want \"my-secret\" — the listing crossed into the sibling", entries[0].Slug)
	}
}

// SEARCH IS THE SURFACE WHERE A LEAK WOULD BE LOUDEST, and it needs its own test: it is the one
// read whose WHERE clause carries a second, unrelated predicate — the `@@` match — and a scope
// predicate dropped next to it is the easiest one to lose in a rewrite.
//
// The query matches BOTH entries by design ("reason" is in both titles): a search that could not
// match the sibling would prove nothing.
//
// MUTATION: remove `project_id = @project_id` from SearchMemories — this test goes red.
func TestSearchStopsAtTheProjectBoundary(t *testing.T) {
	st, mine, _ := siblings(t)

	entries, total, err := st.List(context.Background(), store.Filter{
		TeamID: mine.teamID, ProjectID: mine.projectID, Query: "reason", Limit: 50,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if total != 1 || len(entries) != 1 {
		t.Fatalf("total = %d, entries = %d, want 1 and 1 — the search crossed the boundary", total, len(entries))
	}
	if entries[0].Slug != "my-secret" {
		t.Errorf("search returned %q", entries[0].Slug)
	}
}

// The handshake index stops at the boundary too. It has its own test because it is the one read an
// agent does not ask for: it is injected, once per session, before its first message. A leak here
// would put a sibling's titles into an agent's context with nothing in the transcript to show for it.
//
// MUTATION: remove `project_id = @project_id` from MemoryIndex — this test goes red.
func TestTheHandshakeIndexStopsAtTheProjectBoundary(t *testing.T) {
	st, mine, _ := siblings(t)

	lines, err := st.Index(context.Background(), mine.teamID, mine.projectID, 50)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if len(lines) != 1 || lines[0].Slug != "my-secret" {
		t.Fatalf("index = %+v, want the single entry \"my-secret\"", lines)
	}
}

// A sibling's entry cannot be SUPERSEDED either. The write side needs its own case: the read tests
// above would all pass on a Supersede that resolved its target without a project predicate, and
// retiring a neighbour's decision is a worse outcome than reading it.
//
// MUTATION: remove `project_id = @project_id` from MemoryBySlug or from SupersedeMemory — this
// test goes red.
func TestASiblingsEntryCannotBeSuperseded(t *testing.T) {
	st, mine, other := siblings(t)
	ctx := context.Background()

	err := st.Supersede(ctx, mine.teamID, mine.projectID, "their-secret", "my-secret")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound — a project retired its sibling's decision", err)
	}

	// And the neighbour's entry is untouched: an error alone would not prove the write did not
	// land, since Supersede reports the resolution failure and the write failure the same way.
	their, err := st.BySlug(ctx, other.teamID, other.projectID, "their-secret")
	if err != nil {
		t.Fatalf("reading the sibling's entry back: %v", err)
	}
	if !their.InForce() {
		t.Errorf("the sibling's entry is superseded by %q", their.SupersededBy)
	}
}

// COUNTER-PROOF of all of the above: a project reaches its OWN entry. Without it, a store that
// refused everything would pass every test in this file.
func TestAProjectReachesItsOwnEntry(t *testing.T) {
	st, mine, _ := siblings(t)

	found, err := st.BySlug(context.Background(), mine.teamID, mine.projectID, "my-secret")
	if err != nil {
		t.Fatalf("a project cannot read its own memory: %v", err)
	}
	if found.Title != "The reason we chose Postgres FTS" {
		t.Errorf("title = %q", found.Title)
	}
	if !found.InForce() {
		t.Errorf("a fresh entry is already superseded by %q", found.SupersededBy)
	}
}
