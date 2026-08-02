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

	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/Coddyum/flowlio-ia/internal/feature/task/store"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// scope est la paire de tenancy d'un projet de test : exactement ce qu'un token de projet porte.
type scope struct {
	teamID    uuid.UUID
	projectID uuid.UUID
}

// newStore ouvre la base de test. Sans FLOWLIO_TEST_DATABASE_URL, le test est ignoré : la suite
// unitaire doit rester exécutable sans infrastructure.
func newStore(t *testing.T) (store.Store, *sql.DB) {
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

	return store.New(database.New(db), db), db
}

// newProject crée une team et un projet jetables par SQL direct.
//
// Les fixtures n'empruntent pas le store de la feature workspace : la feature task ne doit
// dépendre d'aucune autre feature, pas même dans ses tests. La suppression de la team emporte le
// reste en cascade, donc la base de test ne dérive pas d'une exécution à l'autre.
func newProject(t *testing.T, db *sql.DB, key string) scope {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id",
		slug, "Team de test",
	).Scan(&teamID)
	if err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", teamID, err)
		}
	})

	var projectID uuid.UUID
	err = db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Projet de test",
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("création du projet %s: %v", key, err)
	}

	return scope{teamID: teamID, projectID: projectID}
}

// newProjectIn crée un second projet dans une team existante : le voisin de palier, celui qui ne
// doit rien voir du backlog du premier.
func newProjectIn(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) scope {
	t.Helper()

	var projectID uuid.UUID
	err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Projet voisin",
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("création du projet %s: %v", key, err)
	}
	return scope{teamID: teamID, projectID: projectID}
}

// createTask ouvre une tâche dans un scope donné, en passant par le chemin nominal du store.
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
		t.Fatalf("création de la tâche %q: %v", title, err)
	}
	return created
}

func TestTaskLifecycle(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "première tâche")
	if task.Number != 1 {
		t.Errorf("première tâche numérotée %d, attendu 1", task.Number)
	}

	second := createTask(t, st, sc, "deuxième tâche")
	if second.Number != 2 {
		t.Errorf("deuxième tâche numérotée %d, attendu 2", second.Number)
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
		t.Errorf("statut = %q, attendu in_progress", updated.Status)
	}
	if updated.Title != task.Title {
		t.Errorf("un champ absent du patch a été écrasé : titre = %q, attendu %q", updated.Title, task.Title)
	}
	if updated.Deadline == nil || !updated.Deadline.UTC().Equal(deadline) {
		t.Errorf("échéance = %v, attendu %v", updated.Deadline, deadline)
	}

	// ClearDeadline doit effacer, là où un pointeur nil signifie « ne change pas ».
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
		t.Errorf("échéance = %v après effacement, attendu nil", cleared.Deadline)
	}

	if _, err := st.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "avancement"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	notes, total, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "avancement" {
		t.Fatalf("ListNotes renvoie %d notes, attendu la note ajoutée", len(notes))
	}
	if total != 1 {
		t.Errorf("total = %d, attendu 1", total)
	}

	archived, err := st.ArchiveTask(ctx, sc.teamID, sc.projectID, task.Number)
	if err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Error("la tâche archivée doit porter une date d'archivage")
	}

	if _, err := st.ArchiveTask(ctx, sc.teamID, sc.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second archivage: erreur = %v, attendu ErrNotFound", err)
	}
}

// Le fil de notes est BORNÉ par la query, et le total reste exact.
//
// Sans LIMIT, `get CORE-34` sérialisait le fil entier : mesuré sur cette base, 1 000 notes de
// 64 KiB donnaient 62,6 Mio en 669 ms, écrites sans être throttlées en 659 ms. C'est l'outil qu'un
// agent appelle pour REPRENDRE une tâche — un fil non borné, c'est un appel qui remplit son
// contexte sur une lecture qu'il croyait anodine.
//
// Ce test utilise des notes de 1 KiB : ce qu'il vérifie n'est pas un volume, c'est que la taille
// rendue NE CROÎT PLUS avec le fil. Le chiffre de 62,6 Mio est une mesure, pas une suite à rejouer
// à chaque `make test-integration`.
//
// MUTATION : retirer `LIMIT @lim` de ListTaskNotes fait tomber ce test sur les trois assertions.
func TestNoteThreadIsBoundedByTheQuery(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")
	task := createTask(t, st, sc, "fil long")

	// created_at est explicite et distinct par note : un INSERT en masse leur donnerait toutes le
	// même now(), et le départage retomberait sur un uuid aléatoire. Le vrai trafic écrit une note
	// par requête, donc une par transaction, donc un created_at par note — c'est ce qu'on simule.
	const written = 1000
	if _, err := db.Exec(`
		INSERT INTO task_notes (task_id, body_md, created_at)
		SELECT $1, repeat('x', 1024) || ' #' || g, now() - make_interval(secs => $2 - g)
		FROM generate_series(1, $2) AS g`,
		task.ID, written,
	); err != nil {
		t.Fatalf("seed du fil: %v", err)
	}

	const window = 10
	notes, total, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, window)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}

	if len(notes) != window {
		t.Errorf("%d notes rendues, attendu %d : le fil n'est pas borné", len(notes), window)
	}
	if total != written {
		t.Errorf("total = %d, attendu %d : l'agent ne sait pas qu'il ne lit qu'une fenêtre", total, written)
	}

	raw, err := json.Marshal(notes)
	if err != nil {
		t.Fatalf("sérialisation: %v", err)
	}
	if len(raw) > 64<<10 {
		t.Errorf("%d octets sérialisés pour %d notes écrites : la taille rendue suit encore le fil",
			len(raw), written)
	}

	// Ce sont les DERNIÈRES notes qui portent l'état, rendues dans l'ordre d'écriture.
	if !strings.HasSuffix(notes[len(notes)-1].Body, "#1000") {
		t.Errorf("dernière note rendue = %q, attendu la note #1000",
			notes[len(notes)-1].Body[max(0, len(notes[len(notes)-1].Body)-8):])
	}
	if !strings.HasSuffix(notes[0].Body, "#991") {
		t.Errorf("première note rendue = %q, attendu la note #991 (fenêtre des 10 dernières)",
			notes[0].Body[max(0, len(notes[0].Body)-8):])
	}
}

// Propriété de sécurité centrale du produit : un token de projet ne voit que son propre backlog.
// Les deux projets sont dans la MÊME team, ce qui est le cas le plus exposé — un filtrage qui ne
// porterait que sur team_id passerait tous les autres tests et échouerait ici.
func TestTasksAreIsolatedAcrossProjectsOfSameTeam(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	front := newProjectIn(t, db, core.teamID, "FRNT")

	task := createTask(t, st, core, "secret de CORE")

	t.Run("lecture", func(t *testing.T) {
		if _, err := st.TaskByNumber(ctx, front.teamID, front.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT lit la tâche de CORE: erreur = %v, attendu ErrNotFound", err)
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
			t.Errorf("FRNT liste %d tâches, attendu 0", len(tasks))
		}
	})

	t.Run("modification", func(t *testing.T) {
		title := "détourné"
		if _, err := st.UpdateTask(ctx, store.TaskPatch{
			TeamID:    front.teamID,
			ProjectID: front.projectID,
			Number:    task.Number,
			Title:     &title,
		}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT modifie la tâche de CORE: erreur = %v, attendu ErrNotFound", err)
		}
	})

	t.Run("note", func(t *testing.T) {
		if _, err := st.AddNote(ctx, front.teamID, front.projectID, task.Number, "intrusion"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT écrit dans le fil de CORE: erreur = %v, attendu ErrNotFound", err)
		}
	})

	t.Run("lecture des notes", func(t *testing.T) {
		notes, _, err := st.ListNotes(ctx, front.teamID, front.projectID, task.Number, 10)
		if err != nil {
			t.Fatalf("ListNotes: %v", err)
		}
		if len(notes) != 0 {
			t.Errorf("FRNT lit %d notes de CORE, attendu 0", len(notes))
		}
	})

	t.Run("archivage", func(t *testing.T) {
		if _, err := st.ArchiveTask(ctx, front.teamID, front.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FRNT archive la tâche de CORE: erreur = %v, attendu ErrNotFound", err)
		}
	})

	// Après toutes ces tentatives, la tâche doit être intacte chez son propriétaire.
	unchanged, err := st.TaskByNumber(ctx, core.teamID, core.projectID, task.Number)
	if err != nil {
		t.Fatalf("TaskByNumber (propriétaire): %v", err)
	}
	if unchanged.Title != "secret de CORE" || unchanged.ArchivedAt != nil {
		t.Errorf("la tâche de CORE a été altérée: %+v", unchanged)
	}
}

// Le team_id d'un scope ne doit jamais suffire à lui seul, et le project_id non plus : une
// requête qui présenterait le bon project_id avec le team_id d'une autre team doit échouer.
func TestTaskScopeRequiresBothTeamAndProject(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	other := newProject(t, db, "CORE")

	task := createTask(t, st, core, "tâche de la team A")

	forged := scope{teamID: other.teamID, projectID: core.projectID}
	if _, err := st.TaskByNumber(ctx, forged.teamID, forged.projectID, task.Number); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("project_id valide + team_id étranger: erreur = %v, attendu ErrNotFound", err)
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
		t.Errorf("le scope forgé liste %d tâches, attendu 0", len(tasks))
	}
}

// Réserver un numéro sur le projet d'une autre team doit être impossible : sinon le compteur
// d'un projet tiers pourrait être avancé sans y avoir accès, et les numéros de ce projet
// deviendraient devinables.
func TestClaimNumberIsScopedToTeam(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()

	core := newProject(t, db, "CORE")
	other := newProject(t, db, "CORE")

	if _, err := st.ClaimNumber(ctx, other.teamID, core.projectID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("réservation croisée: erreur = %v, attendu ErrNotFound", err)
	}

	// Le compteur du projet visé n'a pas bougé : la première tâche légitime porte bien le n° 1.
	task := createTask(t, st, core, "première")
	if task.Number != 1 {
		t.Errorf("numéro = %d après une tentative croisée, attendu 1", task.Number)
	}
}

// La réservation du numéro et l'insertion partagent une transaction : une insertion refusée ne
// doit pas brûler définitivement un numéro dans la suite CORE-1, CORE-2, …
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
			t.Errorf("numéro réservé = %d, attendu 1", number)
		}
		// Titre vide : refusé par la contrainte tasks_title_not_blank, donc la transaction
		// entière est annulée, réservation du numéro comprise.
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
		t.Fatalf("insertion d'un titre vide: erreur = %v, attendu ErrConflict", err)
	}

	task := createTask(t, st, sc, "première vraie tâche")
	if task.Number != 1 {
		t.Errorf("numéro = %d après un échec, attendu 1 (le numéro ne doit pas être brûlé)", task.Number)
	}
}

// Le patch et la note d'un même appel tombent ensemble ou tiennent ensemble.
//
// C'est la garantie sur laquelle repose le repli d'add_task_note dans update_task : sans elle,
// l'état « statut changé, motif perdu » resterait atteignable — la note échoue alors que le done
// est déjà en base, et la session suivante lit un done que rien n'explique.
//
// MUTATION : remplacer le Rollback différé de tx.go par un Commit inconditionnel fait tomber ce
// test sur les deux assertions à la fois.
func TestPatchAndNoteRollBackTogether(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "titre d'origine")

	boom := errors.New("échec après les deux écritures")
	patched := "titre modifié"
	err := st.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.UpdateTask(ctx, store.TaskPatch{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    task.Number,
			Title:     &patched,
		}); err != nil {
			return err
		}
		if _, err := tx.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "note qui ne doit pas survivre"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("erreur = %v, attendu celle renvoyée par fn", err)
	}

	reread, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, task.Number)
	if err != nil {
		t.Fatalf("relecture de la tâche: %v", err)
	}
	if reread.Title != "titre d'origine" {
		t.Errorf("titre = %q après annulation, attendu %q : le patch a été committé seul",
			reread.Title, "titre d'origine")
	}

	notes, _, err := st.ListNotes(ctx, sc.teamID, sc.projectID, task.Number, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("%d note(s) après annulation, attendu 0: %+v", len(notes), notes)
	}
}

func TestArchivedTaskIsNotWritable(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	task := createTask(t, st, sc, "à archiver")
	if _, err := st.ArchiveTask(ctx, sc.teamID, sc.projectID, task.Number); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}

	title := "modification après archivage"
	if _, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID:    sc.teamID,
		ProjectID: sc.projectID,
		Number:    task.Number,
		Title:     &title,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("modification d'une tâche archivée: erreur = %v, attendu ErrNotFound", err)
	}

	if _, err := st.AddNote(ctx, sc.teamID, sc.projectID, task.Number, "note tardive"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("note sur une tâche archivée: erreur = %v, attendu ErrNotFound", err)
	}

	// Elle reste lisible : archiver range, ça n'efface pas.
	if _, err := st.TaskByNumber(ctx, sc.teamID, sc.projectID, task.Number); err != nil {
		t.Errorf("lecture d'une tâche archivée: %v", err)
	}
}

func TestListTasksFilters(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	todo := createTask(t, st, sc, "à faire")
	done := createTask(t, st, sc, "terminée")
	archived := createTask(t, st, sc, "archivée")

	doneStatus := "done"
	if _, err := st.UpdateTask(ctx, store.TaskPatch{
		TeamID: sc.teamID, ProjectID: sc.projectID, Number: done.Number, Status: &doneStatus,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, err := st.ArchiveTask(ctx, sc.teamID, sc.projectID, archived.Number); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}

	base := store.TaskFilter{TeamID: sc.teamID, ProjectID: sc.projectID, Limit: 50}

	t.Run("les archivées sont exclues par défaut", func(t *testing.T) {
		tasks, err := st.ListTasks(ctx, base)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 2 {
			t.Fatalf("%d tâches actives, attendu 2", len(tasks))
		}
		// Tri par numéro décroissant : la plus récente d'abord.
		if tasks[0].Number != done.Number {
			t.Errorf("première tâche n° %d, attendu %d (tri décroissant)", tasks[0].Number, done.Number)
		}
	})

	t.Run("archived inclut les archivées", func(t *testing.T) {
		filter := base
		filter.IncludeArchived = true
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 3 {
			t.Errorf("%d tâches au total, attendu 3", len(tasks))
		}
	})

	t.Run("filtre par statut", func(t *testing.T) {
		filter := base
		filter.Status = "todo"
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 1 || tasks[0].Number != todo.Number {
			t.Errorf("filtre todo renvoie %d tâches, attendu la seule tâche à faire", len(tasks))
		}
	})

	t.Run("la limite est respectée", func(t *testing.T) {
		filter := base
		filter.Limit = 1
		tasks, err := st.ListTasks(ctx, filter)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(tasks) != 1 {
			t.Errorf("%d tâches avec limit=1, attendu 1", len(tasks))
		}
	})
}

// La base porte la garantie du vocabulaire : un statut hors enum est refusé même si la
// validation applicative venait à être contournée.
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
			Title:     "statut inventé",
			Status:    "wontfix",
			Priority:  "normal",
		})
		return err
	})
	if err == nil {
		t.Fatal("un statut hors enum a été accepté par la base")
	}
}

// LE SCÉNARIO QUI MOTIVE L'IDEMPOTENCE (FLWL-14) NE SE PRODUIT PAS TEL QU'IL EST DÉCRIT.
//
// « L'agent appelle create_task, la réponse se perd, il rejoue » suppose que la première
// création a abouti. Or quand le client abandonne — délai de 15 s dépassé, session tuée, agent
// interrompu — le contexte de la requête est annulé, et la transaction l'est avec lui : aucune
// ligne, aucun numéro consommé. Le rejeu crée alors la seule tâche qui existera.
//
// La fenêtre où un rejeu duplique réellement est l'intervalle entre le COMMIT réussi et
// l'arrivée des octets chez le client. Ce test la borne en prouvant tout ce qui est en amont.
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
			t.Errorf("numéro réservé = %d, attendu 1", number)
		}

		// Le client abandonne ici, la réservation déjà faite : c'est le pire moment possible.
		cancel()

		_, err = tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    number,
			Title:     "tâche dont la réponse se perdra",
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur = %v, attendu context.Canceled", err)
	}

	live := context.Background()
	tasks, err := st.ListTasks(live, store.TaskFilter{
		TeamID: sc.teamID, ProjectID: sc.projectID, IncludeArchived: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("lecture du backlog: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("%d tâche(s) créée(s) par une requête annulée, attendu 0", len(tasks))
	}

	// Le rejeu de l'agent : c'est lui qui crée la tâche, et il prend bien le numéro 1.
	replayed := createTask(t, st, sc, "tâche dont la réponse se perdra")
	if replayed.Number != 1 {
		t.Errorf("numéro = %d après annulation, attendu 1 (aucun numéro consommé)", replayed.Number)
	}
}

// Un numéro servi deux fois est une incohérence du compteur, jamais une faute de l'appelant :
// le numéro n'est pas un paramètre d'API, il est tiré de projects.next_number. Le rendre en
// « conflit » ferait répondre 409 à un agent qui n'a rien fait de mal et qui réessaierait
// indéfiniment. Décision #23 de docs/DESIGN-M3.md, portée depuis issue/store/errors.go.
func TestDuplicateNumberIsCorruptionNotConflict(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	sc := newProject(t, db, "CORE")

	first := createTask(t, st, sc, "première tâche")

	err := st.WithTx(ctx, func(tx store.Store) error {
		_, err := tx.CreateTask(ctx, store.NewTask{
			TeamID:    sc.teamID,
			ProjectID: sc.projectID,
			Number:    first.Number,
			Title:     "même numéro que la première",
			Status:    "todo",
			Priority:  "normal",
		})
		return err
	})
	if !errors.Is(err, store.ErrCorrupted) {
		t.Fatalf("erreur = %v, attendu ErrCorrupted", err)
	}
	if errors.Is(err, store.ErrConflict) {
		t.Error("un compteur corrompu est remonté comme un conflit d'appelant")
	}
}
