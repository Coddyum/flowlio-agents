package store_test

// GARANTIES 5 À 16 DU TABLEAU DE docs/DESIGN-TUI.md § « Garanties de sécurité ».
//
// `make check` NE PROUVE RIEN DE CE FICHIER : sans FLOWLIO_TEST_DATABASE_URL, tout est ignoré.
// La recette de ce module est `make test-integration`.
//
// LA FIXTURE PORTE DEUX TEAMS AVEC DES CLÉS HOMONYMES. Les deux ont un `CORE` et un `WEB`. Un
// scope qui ne porterait que sur `key` — le défaut le plus naturel à écrire — passerait tous les
// tests d'une fixture à une seule team, et échouerait ici. C'est la seule raison d'être de cette
// forme, et il ne faut pas la simplifier.
//
// Les insertions sont en SQL DIRECT et non par les features `task` et `issue` : un test qui
// passerait par elles prouverait la cohérence de leurs propres règles, et surtout ne pourrait pas
// fabriquer les états illégaux que la moitié de ces tests existe pour refuser — un message dont
// l'auteur appartient à une autre team, par exemple.

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

// team porte une team de test et les projets qu'on lui a créés, indexés par clé.
type team struct {
	id       uuid.UUID
	slug     string
	projects map[string]uuid.UUID
}

// fixture porte les deux teams. B est la VOISINE : rien de ce qu'elle contient ne doit jamais
// apparaître dans une lecture de A.
type fixture struct {
	db *sql.DB
	a  team
	b  team
}

func newStore(t *testing.T) (overviewstore.Store, *sql.DB) {
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

	return overviewstore.New(database.New(db)), db
}

// newTeam crée une team jetable et ses projets. La suppression de la team emporte tout le reste
// en cascade.
func newTeam(t *testing.T, db *sql.DB, keys ...string) team {
	t.Helper()

	tm := team{slug: "test-" + strings.ToLower(uuid.NewString()[:8]), projects: map[string]uuid.UUID{}}
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", tm.slug, "Team de test",
	).Scan(&tm.id); err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", tm.id); err != nil {
			t.Errorf("nettoyage de la team %s: %v", tm.id, err)
		}
	})

	for _, key := range keys {
		var id uuid.UUID
		if err := db.QueryRow(
			"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
			tm.id, key, "Projet "+key,
		).Scan(&id); err != nil {
			t.Fatalf("création du projet %s: %v", key, err)
		}
		tm.projects[key] = id
	}
	return tm
}

// newFixture crée les deux teams avec des clés HOMONYMES, plus un DOCS inactif côté A.
func newFixture(t *testing.T, db *sql.DB) fixture {
	t.Helper()
	return fixture{
		db: db,
		a:  newTeam(t, db, "CORE", "WEB", "DOCS"),
		b:  newTeam(t, db, "CORE", "WEB"),
	}
}

// openIssue insère une issue. state et updatedAt sont fournis : le vieillissement d'une file est
// exactement ce que cette surface rend, donc il doit être contrôlé par le test.
func openIssue(t *testing.T, db *sql.DB, tm team, dest, author string, number int64, title, state string, updatedAt time.Time) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6::issue_state, $7) RETURNING id`,
		tm.id, tm.projects[dest], tm.projects[author], number, title, state, updatedAt,
	).Scan(&id); err != nil {
		t.Fatalf("création de l'issue %s-%d: %v", dest, number, err)
	}
	return id
}

// addMessage insère un message dans un fil. authorProjectID est passé NU, sans contrôle de team :
// c'est précisément l'état illégal que la garantie 14 doit refuser à la lecture, et la FK simple
// d'issue_messages permet de l'écrire.
func addMessage(t *testing.T, db *sql.DB, issueID, authorProjectID uuid.UUID, body string, createdAt time.Time) {
	t.Helper()

	if _, err := db.Exec(
		`INSERT INTO issue_messages (issue_id, author_project_id, body_md, created_at)
		 VALUES ($1, $2, $3, $4)`,
		issueID, authorProjectID, body, createdAt,
	); err != nil {
		t.Fatalf("création du message: %v", err)
	}
}

// addTask insère une tâche. updatedAt pilote la dormance : le seuil vit dans le service, donc un
// test qui veut une tâche « morte » la fabrique par sa date, pas par un réglage.
func addTask(t *testing.T, db *sql.DB, tm team, key string, number int64, title, status string, updatedAt time.Time) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO tasks (team_id, project_id, number, title, status, updated_at)
		 VALUES ($1, $2, $3, $4, $5::task_status, $6) RETURNING id`,
		tm.id, tm.projects[key], number, title, status, updatedAt,
	).Scan(&id); err != nil {
		t.Fatalf("création de la tâche %s-%d: %v", key, number, err)
	}
	return id
}

// addNote insère une note de progression sur une tâche.
func addNote(t *testing.T, db *sql.DB, taskID uuid.UUID, body string, createdAt time.Time) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT INTO task_notes (task_id, body_md, created_at) VALUES ($1, $2, $3)",
		taskID, body, createdAt,
	); err != nil {
		t.Fatalf("création de la note: %v", err)
	}
}

// addToken insère un token de projet et son dernier usage. lastUsed nul ⇒ un token qui n'a jamais
// servi, c'est-à-dire le cas nominal du premier jour.
func addToken(t *testing.T, db *sql.DB, tm team, key string, lastUsed *time.Time) {
	t.Helper()

	// Un token admin ne porte NI team NI projet — `tokens_scope_shape` depuis la migration
	// 000006. Une clé vide fabrique donc l'admin global, le seul que l'amorçage crée réellement.
	teamID, projectID, scope := any(tm.id), any(tm.projects[key]), "project"
	if key == "" {
		teamID, projectID, scope = nil, nil, "admin"
	}

	prefix := strings.ToLower(uuid.NewString()[:8]) + "abcd"
	if _, err := db.Exec(
		`INSERT INTO tokens (team_id, project_id, name, prefix, secret_hash, scope, last_used_at)
		 VALUES ($1, $2, 'agent', $3, 'hash-de-test', $4::token_scope, $5)`,
		teamID, projectID, prefix, scope, lastUsed,
	); err != nil {
		t.Fatalf("création du token: %v", err)
	}
}

// refs rend l'ensemble des références d'une file d'issues, pour comparer des ENSEMBLES EXACTS.
//
// « ne contient rien de la voisine » passerait aussi sur un résultat vide, c'est-à-dire sur une
// query cassée. Comparer l'ensemble exact refuse les deux erreurs d'un coup.
func refs(debts []overviewstore.IssueDebt) map[string]bool {
	out := make(map[string]bool, len(debts))
	for _, d := range debts {
		out[fmt.Sprintf("%s-%d", d.ProjectKey, d.Number)] = true
	}
	return out
}

// taskRefs rend l'ensemble des références d'une file de tâches.
func taskRefs(debts []overviewstore.TaskDebt) map[string]bool {
	out := make(map[string]bool, len(debts))
	for _, d := range debts {
		out[fmt.Sprintf("%s-%d", d.ProjectKey, d.Number)] = true
	}
	return out
}

// assertSet compare un ensemble obtenu à l'ensemble attendu, et nomme les deux écarts.
func assertSet(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()

	wanted := make(map[string]bool, len(want))
	for _, w := range want {
		wanted[w] = true
		if !got[w] {
			t.Errorf("%s absent de la file — la lecture ne voit plus tout ce qu'elle doit voir", w)
		}
	}
	for g := range got {
		if !wanted[g] {
			t.Errorf("%s présent dans la file — une ligne d'une autre team a fuité", g)
		}
	}
}

var ctx = context.Background()
