package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// scope is the tenancy pair of a test project: exactly what a project token carries.
type scope struct {
	teamID    uuid.UUID
	projectID uuid.UUID
}

// newStore opens the test database. Without FLOWLIO_TEST_DATABASE_URL the test is skipped: the unit
// suite has to stay runnable with no infrastructure.
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

	return store.New(database.New(db), db, cache.NewMemory(time.Hour, time.Hour)), db
}

// newProject creates a throwaway team and project through direct SQL.
//
// The fixtures do not borrow the workspace feature's store: the task feature must depend on no
// other feature, not even in its tests. Deleting the team takes the rest with it in cascade, so the
// test database does not drift from one run to the next.
func newProject(t *testing.T, db *sql.DB, key string) scope {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id",
		slug, "Test team",
	).Scan(&teamID)
	if err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", teamID, err)
		}
	})

	var projectID uuid.UUID
	err = db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Test project",
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}

	return scope{teamID: teamID, projectID: projectID}
}

// newProjectIn creates a second project inside an existing team: the next-door neighbour, the one
// that must see nothing of the first one's backlog.
func newProjectIn(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) scope {
	t.Helper()

	var projectID uuid.UUID
	err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Sibling project",
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}
	return scope{teamID: teamID, projectID: projectID}
}

// createTask opens a task in a given scope, through the store's nominal path.
func createTask(t *testing.T, st store.Store, sc scope, title string) store.Task {
	t.Helper()

	ctx := context.Background()
	var created store.Task
	err := st.WithTx(ctx, func(tx store.Store) error {
		number, err := tx.ClaimNumber(ctx, sc.teamID, sc.projectID)
		if err != nil {
			return err
		}
		created, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    number,
			Title:     title,
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if err != nil {
		t.Fatalf("creating task %q: %v", title, err)
	}
	return created
}

func TestTaskLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "first task")
	if task.Number != 1 {
		t.Errorf("first task numbered %d, want 1", task.Number)
	}

	second := createTask(t, st, sc, "second task")
	if second.Number != 2 {
		t.Errorf("second task numbered %d, want 2", second.Number)
	}

	status := "in_progress"
	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	updated, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID:    sc.teamID,
		ProjectID: sc.projectID,
		Number:    task.Number,
		Status:    &status,
		Deadline:  &deadline,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if updated.Title != task.Title {
		t.Errorf("a field absent from the patch was overwritten: title = %q, want %q", updated.Title, task.Title)
	}
	if updated.Deadline == nil || !updated.Deadline.UTC().Equal(deadline) {
		t.Errorf("deadline = %v, want %v", updated.Deadline, deadline)
	}

	// ClearDeadline must wipe, where a nil pointer means "do not change".
	cleared, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID:        sc.teamID,
		ProjectID:     sc.projectID,
		Number:        task.Number,
		ClearDeadline: true,
	})
	if err != nil {
		t.Fatalf("UpdateTask (clear deadline): %v", err)
	}
	if cleared.Deadline != nil {
		t.Errorf("deadline = %v after wiping, want nil", cleared.Deadline)
	}

	if _, err := st.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "progress"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	notes, total, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "progress" {
		t.Fatalf("ListNotes returns %d notes, want the note just added", len(notes))
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}

	archived, err := st.UpdateTask(ctx, store.TaskPatch{TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number, Archive: true})
	if err != nil {
		t.Fatalf("archiving: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Error("an archived task must carry an archive timestamp")
	}

	if _, err := st.UpdateTask(ctx, store.TaskPatch{TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number, Archive: true}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second archiving: error = %v, want ErrNotFound", err)
	}
}

// The note thread is BOUNDED by the query, and the total stays exact.
//
// Without LIMIT, `get CORE-34` serialised the whole thread: measured on this database, 1,000 notes
// of 64 KiB gave 62.6 MiB in 669 ms, written unthrottled in 659 ms. This is the tool an agent calls
// to RESUME a task — an unbounded thread means one call that fills its context on a read it thought
// was harmless.
//
// This test uses 1 KiB notes: what it checks is not a volume, it is that the size returned NO
// LONGER GROWS with the thread. The 62.6 MiB figure is a measurement, not a run to replay on every
// `make test-integration`.
//
// MUTATION: removing `LIMIT @lim` from ListTaskNotes makes this test fail on all three assertions.
func TestNoteThreadIsBoundedByTheQuery(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")
	task := createTask(t, st, sc, "long thread")

	// created_at is explicit and distinct per note: a bulk INSERT would give them all the same
	// now(), and the tie-break would fall back on a random uuid. Real traffic writes one note per
	// request, hence one per transaction, hence one created_at per note — that is what we simulate.
	const written = 1000
	if _, err := db.Exec(`
		INSERT INTO task_notes (task_id, body_md, created_at)
		SELECT $1, repeat('x', 1024) || ' #' || g, now() - make_interval(secs => $2 - g)
		FROM generate_series(1, $2) AS g`,
		task.ID, written,
	); err != nil {
		t.Fatalf("seeding the thread: %v", err)
	}

	const window = 10
	notes, total, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, window)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}

	if len(notes) != window {
		t.Errorf("%d notes returned, want %d: the thread is not bounded", len(notes), window)
	}
	if total != written {
		t.Errorf("total = %d, want %d: the agent does not know it is only reading a window", total, written)
	}

	raw, err := json.Marshal(notes)
	if err != nil {
		t.Fatalf("serialisation: %v", err)
	}
	if len(raw) > 64<<10 {
		t.Errorf("%d bytes serialised for %d notes written: the size returned still follows the thread",
			len(raw), written)
	}

	// It is the LAST notes that carry the state, returned in write order.
	if !strings.HasSuffix(notes[len(notes)-1].Body, "#1000") {
		t.Errorf("last note returned = %q, want note #1000",
			notes[len(notes)-1].Body[max(0, len(notes[len(notes)-1].Body)-8):])
	}
	if !strings.HasSuffix(notes[0].Body, "#991") {
		t.Errorf("first note returned = %q, want note #991 (window of the last 10)",
			notes[0].Body[max(0, len(notes[0].Body)-8):])
	}
}

// The product's central security property: a project token only ever sees its own backlog. Both
// projects live in the SAME team, which is the most exposed case — a filter carrying only team_id
// would pass every other test and fail here.
func TestTasksAreIsolatedAcrossProjectsOfSameTeam(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	front := newProjectIn(t, db, core.teamID, "FRNT")

	task := createTask(t, st, core, "CORE's secret")

	t.Run("read", func(t *testing.T) {
		if _, err := st.TaskByNumber(ctx, front.teamID, front.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT reads CORE's task: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("listing", func(t *testing.T) {
		tasks, err := st.ListTasks(ctx, store.TaskFilter{
			TeamID:    front.teamID,
			ProjectID: front.projectID,
			Limit:     50,
		})
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("FRNT lists %d tasks, want 0", len(tasks))
		}
	})

	t.Run("modification", func(t *testing.T) {
		title := "hijacked"
		if _, err := st.UpdateTask(ctx, store.TaskPatch{
			TeamID:    front.teamID,
			ProjectID: front.projectID,
			Number:    task.Number,
			Title:     &title,
		}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT modifies CORE's task: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("note", func(t *testing.T) {
		if _, err := st.AddNote(ctx, front.teamID, front.projectID, task.Number, "intrusion"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT writes into CORE's thread: error = %v, want ErrNotFound", err)
		}
	})

	t.Run("reading the notes", func(t *testing.T) {
		notes, _, err := st.ListNotes(ctx, front.teamID, front.projectID, task.Number, 10)
		if err != nil {
			t.Fatalf("ListNotes: %v", err)
		}
		if len(notes) != 0 {
			t.Errorf("FRNT reads %d of CORE's notes, want 0", len(notes))
		}
	})

	t.Run("archiving", func(t *testing.T) {
		if _, err := st.UpdateTask(ctx, store.TaskPatch{TeamID: front.teamID, ProjectID: front.projectID, Number: task.Number, Archive: true}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT archives CORE's task: error = %v, want ErrNotFound", err)
		}
	})

	// After all those attempts, the task must be untouched at its owner's.
	unchanged, err := st.TaskByNumber(ctx, core.teamID, core.projectID, task.Number)
	if err != nil {
		t.Fatalf("TaskByNumber (owner): %v", err)
	}
	if unchanged.Title != "CORE's secret" || unchanged.ArchivedAt != nil {
		t.Errorf("CORE's task was altered: %+v", unchanged)
	}
}

// A scope's team_id must never be enough on its own, and neither must the project_id: a request
// presenting the right project_id with another team's team_id has to fail.
func TestTaskScopeRequiresBothTeamAndProject(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	other := newProject(t, db, "CORE")

	task := createTask(t, st, core, "task of team A")

	forged := scope{teamID: other.teamID, projectID: core.projectID}
	if _, err := st.TaskByNumber(ctx, forged.teamID, forged.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("valid project_id + foreign team_id: error = %v, want ErrNotFound", err)
	}

	tasks, err := st.ListTasks(ctx, store.TaskFilter{
		TeamID:    forged.teamID,
		ProjectID: forged.projectID,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("the forged scope lists %d tasks, want 0", len(tasks))
	}
}

// Reserving a number on another team's project has to be impossible: otherwise a third-party
// project's counter could be pushed forward without any access to it, and that project's numbers
// would become guessable.
func TestClaimNumberIsScopedToTeam(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	other := newProject(t, db, "CORE")

	if _, err := st.ClaimNumber(ctx, other.teamID, core.projectID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross reservation: error = %v, want ErrNotFound", err)
	}

	// The target project's counter has not moved: the first legitimate task does carry number 1.
	task := createTask(t, st, core, "first")
	if task.Number != 1 {
		t.Errorf("number = %d after a cross attempt, want 1", task.Number)
	}
}

// Reserving the number and inserting share a transaction: a rejected insert must not burn a number
// for good in the CORE-1, CORE-2, … sequence.
func TestFailedCreateDoesNotBurnNumber(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	err := st.WithTx(ctx, func(tx store.Store) error {
		number, err := tx.ClaimNumber(ctx, sc.teamID, sc.projectID)
		if err != nil {
			return err
		}
		if number != 1 {
			t.Errorf("number reserved = %d, want 1", number)
		}
		// Empty title: refused by the tasks_title_not_blank constraint, so the whole transaction is
		// rolled back, the number reservation included.
		_, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    number,
			Title:     "   ",
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("inserting an empty title: error = %v, want ErrConflict", err)
	}

	task := createTask(t, st, sc, "first real task")
	if task.Number != 1 {
		t.Errorf("number = %d after a failure, want 1 (the number must not be burnt)", task.Number)
	}
}

// The patch and the note of one call fall together or hold together.
//
// This is the guarantee the folding of add_task_note into update_task rests on: without it, the
// state "status changed, reason lost" would stay reachable — the note fails while the done is
// already in the database, and the next session reads a done that nothing explains.
//
// MUTATION: replacing the deferred Rollback of tx.go with an unconditional Commit makes this test
// fail on both assertions at once.
func TestPatchAndNoteRollBackTogether(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "original title")

	boom := errors.New("failure after both writes")
	patched := "modified title"
	err := st.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.UpdateTask(ctx, store.TaskPatch{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    task.Number,
			Title:     &patched,
		}); err != nil {
			return err
		}
		if _, err := tx.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "note that must not survive"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the one returned by fn", err)
	}

	reread, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, task.Number)
	if err != nil {
		t.Fatalf("reading the task back: %v", err)
	}
	if reread.Title != "original title" {
		t.Errorf("title = %q after rollback, want %q: the patch was committed on its own",
			reread.Title, "original title")
	}

	notes, _, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("%d note(s) after rollback, want 0: %+v", len(notes), notes)
	}
}

func TestArchivedTaskIsNotWritable(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "to be archived")
	if _, err := st.UpdateTask(ctx, store.TaskPatch{TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number, Archive: true}); err != nil {
		t.Fatalf("archiving: %v", err)
	}

	title := "change after archiving"
	if _, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID:    sc.teamID,
		ProjectID: sc.projectID,
		Number:    task.Number,
		Title:     &title,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("modifying an archived task: error = %v, want ErrNotFound", err)
	}

	if _, err := st.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "late note"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("note on an archived task: error = %v, want ErrNotFound", err)
	}

	// It stays readable: archiving tidies away, it does not erase.
	if _, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, task.Number); err != nil {
		t.Errorf("reading an archived task: %v", err)
	}
}

func TestListTasksFilters(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	todo := createTask(t, st, sc, "to do")
	done := createTask(t, st, sc, "finished")
	archived := createTask(t, st, sc, "archived")

	doneStatus := "done"
	if _, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: done.Number, Status: &doneStatus,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, err := st.UpdateTask(ctx, store.TaskPatch{TeamID: sc.teamID, ProjectID: sc.projectID, Number: archived.Number, Archive: true}); err != nil {
		t.Fatalf("archiving: %v", err)
	}

	base := store.TaskFilter{TeamID: sc.teamID, ProjectID: sc.projectID, Limit: 50}

	t.Run("archived ones are excluded by default", func(t *testing.T) {
		tasks, err := st.ListTasks(ctx, base)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 2 {
			t.Fatalf("%d active tasks, want 2", len(tasks))
		}
		// Sorted by descending number: the most recent one first.
		if tasks[0].Number != done.Number {
			t.Errorf("first task no. %d, want %d (descending sort)", tasks[0].Number, done.Number)
		}
	})

	t.Run("archived includes the archived ones", func(t *testing.T) {
		filter := base
		filter.IncludeArchived = true
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 3 {
			t.Errorf("%d tasks in total, want 3", len(tasks))
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		filter := base
		filter.Status = "todo"
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 1 || tasks[0].Number != todo.Number {
			t.Errorf("the todo filter returns %d tasks, want the single to-do task", len(tasks))
		}
	})

	t.Run("the limit is honoured", func(t *testing.T) {
		filter := base
		filter.Limit = 1
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 1 {
			t.Errorf("%d tasks with limit=1, want 1", len(tasks))
		}
	})
}

// The database carries the vocabulary guarantee: a status outside the enum is refused even if the
// application-level validation were to be bypassed.
func TestDatabaseRejectsUnknownStatus(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	err := st.WithTx(ctx, func(tx store.Store) error {
		number, err := tx.ClaimNumber(ctx, sc.teamID, sc.projectID)
		if err != nil {
			return err
		}
		_, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    number,
			Title:     "invented status",
			Status:    "wontfix",
			Priority:  "normal",
		})
		return err
	})
	if err == nil {
		t.Fatal("a status outside the enum was accepted by the database")
	}
}

// THE SCENARIO MOTIVATING IDEMPOTENCE (FLWL-14) DOES NOT HAPPEN AS DESCRIBED.
//
// "The agent calls create_task, the response is lost, it replays" assumes the first creation went
// through. Yet when the client gives up — 15 s timeout exceeded, session killed, agent interrupted
// — the request context is cancelled, and the transaction goes with it: no row, no number consumed.
// The replay then creates the only task that will ever exist.
//
// The window where a replay really duplicates is the interval between a successful COMMIT and the
// bytes arriving at the client. This test bounds it by proving everything upstream of it.
func TestCancelledRequestCreatesNothing(t *testing.T) {
	st, db := newStore(t)
	sc := newProject(t, db, "CORE")

	ctx, cancel := context.WithCancel(context.Background())
	err := st.WithTx(ctx, func(tx store.Store) error {
		number, err := tx.ClaimNumber(ctx, sc.teamID, sc.projectID)
		if err != nil {
			return err
		}
		if number != 1 {
			t.Errorf("number reserved = %d, want 1", number)
		}

		// The client gives up here, the reservation already made: the worst possible moment.
		cancel()

		_, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    number,
			Title:     "task whose response will be lost",
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	live := context.Background()
	tasks, err := st.ListTasks(live, store.TaskFilter{
		TeamID: sc.teamID, ProjectID: sc.projectID, IncludeArchived: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("reading the backlog: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("%d task(s) created by a cancelled request, want 0", len(tasks))
	}

	// The agent's replay: it is what creates the task, and it does take number 1.
	replayed := createTask(t, st, sc, "task whose response will be lost")
	if replayed.Number != 1 {
		t.Errorf("number = %d after cancellation, want 1 (no number consumed)", replayed.Number)
	}
}

// A number served twice is an inconsistency of the counter, never a caller's fault: the number is
// not an API parameter, it is drawn from projects.next_number. Returning it as a "conflict" would
// answer 409 to an agent that did nothing wrong and would have it retry forever. Decision #23 of
// docs/DESIGN-M3.md, carried over from issue/store/errors.go.
func TestDuplicateNumberIsCorruptionNotConflict(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	first := createTask(t, st, sc, "first task")

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    first.Number,
			Title:     "same number as the first",
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if !errors.Is(err, store.ErrCorrupted) {
		t.Fatalf("error = %v, want ErrCorrupted", err)
	}
	if errors.Is(err, store.ErrConflict) {
		t.Error("a corrupted counter surfaced as a caller conflict")
	}
}

// "Move to done, here is why, and archive" holds in ONE transaction, in the right order.
//
// The order is not indifferent: ever since archiving became a field of the patch, patching first
// archives the task, and CreateTaskNote — whose query carries `t.archived_at IS NULL` — then refuses
// to write into the thread of a task just closed. Reproduced: the most common end-of-session call
// failed entirely on `task store: not found`.
//
// MUTATION: putting the patch back before the note in UpdateTask (service) makes this test fail.
func TestEndOfTaskWritesNoteThenArchivesInOneTransaction(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")
	task := createTask(t, st, sc, "to be finished")

	done := "done"
	var updated store.Task
	err := st.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "delivered"); err != nil {
			return err
		}
		var err error
		updated, err = tx.UpdateTask(ctx, store.TaskPatch{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    task.Number,
			Status:    &done,
			Archive:   true,
		})
		return err
	})
	if err != nil {
		t.Fatalf("end of task in one transaction: %v", err)
	}

	if updated.Status != "done" {
		t.Errorf("status = %q, want done", updated.Status)
	}
	if updated.ArchivedAt == nil {
		t.Error("the task must be archived by the patch itself, with no second write")
	}

	// The note is there, and only once: that is the duplicate the second request produced on every
	// replay.
	notes, total, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if total != 1 || len(notes) != 1 || notes[0].Body != "delivered" {
		t.Errorf("thread = %d note(s) out of %d, want exactly \"delivered\"", len(notes), total)
	}

	// Replaying the same call can no longer duplicate the note: the task is archived, the whole
	// transaction is refused.
	err = st.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "delivered"); err != nil {
			return err
		}
		_, err := tx.UpdateTask(ctx, store.TaskPatch{
			TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number, Archive: true,
		})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("replay: error = %v, want ErrNotFound", err)
	}
	_, total, err = st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes after replay: %v", err)
	}
	if total != 1 {
		t.Errorf("%d notes after replay, want 1: the replay duplicated the note", total)
	}
}
