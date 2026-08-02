package store_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-ia/internal/database"
	inboxstore "github.com/Coddyum/flowlio-ia/internal/feature/inbox/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// fixture porte une team, deux projets qui se parlent et le token du projet observé.
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
		t.Skip("FLOWLIO_TEST_DATABASE_URL non renseigné — test d'intégration ignoré")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("ouverture de la base: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("base injoignable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return inboxstore.New(database.New(db)), db
}

// newFixture crée une team, les projets WEB et CORE, et un token de projet pour CORE : c'est le
// point de vue depuis lequel l'inbox est observée.
func newFixture(t *testing.T, db *sql.DB) fixture {
	t.Helper()

	f := fixture{db: db}
	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Team de test",
	).Scan(&f.teamID); err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", f.teamID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", f.teamID, err)
		}
	})

	for key, dest := range map[string]*uuid.UUID{"WEB": &f.web, "CORE": &f.core} {
		if err := db.QueryRow(
			"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
			f.teamID, key, "Projet "+key,
		).Scan(dest); err != nil {
			t.Fatalf("création du projet %s: %v", key, err)
		}
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if err := db.QueryRow(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope)
		 VALUES ($1, $2, 'agent', $3, 'hash-de-test', 'project') RETURNING id`,
		f.teamID, f.core, prefix,
	).Scan(&f.tokenID); err != nil {
		t.Fatalf("création du token: %v", err)
	}

	return f
}

// openIssue insère une issue et son événement par SQL direct : la feature inbox ne doit dépendre
// d'aucune autre feature, pas même dans ses tests.
func openIssue(t *testing.T, f fixture, from, to uuid.UUID, title, state, body string) uuid.UUID {
	t.Helper()

	var number int64
	if err := f.db.QueryRow(
		"UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1", to,
	).Scan(&number); err != nil {
		t.Fatalf("réservation du numéro: %v", err)
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
		t.Fatalf("création de l'issue %q: %v", title, err)
	}

	if _, err := f.db.Exec(
		"INSERT INTO issue_messages (issue_id, author_project_id, body_md) VALUES ($1, $2, $3)",
		issueID, from, body,
	); err != nil {
		t.Fatalf("premier message: %v", err)
	}

	if _, err := f.db.Exec(
		`INSERT INTO events (team_id, project_id, actor_project_id, kind, subject_type, subject_id)
		 VALUES ($1, $2, $3, 'issue.opened', 'issue', $4)`,
		f.teamID, to, from, issueID,
	); err != nil {
		t.Fatalf("événement: %v", err)
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

// L'inbox renvoie l'ÉTAT, pas un flux. Chaque seau est dérivé de issues.state / tasks.status.
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
		t.Errorf("clé = %q, attendu CORE", key)
	}

	openIssue(t, f, f.web, f.core, "WEB attend CORE", "open", "peux-tu regarder ?")
	openIssue(t, f, f.core, f.web, "CORE a eu sa réponse", "answered", "voilà la réponse")
	openIssue(t, f, f.web, f.core, "déjà réglée", "closed", "réglé")

	cursor, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if cursor.LastEventID != 0 {
		t.Errorf("curseur d'un token neuf = %d, attendu 0", cursor.LastEventID)
	}
	if cursor.HeadEventID == 0 {
		t.Fatal("tête du journal à 0 alors que des événements ont été écrits")
	}

	incoming, err := st.IncomingOpen(ctx, sc, cursor.LastEventID)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 1 || incoming[0].Title != "WEB attend CORE" {
		t.Fatalf("%d issues entrantes ouvertes, attendu la seule question de WEB", len(incoming))
	}
	if incoming[0].PeerKey != "WEB" {
		t.Errorf("pair = %q, attendu WEB", incoming[0].PeerKey)
	}
	if incoming[0].Excerpt != "peux-tu regarder ?" {
		t.Errorf("extrait = %q, attendu le dernier message", incoming[0].Excerpt)
	}
	if !incoming[0].New {
		t.Error("l'issue doit être marquée nouvelle : le curseur d'un token neuf est à 0")
	}

	answered, err := st.OutgoingAnswered(ctx, sc, cursor.LastEventID)
	if err != nil {
		t.Fatalf("OutgoingAnswered: %v", err)
	}
	if len(answered) != 1 || answered[0].PeerKey != "WEB" {
		t.Fatalf("%d issues sortantes répondues, attendu 1 chez WEB", len(answered))
	}
}

// Le curseur ne pilote QUE le drapeau « nouveau ». Après l'avoir avancé, les mêmes lignes
// reviennent : l'état n'a pas changé, donc il reste à traiter.
func TestCursorOnlyDrivesTheNewFlag(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)
	sc := scopeOf(f)

	openIssue(t, f, f.web, f.core, "question en attente", "open", "?")

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
		t.Errorf("curseur = %d après avancement, attendu %d", after.LastEventID, cursor.HeadEventID)
	}

	incoming, err := st.IncomingOpen(ctx, sc, after.LastEventID)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 1 {
		t.Fatalf("%d issues après avancement du curseur, attendu 1 : "+
			"une question sans réponse reste à traiter", len(incoming))
	}
	if incoming[0].New {
		t.Error("l'issue ne doit plus être marquée nouvelle après avancement du curseur")
	}

	// Le curseur ne recule jamais, même si un appel concurrent présente une position ancienne.
	if err := st.Advance(ctx, f.tokenID, 0); err != nil {
		t.Fatalf("Advance (position ancienne): %v", err)
	}
	back, err := st.Cursor(ctx, sc)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if back.LastEventID != cursor.HeadEventID {
		t.Errorf("curseur = %d, attendu %d : il ne doit jamais reculer",
			back.LastEventID, cursor.HeadEventID)
	}
}

// L'inbox d'un projet ne montre jamais l'activité d'un projet tiers, même dans la même team.
func TestInboxIsScopedToItsProject(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	f := newFixture(t, db)

	var spy uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, 'SPY', 'Projet SPY') RETURNING id",
		f.teamID,
	).Scan(&spy); err != nil {
		t.Fatalf("création du projet SPY: %v", err)
	}

	openIssue(t, f, f.web, f.core, "entre WEB et CORE", "open", "privé")

	spyScope := inboxstore.Scope{TokenID: f.tokenID, TeamID: f.teamID, ProjectID: spy, Limit: 11}

	incoming, err := st.IncomingOpen(ctx, spyScope, 0)
	if err != nil {
		t.Fatalf("IncomingOpen: %v", err)
	}
	if len(incoming) != 0 {
		t.Errorf("SPY voit %d issues entrantes, attendu 0", len(incoming))
	}

	answered, err := st.OutgoingAnswered(ctx, spyScope, 0)
	if err != nil {
		t.Fatalf("OutgoingAnswered: %v", err)
	}
	if len(answered) != 0 {
		t.Errorf("SPY voit %d issues répondues, attendu 0", len(answered))
	}

	tasks, err := st.InProgressTasks(ctx, spyScope)
	if err != nil {
		t.Fatalf("InProgressTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("SPY voit %d tâches, attendu 0", len(tasks))
	}
}

// Une tâche en cours signale une session interrompue : c'est ce qu'un agent doit reprendre en
// premier. Les tâches archivées ou terminées n'y figurent pas.
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
			t.Fatalf("réservation du numéro: %v", err)
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
			t.Fatalf("création de la tâche %q: %v", title, err)
		}
	}

	insert("en cours", "in_progress", false)
	insert("à faire", "todo", false)
	insert("terminée", "done", false)
	insert("en cours mais archivée", "in_progress", true)

	tasks, err := st.InProgressTasks(ctx, scopeOf(f))
	if err != nil {
		t.Fatalf("InProgressTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "en cours" {
		t.Fatalf("%d tâches, attendu la seule tâche en cours non archivée", len(tasks))
	}
}
