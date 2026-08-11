package service_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// projectScope is the tenancy pair of a test project: what a project token carries.
type projectScope struct {
	teamID    uuid.UUID
	projectID uuid.UUID
}

// newRealService mounts the service on the REAL database. In-memory doubles prove what the service
// decides on its own; this file proves the whole chain — service, store, queries, constraints —
// because the release rule exists nowhere in Go: it lives in the SQL.
func newRealService(t *testing.T) (service.Service, *sql.DB) {
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

	return service.New(store.New(database.New(db), db, cache.NewMemory(time.Hour, time.Hour))), db
}

// newRealProject creates a throwaway team and project through direct SQL. The fixtures borrow no
// other feature: deleting the team takes the rest with it in cascade.
func newRealProject(t *testing.T, db *sql.DB, key string) projectScope {
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

	var projectID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Test project",
	).Scan(&projectID); err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}

	return projectScope{teamID: teamID, projectID: projectID}
}

// openTask opens a task through the service's nominal path.
func openTask(t *testing.T, svc service.Service, sc projectScope, title string) service.Task {
	t.Helper()

	task, err := svc.CreateTask(context.Background(), service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Title: title,
	})
	if err != nil {
		t.Fatalf("creating %q: %v", title, err)
	}
	return task
}

// unblockedEvents counts the `task.unblocked` entries journalled on a given task.
func unblockedEvents(t *testing.T, db *sql.DB, sc projectScope, number int64) int {
	t.Helper()

	var count int
	err := db.QueryRow(`
		SELECT count(*) FROM events e
		JOIN tasks t ON t.id = e.subject_id
		WHERE e.kind = 'task.unblocked'
		  AND e.subject_type = 'task'
		  AND t.project_id = $1
		  AND t.number = $2`, sc.projectID, number).Scan(&count)
	if err != nil {
		t.Fatalf("counting the events: %v", err)
	}
	return count
}

// THE criterion of the card, end to end: block_task, then moving the blocker to `done`, yields a
// blocked task back on `todo` AND a `task.unblocked` in the journal.
//
// Both halves matter. Without the return to `todo`, the agent reads a block nothing lifts; without
// the event, check_inbox has nothing to hand back and the task still says nothing — which was the
// original gap.
func TestBlockThenDoneReleasesAndAnnounces(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "E2E")

	blocked := openTask(t, svc, sc, "waiting for the migration")
	blocker := openTask(t, svc, sc, "the migration")

	after, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	})
	if err != nil {
		t.Fatalf("BlockTask: %v", err)
	}
	if after.Status != "blocked" {
		t.Fatalf("status after blocking = %q, want blocked", after.Status)
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
	}); err != nil {
		t.Fatalf("moving the blocker to done: %v", err)
	}

	released, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("reading the blocked task back: %v", err)
	}
	if released.Status != "todo" {
		t.Errorf("status after release = %q, want todo", released.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d task.unblocked event(s), want 1", n)
	}
}

// The other half of the "only if the edge had blocked it" criterion: a task the agent had moved to
// `blocked` ITSELF keeps its status, and is notified all the same.
//
// Notifying and deciding are two distinct gestures, and only one of them is automated. Conflating
// them would overwrite a human piece of information with a deduction.
func TestReleaseNotifiesWithoutOverridingAnAgentsBlock(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "KEEP")

	blocked := openTask(t, svc, sc, "blocked for another reason")
	blocker := openTask(t, svc, sc, "the migration")

	// The agent blocks the task by hand first: the edge opened afterwards will not claim the block,
	// so releasing it will not lift it.
	manual := "blocked"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Status: &manual,
	}); err != nil {
		t.Fatalf("manual block: %v", err)
	}
	if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Status: &done,
	}); err != nil {
		t.Fatalf("moving the blocker to done: %v", err)
	}

	after, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if after.Status != "blocked" {
		t.Errorf("status = %q, want blocked: the agent's decision is not overwritten", after.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d event(s), want 1: we notify even when we do not decide", n)
	}
}

// Archiving a blocker releases its edges. Archived, it will never reach `done`: without that rule,
// we would manufacture tasks nothing can ever unblock.
func TestArchivingABlockerReleasesItsEdges(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "ARCH")

	blocked := openTask(t, svc, sc, "waiting for a task about to vanish")
	blocker := openTask(t, svc, sc, "abandoned")

	if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocker.Number, Archive: true,
	}); err != nil {
		t.Fatalf("archiving the blocker: %v", err)
	}

	after, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if after.Status != "todo" {
		t.Errorf("status = %q, want todo: an archived blocker will never reach done", after.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d event(s), want 1", n)
	}
}

// Two blockers: the first one to fall releases nothing. Going back to `todo` demands that ALL edges
// be lifted — being freed from one obstacle out of two is still being blocked.
func TestPartialReleaseKeepsTheTaskBlocked(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "PART")

	blocked := openTask(t, svc, sc, "waiting for two things")
	first := openTask(t, svc, sc, "first")
	second := openTask(t, svc, sc, "second")

	for _, blocker := range []service.Task{first, second} {
		if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
			TeamID: sc.teamID, ProjectID: sc.projectID,
			Number: blocked.Number, Blocker: blocker.Number,
		}); err != nil {
			t.Fatalf("BlockTask on #%d: %v", blocker.Number, err)
		}
	}

	done := "done"
	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: first.Number, Status: &done,
	}); err != nil {
		t.Fatalf("first blocker to done: %v", err)
	}

	half, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("intermediate read back: %v", err)
	}
	if half.Status != "blocked" {
		t.Fatalf("status = %q, want blocked: one edge remains", half.Status)
	}

	if _, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: second.Number, Status: &done,
	}); err != nil {
		t.Fatalf("second blocker to done: %v", err)
	}

	full, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
	if err != nil {
		t.Fatalf("final read back: %v", err)
	}
	if full.Status != "todo" {
		t.Errorf("status = %q, want todo once both edges are lifted", full.Status)
	}
}

// A key from another project has no path in here: the service only resolves NUMBERS of its own
// project, and a number that does not exist there is not found. This is the service-side
// counterpart of what the composite constraint guarantees in the database.
func TestBlockTaskCannotNameAnotherProjectsTask(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "MINE")

	blocked := openTask(t, svc, sc, "mine")

	// A sibling project in the SAME team, with its own task number 1.
	var siblingID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		sc.teamID, "OTHR", "Sibling project",
	).Scan(&siblingID); err != nil {
		t.Fatalf("creating the sibling project: %v", err)
	}
	if _, err := svc.CreateTask(ctx, service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: siblingID, Title: "theirs",
	}); err != nil {
		t.Fatalf("sibling's task: %v", err)
	}

	// Number 999 exists in neither: the service cannot resolve it, and no input shape is able to
	// name the sibling project.
	_, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number, Blocker: 999,
	})
	if err == nil {
		t.Fatal("BlockTask accepted a non-existent blocker")
	}
}
