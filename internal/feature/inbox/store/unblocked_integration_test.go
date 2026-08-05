package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

// insertTask ouvre une tâche du projet observé, par SQL direct : le store inbox ne fait que lire,
// et emprunter la feature task pour ses fixtures ferait dépendre un module d'un autre.
func insertTask(t *testing.T, db *sql.DB, f fixture, title, status string) uuid.UUID {
	t.Helper()

	var number int64
	if err := db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1",
		f.core,
	).Scan(&number); err != nil {
		t.Fatalf("réservation du numéro: %v", err)
	}

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO tasks (team_id, project_id, number, title, status)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		f.teamID, f.core, number, title, status,
	).Scan(&id); err != nil {
		t.Fatalf("création de la tâche %q: %v", title, err)
	}
	return id
}

// insertEdge pose une arête de blocage, libérée ou non.
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
		t.Fatalf("création de l'arête: %v", err)
	}
}

// Le seau `unblocked` est un ÉTAT, comme les trois autres : il est recalculé depuis les arêtes, pas
// rejoué depuis un journal. Sa condition d'appartenance est « plus aucune arête active, et au moins
// une libérée », et sa condition de SORTIE est le statut : reprendre la tâche l'en retire.
func TestUnblockedBucketReflectsTheEdges(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	blocker := insertTask(t, db, f, "la bloquante", "done")

	// Y appartient : toutes ses arêtes sont levées, et c'est l'une d'elles qui l'avait bloquée.
	freed := insertTask(t, db, f, "libérée", "todo")
	insertEdge(t, db, f, freed, blocker, true, true)

	// Y appartient aussi : l'agent l'avait bloquée pour une AUTRE raison, mais l'obstacle est levé
	// et il doit l'apprendre. Sans elle, la notification dépendrait de qui a posé le blocage.
	stillBlocked := insertTask(t, db, f, "libérée mais bloquée ailleurs", "blocked")
	insertEdge(t, db, f, stillBlocked, blocker, false, true)

	// N'y appartient pas : une arête la bloque encore.
	partial := insertTask(t, db, f, "encore bloquée", "blocked")
	insertEdge(t, db, f, partial, blocker, true, true)
	insertEdge(t, db, f, partial, insertTask(t, db, f, "seconde bloquante", "todo"), false, false)

	// N'y appartient pas : l'agent l'a reprise, donc la notification a fait son travail.
	resumed := insertTask(t, db, f, "reprise", "in_progress")
	insertEdge(t, db, f, resumed, blocker, true, true)

	// N'y appartient pas : jamais bloquée par rien.
	insertTask(t, db, f, "ordinaire", "todo")

	lines, err := st.UnblockedTasks(ctx, scopeOf(f), 0)
	if err != nil {
		t.Fatalf("UnblockedTasks: %v", err)
	}

	titles := make(map[string]string, len(lines))
	for _, line := range lines {
		titles[line.Title] = line.Status
	}
	if len(titles) != 2 {
		t.Fatalf("%d ligne(s): %v — attendu les deux tâches dont toutes les arêtes sont levées",
			len(titles), titles)
	}
	if titles["libérée"] != "todo" {
		t.Errorf("statut de « libérée » = %q, attendu todo", titles["libérée"])
	}
	// Le statut est l'information utile du seau : il dit lequel des deux cas l'agent regarde.
	if titles["libérée mais bloquée ailleurs"] != "blocked" {
		t.Errorf("statut = %q, attendu blocked : personne ne décide à la place de l'agent",
			titles["libérée mais bloquée ailleurs"])
	}
}

// Le drapeau « nouveau » vient du curseur du token, comme partout ailleurs — et il ne conditionne
// AUCUNE ligne : une tâche débloquée reste dans le seau même une fois vue. C'est ce qui fait qu'un
// agent dont le contexte a été compacté la retrouve.
func TestUnblockedNewFlagFollowsTheCursor(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	blocker := insertTask(t, db, f, "la bloquante", "done")
	freed := insertTask(t, db, f, "libérée", "todo")
	insertEdge(t, db, f, freed, blocker, true, true)

	var eventID int64
	if err := db.QueryRow(
		`INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $2, 'task.unblocked', 'task', $3) RETURNING id`,
		f.teamID, f.core, freed,
	).Scan(&eventID); err != nil {
		t.Fatalf("journalisation: %v", err)
	}

	before, err := st.UnblockedTasks(ctx, scopeOf(f), eventID-1)
	if err != nil {
		t.Fatalf("UnblockedTasks avant le curseur: %v", err)
	}
	if len(before) != 1 || !before[0].New {
		t.Fatalf("ligne non marquée nouvelle alors que l'événement est postérieur au curseur: %+v", before)
	}

	after, err := st.UnblockedTasks(ctx, scopeOf(f), eventID)
	if err != nil {
		t.Fatalf("UnblockedTasks après le curseur: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("%d ligne(s) après le curseur, attendu 1 : le curseur ne pilote qu'un drapeau", len(after))
	}
	if after[0].New {
		t.Error("ligne encore marquée nouvelle alors que le curseur a dépassé l'événement")
	}
}
