package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

// insertTask opens a task of the observed project, through direct SQL: the inbox store only
// reads, and borrowing the task feature for its fixtures would make one module depend on another.
func insertTask(t *testing.T, db *sql.DB, f fixture, title, status string) uuid.UUID {
	t.Helper()

	var number int64
	if err := db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1",
		f.core,
	).Scan(&number); err != nil {
		t.Fatalf("reserving the number: %v", err)
	}

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO tasks (team_id, project_id, number, title, status)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		f.teamID, f.core, number, title, status,
	).Scan(&id); err != nil {
		t.Fatalf("creating task %q: %v", title, err)
	}
	return id
}

// insertEdge lays down a blocking edge, released or not.
func insertEdge(t *testing.T, db *sql.DB, f fixture, task, blocker uuid.UUID, setBlocked, released bool) {
	t.Helper()

	releasedAt := "NULL"
	if released {
		releasedAt = "now()"
	}
	if _, err := db.Exec(
		`INSERT INTO task_dependencies (project_id, task_id, blocker_task_id, until_status, set_blocked, released_at)
		 VALUES ($1, $2, $3, 'done', $4, `+releasedAt+`)`,
		f.core, task, blocker, setBlocked,
	); err != nil {
		t.Fatalf("creating the edge: %v", err)
	}
}

// The `unblocked` bucket is a STATE, like the three others: it is recomputed from the edges, not
// replayed from a journal. Its membership condition is "no active edge left, and at least one
// released", and its EXIT condition is the status: picking the task up again takes it out.
func TestUnblockedBucketReflectsTheEdges(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	blocker := insertTask(t, db, f, "the blocker", "done")

	// Belongs to it: all its edges are lifted, and one of them was blocking it.
	freed := insertTask(t, db, f, "freed", "todo")
	insertEdge(t, db, f, freed, blocker, true, true)

	// Belongs to it too: the agent had blocked it for ANOTHER reason, but the obstacle is lifted
	// and it must learn about it. Without it, the notification would depend on who set the block.
	stillBlocked := insertTask(t, db, f, "freed but blocked elsewhere", "blocked")
	insertEdge(t, db, f, stillBlocked, blocker, false, true)

	// Does not belong to it: an edge still blocks it.
	partial := insertTask(t, db, f, "still blocked", "blocked")
	insertEdge(t, db, f, partial, blocker, true, true)
	insertEdge(t, db, f, partial, insertTask(t, db, f, "second blocker", "todo"), false, false)

	// Does not belong to it: the agent picked it up again, so the notification did its job.
	resumed := insertTask(t, db, f, "resumed", "in_progress")
	insertEdge(t, db, f, resumed, blocker, true, true)

	// Does not belong to it: never blocked by anything.
	insertTask(t, db, f, "ordinary", "todo")

	lines, err := st.UnblockedTasks(ctx, scopeOf(f), 0)
	if err != nil {
		t.Fatalf("UnblockedTasks: %v", err)
	}

	titles := make(map[string]string, len(lines))
	for _, line := range lines {
		titles[line.Title] = line.Status
	}
	if len(titles) != 2 {
		t.Fatalf("%d line(s): %v — expected the two tasks whose edges are all lifted",
			len(titles), titles)
	}
	if titles["freed"] != "todo" {
		t.Errorf("status of \"freed\" = %q, expected todo", titles["freed"])
	}
	// The status is the useful piece of the bucket: it says which of the two cases the agent is
	// looking at.
	if titles["freed but blocked elsewhere"] != "blocked" {
		t.Errorf("status = %q, expected blocked: nobody decides in the agent's place",
			titles["freed but blocked elsewhere"])
	}
}

// The "new" flag comes from the token cursor, as everywhere else — and it gates NO line: an
// unblocked task stays in the bucket even once seen. That is what makes an agent whose context
// was compacted find it again.
func TestUnblockedNewFlagFollowsTheCursor(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	blocker := insertTask(t, db, f, "the blocker", "done")
	freed := insertTask(t, db, f, "freed", "todo")
	insertEdge(t, db, f, freed, blocker, true, true)

	var eventID int64
	if err := db.QueryRow(
		`INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $2, 'task.unblocked', 'task', $3) RETURNING id`,
		f.teamID, f.core, freed,
	).Scan(&eventID); err != nil {
		t.Fatalf("journalling: %v", err)
	}

	before, err := st.UnblockedTasks(ctx, scopeOf(f), eventID-1)
	if err != nil {
		t.Fatalf("UnblockedTasks before the cursor: %v", err)
	}
	if len(before) != 1 || !before[0].New {
		t.Fatalf("line not flagged new although the event is later than the cursor: %+v", before)
	}

	after, err := st.UnblockedTasks(ctx, scopeOf(f), eventID)
	if err != nil {
		t.Fatalf("UnblockedTasks after the cursor: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("%d line(s) after the cursor, expected 1: the cursor only drives a flag", len(after))
	}
	if after[0].New {
		t.Error("line still flagged new although the cursor passed the event")
	}
}
