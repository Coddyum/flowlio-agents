package feature_test

// What this file locks down: a repo created inside a team arrives ALREADY CONNECTED to every repo
// the team already held, in both directions, as ROWS — two rows per peer since the edge became
// DIRECTED (card 11, migration 000013).
//
// WHY IT COUNTS ROWS AND NOT A STATUS. "All the repos of a team trust each other" could be honoured
// by an implicit rule in the query that decides — and every status this file could read would stay
// green. flowlio.me draws its canvas FROM project_trust: an implicit default renders as "no link"
// over a channel that is open, which is the product showing the opposite of its own database. The
// property owed is "no edge, no trust — always, without exception", and only a COUNT observes it.
//
// MUTATION: removing the `linked` CTE from CreateProject (sql/queries/projects.sql) takes the third
// repo's contribution from 4 edges to 0, and this file goes red on the count before it goes red on
// anything else. Played on 2026-08-07: see the report in the task's history.
//
// SECOND MUTATION, the one card 11 adds: dropping `(p.id, c.id)` from the LATERAL VALUES — that is,
// writing only the arrow OUT of the newcomer — halves the delta to 2, and the inbound create_issue
// at the bottom of this file starts answering `not found`. Half a default is not a default.
//
// WHY IT LIVES AT internal/feature/ RATHER THAN INSIDE workspace/. The count is a workspace fact,
// but the behaviour it buys belongs to the issue feature — a repo that cannot be written to is a
// repo nobody can question. A feature never imports a sibling, so the only package that may observe
// both is this one, which owns no feature.

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	issuestore "github.com/Coddyum/flowlio-agents/internal/feature/issue/store"
	workspacestore "github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openTrustDB opens the test database, or skips: the unit suite stays runnable with no
// infrastructure.
func openTrustDB(t *testing.T) *sql.DB {
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
	return db
}

// newTrustTeam creates a throwaway team and schedules its deletion; the cascade takes the projects
// and their edges with it.
func newTrustTeam(t *testing.T, ws workspacestore.Store, db *sql.DB) workspacestore.Team {
	t.Helper()

	slug := "trust-" + strings.ToLower(uuid.NewString()[:8])
	team, err := ws.CreateTeam(context.Background(), slug, "Trust default team")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", team.ID); err != nil {
			t.Errorf("cleaning up team %s: %v", team.ID, err)
		}
	})
	return team
}

// countTrustEdges counts the team's rows in project_trust.
//
// It reads the TABLE, not ListTrustEdges: the readable view joins projects twice, so a bug that
// dropped a join would show as a missing edge here and hide as a missing edge there. The number this
// returns is the number the canvas will draw.
func countTrustEdges(t *testing.T, db *sql.DB, teamID uuid.UUID) int {
	t.Helper()

	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM project_trust WHERE team_id = $1", teamID,
	).Scan(&n); err != nil {
		t.Fatalf("counting the edges: %v", err)
	}
	return n
}

// trustArrows renders the graph as "A→B" strings, DIRECTION INCLUDED.
//
// Its predecessor sorted the two ends of every edge, because under the pair of 000007 the order of
// first_key/second_key was the UUIDs' and meant nothing. Since 000013 it means everything: sorting
// the ends would render CORE→OPS and OPS→CORE identically, and this file would then assert three
// pairs while the table held any six arrows at all — including six pointing the same way.
//
// The rows keep the order ListTrustEdges gives them (`ORDER BY a.key, b.key`), so the assertion also
// pins what `flowlio trust list` prints, line for line.
func trustArrows(t *testing.T, ws workspacestore.Store, teamID uuid.UUID) []string {
	t.Helper()

	edges, err := ws.ListTrustEdges(context.Background(), teamID)
	if err != nil {
		t.Fatalf("ListTrustEdges: %v", err)
	}

	arrows := make([]string, 0, len(edges))
	for _, e := range edges {
		arrows = append(arrows, e.FromKey+"→"+e.ToKey)
	}
	return arrows
}

// openIssue asks a question from one repo to another, through the nominal path. It returns the
// store's error, which is the ONLY thing an unauthorised pair produces: a `not found`, deliberately
// indistinguishable from an unknown key.
func openIssue(t *testing.T, st issuestore.Store, teamID, authorID uuid.UUID, toKey, title string) error {
	t.Helper()

	ctx := context.Background()
	return st.WithTx(ctx, func(tx issuestore.Store) error {
		created, err := tx.CreateIssue(ctx, issuestore.NewIssue{
			TeamID:          teamID,
			AuthorProjectID: authorID,
			ToProjectKey:    toKey,
			Title:           title,
		})
		if err != nil {
			return err
		}
		return tx.AddFirstMessage(ctx, created.ID, authorID, "body of the question")
	})
}

// The third repo of a team arrives connected to each of the two already there, and `trust list`
// shows it.
//
// THE COUNT IS THE ASSERTION. The edge is DIRECTED (000013), one row per direction: the newcomer
// therefore contributes exactly 4 rows — two per repo already in the team — and the team ends with
// 6. Under the symmetric model this same guarantee read 2 and 3; the number doubled with card 11,
// what it proves did not.
func TestAThirdRepoArrivesLinkedToBothExistingOnes(t *testing.T) {
	db := openTrustDB(t)
	ws := workspacestore.New(database.New(db))
	ctx := context.Background()

	team := newTrustTeam(t, ws, db)

	if _, err := ws.CreateProject(ctx, team.ID, "CORE", "core"); err != nil {
		t.Fatalf("CreateProject CORE: %v", err)
	}
	// The FIRST repo of a team has nobody to be linked to, so its creation writes nothing.
	if got := countTrustEdges(t, db, team.ID); got != 0 {
		t.Errorf("one repo alone gives %d edges, want 0 — a repo cannot trust itself", got)
	}

	if _, err := ws.CreateProject(ctx, team.ID, "FRNT", "front"); err != nil {
		t.Fatalf("CreateProject FRNT: %v", err)
	}
	// Reported and not fatal on purpose: the assertion the card names is the DELTA the third repo
	// contributes, further down. Stopping here would hide it behind an earlier symptom of the very
	// same defect.
	if got := countTrustEdges(t, db, team.ID); got != 2 {
		t.Errorf("two repos give %d edges, want 2 (CORE→FRNT and FRNT→CORE)", got)
	}

	before := countTrustEdges(t, db, team.ID)

	ops, err := ws.CreateProject(ctx, team.ID, "OPS", "ops")
	if err != nil {
		t.Fatalf("CreateProject OPS: %v", err)
	}

	after := countTrustEdges(t, db, team.ID)
	if after-before != 4 {
		t.Errorf("the third repo added %d edges, want 4 (two per repo already in the team)", after-before)
	}
	if after != 6 {
		t.Errorf("the team holds %d edges, want 6 (three couples, two directions each)", after)
	}

	// `flowlio trust list` is ListTrustEdges: the customer must SEE the six arrows, since what they
	// do next is cut the ones they do not want — and cutting one no longer cuts its opposite.
	want := []string{"CORE→FRNT", "CORE→OPS", "FRNT→CORE", "FRNT→OPS", "OPS→CORE", "OPS→FRNT"}
	got := trustArrows(t, ws, team.ID)
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("trust list shows [%s], want [%s]", strings.Join(got, ", "), strings.Join(want, ", "))
	}

	// The behaviour the rows buy: the channel is open BOTH WAYS at the first gesture. This is the
	// failure observed on screen on 2026-08-07 — create_issue from a fresh repo answered
	// `not found`, and nothing said why. Under the directed model it takes TWO rows to keep that
	// true, which is why both calls below matter and neither implies the other.
	issues := issuestore.New(database.New(db), db, cache.NewMemory(time.Hour, time.Hour))

	core, err := ws.ProjectByKey(ctx, team.ID, "CORE")
	if err != nil {
		t.Fatalf("ProjectByKey CORE: %v", err)
	}

	if err := openIssue(t, issues, team.ID, ops.ID, "CORE", "OPS asks CORE"); err != nil {
		t.Errorf("create_issue from the newcomer to an existing repo: %v", err)
	}
	if err := openIssue(t, issues, team.ID, core.ID, "OPS", "CORE asks OPS"); err != nil {
		t.Errorf("create_issue from an existing repo to the newcomer: %v", err)
	}
}
