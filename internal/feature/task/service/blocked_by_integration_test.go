package service_test

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                              | Résumé                                        | Ligne |
// |--------------------------------------|-----------------------------------------------|-------|
// | edgeState                            | The state of one edge as Postgres holds it      | 42    |
// | edgesOf                              | Reads back the edges blocking a task            | 50    |
// | writeBody                            | Rewrites a description through the service      | 80    |
// | TestBodyLineOpensTheEdge             | A description carrying the line opens the edge  | 90    |
// | TestBodyLineRemovedReleasesItsOwnEdge| The line disappearing releases what it opened   | 141   |
// | TestBodyLineNeverReleasesAnAPIEdge   | A body edit cannot lift what block_task decided | 192   |
// | TestUnreadableLineRefusesTheWholePatch | An ambiguous line writes NOTHING              | 242   |
// | TestBodyLineOnCreate                 | A task born blocked by its own description      | 297   |
// | taskCount                            | Counts the project's tasks, archived included   | 336   |
// | itoa                                 | Composes the number of a readable reference     | 348   |
//
// Fin du sommaire.
// =====================================================================
//
// WHAT THIS FILE PROVES, and why none of it can be proven without Postgres.
//
// The `#blocked-by` line compiles into a row of task_dependencies, and the decision this card had
// to take (D47) lives in a column of that row: `origin`. A store double would prove the
// reimplementation of the origin predicate, not the product — the predicate is in the query.
//
// The third test is the one the card asked for by name: it says which of the three options is the
// right one. A description losing its line releases the edge THAT LINE opened, and nothing else.

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// edgeState is one edge as the database holds it: the two columns no reading of the description can
// recover, plus whether it is still active.
type edgeState struct {
	blocker  int64
	until    string
	origin   string
	released bool
}

// edgesOf reads back every edge blocking a task, active or not, oldest first.
func edgesOf(t *testing.T, db *sql.DB, sc projectScope, number int64) []edgeState {
	t.Helper()

	rows, err := db.Query(`
		SELECT b.number, d.until_status::text, d.origin, d.released_at IS NOT NULL
		FROM task_dependencies d
		JOIN tasks t ON t.id = d.task_id
		JOIN tasks b ON b.id = d.blocker_task_id
		WHERE d.project_id = $1 AND t.number = $2
		ORDER BY d.created_at`, sc.projectID, number)
	if err != nil {
		t.Fatalf("reading the edges back: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var edges []edgeState
	for rows.Next() {
		var e edgeState
		if err := rows.Scan(&e.blocker, &e.until, &e.origin, &e.released); err != nil {
			t.Fatalf("reading an edge: %v", err)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the edges back: %v", err)
	}
	return edges
}

// writeBody rewrites a description through the nominal path, the one an agent or the UI calls.
func writeBody(t *testing.T, svc service.Service, sc projectScope, number int64, body string) (service.Task, error) {
	t.Helper()

	return svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: number, Body: &body,
	})
}

// TestBodyLineOpensTheEdge is the first criterion of the card: a description carrying the line
// opens the edge, verified against Postgres and not against what the service returned.
func TestBodyLineOpensTheEdge(t *testing.T) {
	svc, db := newRealService(t)
	sc := newRealProject(t, db, "BODY")

	blocked := openTask(t, svc, sc, "waiting for the migration")
	blocker := openTask(t, svc, sc, "the migration")

	after, err := writeBody(t, svc, sc, blocked.Number,
		"Brief.\n\n#blocked-by @BODY-"+itoa(blocker.Number)+" until #in_progress\n")
	if err != nil {
		t.Fatalf("writing the description: %v", err)
	}
	if after.Status != "blocked" {
		t.Errorf("status after writing = %q, want blocked", after.Status)
	}

	edges := edgesOf(t, db, sc, blocked.Number)
	if len(edges) != 1 {
		t.Fatalf("%d edge(s) in the database, want 1: %+v", len(edges), edges)
	}
	if edges[0].blocker != blocker.Number {
		t.Errorf("edge blocked by %d, want %d", edges[0].blocker, blocker.Number)
	}
	if edges[0].until != "in_progress" {
		t.Errorf("release condition = %q, want in_progress — the line's condition was not read",
			edges[0].until)
	}
	if edges[0].origin != "body" {
		t.Errorf("origin = %q, want body", edges[0].origin)
	}
	if edges[0].released {
		t.Error("the edge is born released")
	}

	// Rewriting the description without touching the line changes nothing: what is compiled is the
	// DIFF, not the body. Reading the line again would try to reopen an edge that already exists.
	if _, err := writeBody(t, svc, sc, blocked.Number,
		"Brief, corrected.\n\n#blocked-by @BODY-"+itoa(blocker.Number)+" until #in_progress\n"); err != nil {
		t.Fatalf("rewriting around an unchanged line: %v", err)
	}
	if edges := edgesOf(t, db, sc, blocked.Number); len(edges) != 1 {
		t.Errorf("%d edge(s) after an unchanged line, want 1: %+v", len(edges), edges)
	}
}

// TestBodyLineRemovedReleasesItsOwnEdge is the criterion the card names: the case "the line
// disappears" has a test, and the test says which of the three options was chosen.
//
// The line disappearing releases the edge IT had opened — the task goes back to `todo` and the
// unblocking is journalled, through the same path as any other release. It is neither an edge
// surviving a description that no longer mentions it, nor a refusal of the write.
func TestBodyLineRemovedReleasesItsOwnEdge(t *testing.T) {
	svc, db := newRealService(t)
	sc := newRealProject(t, db, "DROP")

	blocked := openTask(t, svc, sc, "waiting for the migration")
	blocker := openTask(t, svc, sc, "the migration")

	line := "#blocked-by @DROP-" + itoa(blocker.Number) + " until #done"
	if _, err := writeBody(t, svc, sc, blocked.Number, "Brief.\n\n"+line+"\n"); err != nil {
		t.Fatalf("writing the description: %v", err)
	}

	after, err := writeBody(t, svc, sc, blocked.Number, "Brief, and nothing more.")
	if err != nil {
		t.Fatalf("removing the line: %v", err)
	}
	if after.Status != "todo" {
		t.Errorf("status after removal = %q, want todo", after.Status)
	}

	edges := edgesOf(t, db, sc, blocked.Number)
	if len(edges) != 1 {
		t.Fatalf("%d edge(s), want 1 released: %+v", len(edges), edges)
	}
	if !edges[0].released {
		t.Error("the edge survived the disappearance of the line that opened it")
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 1 {
		t.Errorf("%d task.unblocked event(s), want 1 — a release announces itself, whatever triggered it", n)
	}

	// Writing the line back opens a NEW edge: the released one is history, not a reservation.
	if _, err := writeBody(t, svc, sc, blocked.Number, "Brief.\n\n"+line+"\n"); err != nil {
		t.Fatalf("writing the line back: %v", err)
	}
	edges = edgesOf(t, db, sc, blocked.Number)
	if len(edges) != 2 {
		t.Fatalf("%d edge(s) after rewriting the line, want 2: %+v", len(edges), edges)
	}
	if edges[1].released {
		t.Error("the edge written back is already released")
	}
}

// TestBodyLineNeverReleasesAnAPIEdge is the decision itself (D47): each surface owns what it
// opened.
//
// A description rewritten — a copy-paste, a reformat, a body that never carried the line — cannot
// lift a block opened through block_task. Without the origin predicate this test goes red, and the
// defect it catches is silent: the agent reads a `todo` and takes back a task that is still
// blocked.
func TestBodyLineNeverReleasesAnAPIEdge(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "OWN")

	blocked := openTask(t, svc, sc, "waiting for the migration")
	blocker := openTask(t, svc, sc, "the migration")

	if _, err := svc.BlockTask(ctx, service.BlockTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Number: blocked.Number, Blocker: blocker.Number,
	}); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	// The description names the edge an agent opened, then loses it. Neither gesture may touch it.
	documented := "#blocked-by @OWN-" + itoa(blocker.Number) + " until #done"
	if _, err := writeBody(t, svc, sc, blocked.Number, "Brief.\n\n"+documented+"\n"); err != nil {
		t.Fatalf("documenting an existing edge: %v", err)
	}
	if edges := edgesOf(t, db, sc, blocked.Number); len(edges) != 1 || edges[0].origin != "api" {
		t.Fatalf("documenting the edge duplicated or reassigned it: %+v", edges)
	}

	after, err := writeBody(t, svc, sc, blocked.Number, "Brief, pasted over.")
	if err != nil {
		t.Fatalf("rewriting the description: %v", err)
	}

	edges := edgesOf(t, db, sc, blocked.Number)
	if len(edges) != 1 {
		t.Fatalf("%d edge(s), want 1: %+v", len(edges), edges)
	}
	if edges[0].released {
		t.Error("a description edit released an edge opened by block_task")
	}
	if after.Status != "blocked" {
		t.Errorf("status after the rewrite = %q, want blocked", after.Status)
	}
	if n := unblockedEvents(t, db, sc, blocked.Number); n != 0 {
		t.Errorf("%d task.unblocked event(s), want 0", n)
	}
}

// TestUnreadableLineRefusesTheWholePatch is the second criterion of the card: an ambiguous line
// refuses the ENTIRE write, description included.
//
// The refusal has to be total, and that is what is checked here rather than the error alone: a body
// written next to an edge that was refused would be the worst of the three options — the text and
// the graph disagreeing, with nobody told.
func TestUnreadableLineRefusesTheWholePatch(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "REF")

	blocked := openTask(t, svc, sc, "waiting for the migration")
	blocker := openTask(t, svc, sc, "the migration")

	if _, err := writeBody(t, svc, sc, blocked.Number, "Kept."); err != nil {
		t.Fatalf("writing the initial description: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{name: "two references on one line", body: "#blocked-by @REF-1 @REF-2"},
		{name: "an unknown task", body: "#blocked-by @REF-9999 until #done"},
		{name: "another project", body: "#blocked-by @FRNT-1 until #done"},
		{name: "an unknown condition", body: "#blocked-by @REF-" + itoa(blocker.Number) + " until #shipped"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := "rewritten title"
			_, err := svc.UpdateTask(ctx, service.UpdateTaskInput{
				TeamID: sc.teamID, ProjectID: sc.projectID, Number: blocked.Number,
				Title: &title, Body: &tc.body,
			})
			if err == nil {
				t.Fatal("write accepted, expected a refusal")
			}
			if !errors.Is(err, service.ErrInvalidInput) && !errors.Is(err, service.ErrNotFound) {
				t.Errorf("unexpected refusal: %v", err)
			}

			current, err := svc.GetTask(ctx, sc.teamID, sc.projectID, blocked.Number)
			if err != nil {
				t.Fatalf("reading the task back: %v", err)
			}
			if current.Body != "Kept." {
				t.Errorf("the description was written despite the refusal: %q", current.Body)
			}
			if current.Title == title {
				t.Error("the title was written despite the refusal — the patch is not rolled back whole")
			}
			if edges := edgesOf(t, db, sc, blocked.Number); len(edges) != 0 {
				t.Errorf("%d edge(s) opened by a refused write: %+v", len(edges), edges)
			}
		})
	}
}

// TestBodyLineOnCreate covers the other writing surface: a task born with the line in its
// description is born blocked, in one single write.
func TestBodyLineOnCreate(t *testing.T) {
	svc, db := newRealService(t)
	ctx := context.Background()
	sc := newRealProject(t, db, "BORN")

	blocker := openTask(t, svc, sc, "the migration")

	created, err := svc.CreateTask(ctx, service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Title: "waiting for the migration",
		Body:  "#blocked-by @BORN-" + itoa(blocker.Number) + " until #done",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Status != "blocked" {
		t.Errorf("status at creation = %q, want blocked", created.Status)
	}

	edges := edgesOf(t, db, sc, created.Number)
	if len(edges) != 1 || edges[0].origin != "body" || edges[0].released {
		t.Fatalf("edges at creation = %+v", edges)
	}

	// A creation refused by its description leaves nothing behind — not even the task.
	before := taskCount(t, db, sc)
	if _, err := svc.CreateTask(ctx, service.CreateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID,
		Title: "waiting for nothing",
		Body:  "#blocked-by @BORN-4242 until #done",
	}); err == nil {
		t.Fatal("creation accepted with an unresolvable reference")
	}
	if after := taskCount(t, db, sc); after != before {
		t.Errorf("%d task(s) after a refused creation, want %d", after, before)
	}
}

// taskCount counts the project's tasks, archived ones included.
func taskCount(t *testing.T, db *sql.DB, sc projectScope) int {
	t.Helper()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM tasks WHERE project_id = $1", sc.projectID).Scan(&count); err != nil {
		t.Fatalf("counting the tasks: %v", err)
	}
	return count
}

// itoa keeps the composition of a readable reference in one place: a description names its
// dependency the way a human reads it, `@KEY-12`.
func itoa(number int64) string {
	return strconv.FormatInt(number, 10)
}
