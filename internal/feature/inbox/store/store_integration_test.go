package store_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/database"
	inboxstore "github.com/Coddyum/flowlio-agents/internal/feature/inbox/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// fixture carries a team, two projects that talk to each other and the observed project's token.
type fixture struct {
	db      *sql.DB
	teamID  uuid.UUID
	web     uuid.UUID
	core    uuid.UUID
	tokenID uuid.UUID
}

func newStore(t *testing.T) (inboxstore.Store, *sql.DB) {
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

	return inboxstore.New(database.New(db)), db
}

// newFixture creates a team, the WEB and CORE projects, and a project token for CORE: that is the
// point of view the inbox is observed from.
func newFixture(t *testing.T, db *sql.DB) fixture {
	t.Helper()

	f := fixture{db: db}
	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Test team",
	).Scan(&f.teamID); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", f.teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", f.teamID, err)
		}
	})

	for key, dest := range map[string]*uuid.UUID{"WEB": &f.web, "CORE": &f.core} {
		if err := db.QueryRow(
			"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
			f.teamID, key, "Project "+key,
		).Scan(dest); err != nil {
			t.Fatalf("creating project %s: %v", key, err)
		}
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if err := db.QueryRow(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope)
		 VALUES ($1, $2, 'agent', $3, 'test-hash', 'project') RETURNING id`,
		f.teamID, f.core, prefix,
	).Scan(&f.tokenID); err != nil {
		t.Fatalf("creating the token: %v", err)
	}

	return f
}

// openIssue inserts an issue and its event through direct SQL: the inbox feature must depend on
// no other feature, not even in its tests.
func openIssue(t *testing.T, f fixture, from, to uuid.UUID, title, state, body string) uuid.UUID {
	t.Helper()

	var number int64
	if err := f.db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1", to,
	).Scan(&number); err != nil {
		t.Fatalf("reserving the number: %v", err)
	}

	var issueID uuid.UUID
	closedAt := "NULL"
	if state == "closed" {
		closedAt = "now()"
	}
	if err := f.db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, `+closedAt+`) RETURNING id`,
		f.teamID, to, from, number, title, state,
	).Scan(&issueID); err != nil {
		t.Fatalf("creating issue %q: %v", title, err)
	}

	if _, err := f.db.Exec(
		"INSERT INTO issue_messages (issue_id, author_project_id, body_md) VALUES ($1, $2, $3)",
		issueID, from, body,
	); err != nil {
		t.Fatalf("first message: %v", err)
	}

	if _, err := f.db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, notify_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $3, $2, 'issue.opened', 'issue', $4)`,
		f.teamID, to, from, issueID,
	); err != nil {
		t.Fatalf("event: %v", err)
	}

	return issueID
}

func scopeOf(f fixture) inboxstore.Scope {
	return inboxstore.Scope{
		TokenID:   f.tokenID,
		TeamID:    f.teamID,
		ProjectID: f.core,
		Limit:     11,
	}
}

// The inbox returns the STATE, not a stream. Every bucket is derived from issues.state /
// tasks.status.
func TestInboxBucketsReflectState(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)
	sc := scopeOf(f)

	key, err := st.ProjectKey(ctx, f.teamID, f.core)
	if err != nil {
		t.Fatalf("ProjectKey: %v", err)
	}
	if key != "CORE" {
		t.Errorf("key = %q, expected CORE", key)
	}

	openIssue(t, f, f.web, f.core, "WEB is waiting on CORE", "open", "could you have a look?")
	openIssue(t, f, f.core, f.web, "CORE got its answer", "answered", "here is the answer")
	openIssue(t, f, f.web, f.core, "already settled", "closed", "settled")

	cursor, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if cursor.LastEventID != 0 {
		t.Errorf("cursor of a brand-new token = %d, expected 0", cursor.LastEventID)
	}
	if cursor.HeadEventID == 0 {
		t.Fatal("head of the journal at 0 although events were written")
	}

	incoming, err := st.IncomingOpen(ctx, sc, cursor.LastEventID)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 1 || incoming[0].Title != "WEB is waiting on CORE" {
		t.Fatalf("%d open incoming issues, expected the single question from WEB", len(incoming))
	}
	if incoming[0].PeerKey != "WEB" {
		t.Errorf("peer = %q, expected WEB", incoming[0].PeerKey)
	}
	if incoming[0].Excerpt != "could you have a look?" {
		t.Errorf("excerpt = %q, expected the last message", incoming[0].Excerpt)
	}
	if !incoming[0].New {
		t.Error("the issue must be flagged new: the cursor of a brand-new token is at 0")
	}

	answered, err := st.OutgoingAnswered(ctx, sc, cursor.LastEventID)
	if err != nil {
		t.Fatalf("OutgoingAnswered: %v", err)
	}
	if len(answered) != 1 || answered[0].PeerKey != "WEB" {
		t.Fatalf("%d answered outgoing issues, expected 1 at WEB", len(answered))
	}
}

// The cursor ONLY drives the "new" flag. After moving it forward, the same lines come back: the
// state has not changed, so it is still to be dealt with.
func TestCursorOnlyDrivesTheNewFlag(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)
	sc := scopeOf(f)

	openIssue(t, f, f.web, f.core, "question pending", "open", "?")

	cursor, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if err := st.Advance(ctx, f.tokenID, cursor.HeadEventID); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	after, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if after.LastEventID != cursor.HeadEventID {
		t.Errorf("cursor = %d after moving forward, expected %d", after.LastEventID, cursor.HeadEventID)
	}

	incoming, err := st.IncomingOpen(ctx, sc, after.LastEventID)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 1 {
		t.Fatalf("%d issues after moving the cursor forward, expected 1: "+
			"an unanswered question is still to be dealt with", len(incoming))
	}
	if incoming[0].New {
		t.Error("the issue must no longer be flagged new after the cursor moved forward")
	}

	// The cursor never goes backwards, even if a concurrent call presents an older position.
	if err := st.Advance(ctx, f.tokenID, 0); err != nil {
		t.Fatalf("Advance (older position): %v", err)
	}
	back, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if back.LastEventID != cursor.HeadEventID {
		t.Errorf("cursor = %d, expected %d: it must never go backwards",
			back.LastEventID, cursor.HeadEventID)
	}
}

// A project's inbox never shows the activity of a third-party project, even within the same team.
func TestInboxIsScopedToItsProject(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	var spy uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'SPY', 'Project SPY') RETURNING id",
		f.teamID,
	).Scan(&spy); err != nil {
		t.Fatalf("creating project SPY: %v", err)
	}

	openIssue(t, f, f.web, f.core, "between WEB and CORE", "open", "private")

	spyScope := inboxstore.Scope{TokenID: f.tokenID, TeamID: f.teamID, ProjectID: spy, Limit: 11}

	incoming, err := st.IncomingOpen(ctx, spyScope, 0)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 0 {
		t.Errorf("SPY sees %d incoming issues, expected 0", len(incoming))
	}

	answered, err := st.OutgoingAnswered(ctx, spyScope, 0)
	if err != nil {
		t.Fatalf("OutgoingAnswered: %v", err)
	}
	if len(answered) != 0 {
		t.Errorf("SPY sees %d answered issues, expected 0", len(answered))
	}

	tasks, err := st.InProgressTasks(ctx, spyScope)
	if err != nil {
		t.Fatalf("InProgressTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("SPY sees %d tasks, expected 0", len(tasks))
	}
}

// A task in progress signals an interrupted session: that is what an agent must pick up first.
// Archived or finished tasks do not show up there.
func TestInProgressTasksOnly(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	insert := func(title, status string, archived bool) {
		t.Helper()
		var number int64
		if err := db.QueryRow(
			"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1",
			f.core,
		).Scan(&number); err != nil {
			t.Fatalf("reserving the number: %v", err)
		}
		archivedAt := "NULL"
		if archived {
			archivedAt = "now()"
		}
		if _, err := db.Exec(
			`INSERT INTO tasks (team_id, project_id, number, title, status, archived_at)
			 VALUES ($1, $2, $3, $4, $5, `+archivedAt+`)`,
			f.teamID, f.core, number, title, status,
		); err != nil {
			t.Fatalf("creating task %q: %v", title, err)
		}
	}

	insert("in progress", "in_progress", false)
	insert("to do", "todo", false)
	insert("finished", "done", false)
	insert("in progress but archived", "in_progress", true)

	tasks, err := st.InProgressTasks(ctx, scopeOf(f))
	if err != nil {
		t.Fatalf("InProgressTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "in progress" {
		t.Fatalf("%d tasks, expected the single non-archived task in progress", len(tasks))
	}
}
