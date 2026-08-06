package store_test

// GUARANTEES 5 TO 16 OF THE TABLE IN docs/DESIGN-TUI.md § "Garanties de sécurité".
//
// `make check` PROVES NOTHING OF THIS FILE: without FLOWLIO_TEST_DATABASE_URL, everything is
// skipped. The recipe for this module is `make test-integration`.
//
// THE FIXTURE CARRIES TWO TEAMS WITH HOMONYMOUS KEYS. Both have a `CORE` and a `WEB`. A scope
// bearing on `key` alone — the most natural flaw to write — would pass every test of a
// single-team fixture, and would fail here. That is the sole reason for this shape, and it must
// not be simplified.
//
// The inserts are DIRECT SQL and not made through the `task` and `issue` features: a test going
// through them would prove the consistency of their own rules, and above all could not fabricate
// the illegal states half of these tests exist to reject — a message whose author belongs to
// another team, for instance.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	overviewstore "github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// team carries a test team and the projects created in it, indexed by key.
type team struct {
	id       uuid.UUID
	slug     string
	projects map[string]uuid.UUID
}

// fixture carries the two teams. B is the NEIGHBOUR: nothing it contains must ever show up in a
// read of A.
type fixture struct {
	db *sql.DB
	a  team
	b  team
}

func newStore(t *testing.T) (overviewstore.Store, *sql.DB) {
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

	return overviewstore.New(database.New(db)), db
}

// newTeam creates a throwaway team and its projects. Deleting the team takes everything else with
// it, by cascade.
func newTeam(t *testing.T, db *sql.DB, keys ...string) team {
	t.Helper()

	tm := team{slug: "test-" + strings.ToLower(uuid.NewString()[:8]), projects: map[string]uuid.UUID{}}
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", tm.slug, "Test team",
	).Scan(&tm.id); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", tm.id); err != nil {
			t.Errorf("cleaning up team %s: %v", tm.id, err)
		}
	})

	for _, key := range keys {
		var id uuid.UUID
		if err := db.QueryRow(
			"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
			tm.id, key, "Project "+key,
		).Scan(&id); err != nil {
			t.Fatalf("creating project %s: %v", key, err)
		}
		tm.projects[key] = id
	}
	return tm
}

// newFixture creates the two teams with HOMONYMOUS keys, plus an idle DOCS on A's side.
func newFixture(t *testing.T, db *sql.DB) fixture {
	t.Helper()
	return fixture{
		db: db,
		a:  newTeam(t, db, "CORE", "WEB", "DOCS"),
		b:  newTeam(t, db, "CORE", "WEB"),
	}
}

// openIssue inserts an issue. state and updatedAt are supplied: the ageing of a queue is exactly
// what this surface yields, so it must be controlled by the test.
func openIssue(t *testing.T, db *sql.DB, tm team, dest, author string, number int64, title, state string, updatedAt time.Time) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6::issue_state, $7) RETURNING id`,
		tm.id, tm.projects[dest], tm.projects[author], number, title, state, updatedAt,
	).Scan(&id); err != nil {
		t.Fatalf("creating issue %s-%d: %v", dest, number, err)
	}
	return id
}

// addMessage inserts a message into a thread. authorProjectID is passed BARE, with no team check:
// that is precisely the illegal state guarantee 14 must reject at read time, and the simple FK of
// issue_messages makes it writable.
func addMessage(t *testing.T, db *sql.DB, issueID, authorProjectID uuid.UUID, body string, createdAt time.Time) {
	t.Helper()

	if _, err := db.Exec(
		`INSERT INTO issue_messages (issue_id, author_project_id, body_md, created_at)
		 VALUES ($1, $2, $3, $4)`,
		issueID, authorProjectID, body, createdAt,
	); err != nil {
		t.Fatalf("creating the message: %v", err)
	}
}

// addTask inserts a task. updatedAt drives dormancy: the threshold lives in the service, so a
// test that wants a "dead" task fabricates it through its date, not through a setting.
func addTask(t *testing.T, db *sql.DB, tm team, key string, number int64, title, status string, updatedAt time.Time) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO tasks (team_id, project_id, number, title, status, updated_at)
		 VALUES ($1, $2, $3, $4, $5::task_status, $6) RETURNING id`,
		tm.id, tm.projects[key], number, title, status, updatedAt,
	).Scan(&id); err != nil {
		t.Fatalf("creating task %s-%d: %v", key, number, err)
	}
	return id
}

// addNote inserts a progress note on a task.
func addNote(t *testing.T, db *sql.DB, taskID uuid.UUID, body string, createdAt time.Time) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT INTO task_notes (task_id, body_md, created_at) VALUES ($1, $2, $3)",
		taskID, body, createdAt,
	); err != nil {
		t.Fatalf("creating the note: %v", err)
	}
}

// addToken inserts a project token and its last use. A nil lastUsed ⇒ a token that never served,
// that is to say the nominal case of the first day.
func addToken(t *testing.T, db *sql.DB, tm team, key string, lastUsed *time.Time) {
	t.Helper()

	// An admin token carries NEITHER team NOR project — `tokens_scope_shape` since migration
	// 000006. An empty key therefore fabricates the global admin, the only one bootstrap really
	// creates.
	teamID, projectID, scope := any(tm.id), any(tm.projects[key]), "project"
	if key == "" {
		teamID, projectID, scope = nil, nil, "admin"
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if _, err := db.Exec(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope, last_used_at)
		 VALUES ($1, $2, 'agent', $3, 'test-hash', $4::token_scope, $5)`,
		teamID, projectID, prefix, scope, lastUsed,
	); err != nil {
		t.Fatalf("creating the token: %v", err)
	}

	// An admin token carries no team, so deleting the fixture team does NOT take it with it: it
	// outlives the test and stays in the development database, where the next reader has to work out
	// which of the admin tokens is theirs. Project tokens go away by cascade; this one has to say so
	// itself.
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM tokens WHERE prefix = $1", prefix); err != nil {
			t.Errorf("cleaning up token %s: %v", prefix, err)
		}
	})
}

// refs yields the set of references of an issue queue, to compare EXACT SETS.
//
// "contains nothing of the neighbour" would also pass on an empty result, that is to say on a
// broken query. Comparing the exact set rejects both mistakes at once.
func refs(debts []overviewstore.IssueDebt) map[string]bool {
	out := make(map[string]bool, len(debts))
	for _, d := range debts {
		out[fmt.Sprintf("%s-%d", d.ProjectKey, d.Number)] = true
	}
	return out
}

// taskRefs yields the set of references of a task queue.
func taskRefs(debts []overviewstore.TaskDebt) map[string]bool {
	out := make(map[string]bool, len(debts))
	for _, d := range debts {
		out[fmt.Sprintf("%s-%d", d.ProjectKey, d.Number)] = true
	}
	return out
}

// assertSet compares an obtained set to the expected one, and names both gaps.
func assertSet(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()

	wanted := make(map[string]bool, len(want))
	for _, w := range want {
		wanted[w] = true
		if !got[w] {
			t.Errorf("%s missing from the queue — the read no longer sees everything it must", w)
		}
	}
	for g := range got {
		if !wanted[g] {
			t.Errorf("%s present in the queue — a row of another team leaked", g)
		}
	}
}

var ctx = context.Background()
