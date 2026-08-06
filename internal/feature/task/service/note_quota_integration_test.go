package service_test

// FLWL-70, PART 5 — the quota and the note it charges for live or die TOGETHER.
//
// WHY THIS FILE EXISTS ON TOP OF the store's integration test and the service's unit tests. Those
// two prove the predicate and the wiring; neither can prove that the charge and the note share a
// fate, because that property IS the transaction and a double has none. Break the pairing and the
// counter stops describing the thread — which is worse than having no quota at all, since a
// counter that has drifted refuses notes for storage nobody is using.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// projectNoteBytes reads the project counter; threadSize sums what the thread actually holds.
// Comparing the two is the whole point: they must never diverge.
func projectNoteBytes(t *testing.T, db *sql.DB, sc projectScope) int64 {
	t.Helper()
	var got int64
	if err := db.QueryRow("SELECT note_bytes FROM projects WHERE id = $1", sc.projectID).Scan(&got); err != nil {
		t.Fatalf("reading note_bytes: %v", err)
	}
	return got
}

func threadSize(t *testing.T, db *sql.DB, sc projectScope) int64 {
	t.Helper()
	var got int64
	err := db.QueryRow(`
		SELECT coalesce(sum(octet_length(n.body_md)), 0)
		FROM task_notes n JOIN tasks t ON t.id = n.task_id
		WHERE t.project_id = $1`, sc.projectID).Scan(&got)
	if err != nil {
		t.Fatalf("measuring the thread: %v", err)
	}
	return got
}

// A note written through the nominal path moves the counter by exactly what the thread gained.
//
// The two figures are read from Postgres, and `octet_length()` is what the migration's backfill
// uses: this is where a Go-side rune count would show up as a drift.
func TestAWrittenNoteAndTheCounterAgree(t *testing.T) {
	svc, db := newRealService(t)
	sc := newRealProject(t, db, "QTA")
	task := openTask(t, svc, sc, "a task with a thread")

	for _, body := range []string{"first", "déployé en préproduction", strings.Repeat("x", 900)} {
		note := body
		if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
			TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number,
			Note: &note,
		}); err != nil {
			t.Fatalf("writing the note %q: %v", body[:min(len(body), 20)], err)
		}
	}

	counter, thread := projectNoteBytes(t, db, sc), threadSize(t, db, sc)
	if counter != thread {
		t.Errorf("note_bytes = %d, thread = %d — the counter no longer describes what is stored",
			counter, thread)
	}
	if thread == 0 {
		t.Fatal("the thread is empty: nothing was measured")
	}
}

// A REFUSED NOTE LEAVES NOTHING BEHIND. The counter sits one byte under the bound, so the write is
// refused — and the thread must be exactly as it was, note included.
//
// If the note survived its refused charge, an agent would keep writing for free: every note past
// the bound would be stored and none would be counted, which is precisely the runaway loop the
// quota exists to stop.
//
// MUTATION: move `ChargeNoteBytes` out of the WithTx closure — this test goes red on the thread.
func TestARefusedNoteIsNotStored(t *testing.T) {
	svc, db := newRealService(t)
	sc := newRealProject(t, db, "QTB")
	task := openTask(t, svc, sc, "a task at the bound")

	before := "the last note that fits"
	if _, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number, Note: &before,
	}); err != nil {
		t.Fatalf("writing the first note: %v", err)
	}
	sizeBefore := threadSize(t, db, sc)

	if _, err := db.Exec("UPDATE projects SET note_bytes = $1 WHERE id = $2",
		store.ProjectNoteBytesQuota, sc.projectID); err != nil {
		t.Fatalf("preloading the counter: %v", err)
	}

	refused := "one line too many"
	status := "in_progress"
	_, err := svc.UpdateTask(context.Background(), service.UpdateTaskInput{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: task.Number,
		Note: &refused, Status: &status,
	})
	if !errors.Is(err, service.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}

	if got := threadSize(t, db, sc); got != sizeBefore {
		t.Errorf("thread = %d bytes after the refusal, want %d — the note was stored anyway, so "+
			"every note past the bound is now free storage", got, sizeBefore)
	}

	// The patch travelling with the refused note rolls back too: the status must not have moved.
	// Otherwise the state "moved to in_progress, reason lost" — the very one folding the note into
	// the patch removed — comes back through the quota.
	var stored string
	if err := db.QueryRow("SELECT status::text FROM tasks WHERE project_id = $1 AND number = $2",
		sc.projectID, task.Number).Scan(&stored); err != nil {
		t.Fatalf("reading the task back: %v", err)
	}
	if stored != task.Status {
		t.Errorf("status = %q, want %q — the patch was committed although its note was refused",
			stored, task.Status)
	}
}
