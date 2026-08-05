package ref_test

// WHAT THIS FILE LOCKS DOWN: that composing two features through the FeatureRegistry did not
// create a THIRD read path with its own idea of tenancy.
//
// `ref` owns no table. Everything it returns comes from `task` and `issue`, and both carry their
// scope inside their queries — team_id AND project_id, on every read. The risk this module
// introduces is not that those queries lose their scope; it is that a reference arrives at the
// WRONG ONE. A sibling's key sent to the task module would be answered from the caller's own
// backlog: the query is perfectly scoped, finds a row, and hands an agent its own task 34 under a
// reference that names somebody else's project.
//
// THE MODULES ARE BUILT BY NewModule AND REGISTERED FOR REAL. Rewiring the composition here would
// prove the test's own wiring. What is under test is what buildModules() assembles — including the
// fact that `ref` resolves its peers at request time, which is the only reason its position in
// that slice does not matter.
//
// WHAT IT DOES NOT COVER: which peer gets asked, and in which order. No HTTP response can see
// that — a correct answer reached by asking the wrong module first looks identical from here.
// That is service/resolve_ref_test.go's half, on doubles that record what they were asked.

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core"
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref"
	"github.com/Coddyum/flowlio-agents/internal/feature/task"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// project is a test project and its tenancy scope: exactly what a token carries.
type project struct {
	teamID uuid.UUID
	id     uuid.UUID
	key    string
}

// coreDouble is the minimal CoreServices a module needs: the auth service, and nothing else.
type coreDouble struct{ svc auth.Service }

func (c coreDouble) Auth() auth.Service { return c.svc }

// resolved is what a caller observes of a resolution.
type resolved struct {
	status int
	Kind   string          `json:"kind"`
	Ref    string          `json:"ref"`
	Task   json.RawMessage `json:"task"`
	Issue  json.RawMessage `json:"issue"`
}

func openDB(t *testing.T) *sql.DB {
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

	return db
}

// newTeam creates a throwaway team. Deleting it cascades over everything below it.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "ref-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Team de test ref",
	).Scan(&teamID); err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", teamID, err)
		}
	})
	return teamID
}

func newProject(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) project {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Projet "+key,
	).Scan(&id); err != nil {
		t.Fatalf("création du projet %s: %v", key, err)
	}
	return project{teamID: teamID, id: id, key: key}
}

// newTask writes a task straight into the table, number included.
//
// By SQL and not through the API on purpose: this file must be able to put a task and an issue
// under the SAME number in two different projects, which is the collision the shared counter makes
// ordinary and which is exactly what the key gate exists to survive.
func newTask(t *testing.T, db *sql.DB, p project, number int64, title string) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT INTO tasks (team_id, project_id, number, title) VALUES ($1, $2, $3, $4)",
		p.teamID, p.id, number, title,
	); err != nil {
		t.Fatalf("création de la tâche %s-%d: %v", p.key, number, err)
	}
}

// newIssue writes an issue from author to recipient. The recipient owns the issue and its number.
func newIssue(t *testing.T, db *sql.DB, recipient, author project, number int64, title string) {
	t.Helper()

	if _, err := db.Exec(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title)
		 VALUES ($1, $2, $3, $4, $5)`,
		recipient.teamID, recipient.id, author.id, number, title,
	); err != nil {
		t.Fatalf("création de l'issue %s-%d: %v", recipient.key, number, err)
	}
}

// serveRef mounts the REAL composition — task, issue and ref built by NewModule and registered —
// and serves ref's routes as the caller sees them.
func serveRef(t *testing.T, db *sql.DB, caller project) (*httptest.Server, authtest.Token) {
	t.Helper()

	tok := authtest.Project(t, caller.teamID, caller.id)
	registry := core.NewRegistry()
	cfg := module.ModuleConfig{
		DB:       database.New(db),
		RawDB:    db,
		Core:     coreDouble{svc: tok.Auth},
		Registry: registry,
	}

	var refMod module.Module
	for _, m := range []module.Module{task.NewModule(cfg), issue.NewModule(cfg), ref.NewModule(cfg)} {
		registry.Register(m.Key(), m)
		if m.Key() == ref.Key {
			refMod = m
		}
	}
	if refMod == nil {
		t.Fatal("le module ref n'a pas été monté")
	}

	ts := httptest.NewServer(refMod.Routes())
	t.Cleanup(ts.Close)
	return ts, tok
}

// get resolves a reference through the real HTTP surface.
func get(t *testing.T, ts *httptest.Server, tok authtest.Token, projectKey string, number int64) resolved {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet,
		ts.URL+"/"+projectKey+"/"+strconv.FormatInt(number, 10), nil)
	if err != nil {
		t.Fatalf("construction de la requête %s-%d: %v", projectKey, number, err)
	}

	resp, err := ts.Client().Do(tok.Authorize(req))
	if err != nil {
		t.Fatalf("appel %s-%d: %v", projectKey, number, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lecture du corps pour %s-%d: %v", projectKey, number, err)
	}

	out := resolved{status: resp.StatusCode}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("réponse illisible pour %s-%d: %v (%s)", projectKey, number, err, body)
		}
	}
	return out
}

// TestResolutionStaysInsideTheCallerScope — the whole point of this module, in one table.
//
// The two WITNESSES open it and are not decorative: a run where nothing resolves would satisfy
// every refusal below while proving nothing at all.
//
// MUTATION PLAYED: in resolve_ref.go, `if in.ProjectKey == ownKey` removed. The case "clé d'un
// frère, numéro qui existe chez moi" comes back 200 with kind=task — the caller's OWN task 34,
// under the reference FRNT-34. Red here on the status, and red on nothing else in the repository.
//
// MUTATION PLAYED: in task/provider.go, `scope.ProjectID` replaced by `scope.TeamID`. Witness 1
// falls to 404 — a task read on a project id that is a team id finds nothing.
//
// MUTATION PLAYED AND FOUND UNNECESSARY, WHICH IS WORTH RECORDING: removing the visibility clause
// from GetIssueByRef (issues.sql) does not compile — sqlc drops the parameter, and the issue store
// stops building. The compiler guards that one; this file guards the thing it cannot see, which is
// that the composition still ROUTES a third party's reference to the query in the first place. The
// case "issue entre deux tiers" is the positive check that it does.
func TestResolutionStaysInsideTheCallerScope(t *testing.T) {
	db := openDB(t)
	team := newTeam(t, db)

	caller := newProject(t, db, team, "CORE")
	sibling := newProject(t, db, team, "FRNT")
	third := newProject(t, db, team, "OPS")

	// Le compteur est partagé entre tâches et issues, donc les collisions de numéro entre projets
	// sont la norme, pas un cas tordu : c'est exactement ce que la garde de clé doit survivre.
	newTask(t, db, caller, 34, "ma tâche 34")
	newTask(t, db, sibling, 34, "la tâche 34 du frère")
	newIssue(t, db, caller, sibling, 12, "question entrante")  // FRNT → CORE
	newIssue(t, db, sibling, caller, 7, "question sortante")   // CORE → FRNT
	newIssue(t, db, third, sibling, 1, "conversation de tiers") // FRNT → OPS

	ts, tok := serveRef(t, db, caller)

	cas := []struct {
		nom    string
		key    string
		number int64
		status int
		kind   string
		ref    string
	}{
		{"témoin — ma tâche", caller.key, 34, http.StatusOK, "task", "CORE-34"},
		{"témoin — issue entrante", caller.key, 12, http.StatusOK, "issue", "CORE-12"},
		{"issue sortante, clé du destinataire", sibling.key, 7, http.StatusOK, "issue", "FRNT-7"},

		{"clé d'un frère, numéro qui existe chez moi", sibling.key, 34, http.StatusNotFound, "", ""},
		{"tâche d'un frère, sous sa propre clé", sibling.key, 99, http.StatusNotFound, "", ""},
		{"issue entre deux tiers", third.key, 1, http.StatusNotFound, "", ""},
		{"numéro qui n'existe nulle part", caller.key, 4242, http.StatusNotFound, "", ""},
		{"clé inconnue", "ZZZZ", 1, http.StatusNotFound, "", ""},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got := get(t, ts, tok, c.key, c.number)

			if got.status != c.status {
				t.Fatalf("statut = %d, attendu %d pour %s-%d", got.status, c.status, c.key, c.number)
			}
			if c.status != http.StatusOK {
				return
			}
			if got.Kind != c.kind {
				t.Errorf("kind = %q, attendu %q", got.Kind, c.kind)
			}
			if got.Ref != c.ref {
				t.Errorf("ref = %q, attendu %q", got.Ref, c.ref)
			}
			// Exactement une charge, jamais les deux : l'agent branche sur `kind`, et deux charges
			// lui laisseraient le choix de la mauvaise — celle qui n'a pas été balisée.
			if (len(got.Task) > 0) == (len(got.Issue) > 0) {
				t.Errorf("charges rendues: task=%d octets, issue=%d octets — il en faut exactement une",
					len(got.Task), len(got.Issue))
			}
		})
	}
}

// Une clé en minuscules est acceptée : c'est une question de FORME d'entrée, et la refuser
// coûterait un aller-retour à un agent qui a recopié une référence d'un message.
func TestLowerCaseKeyResolvesTheSameReference(t *testing.T) {
	db := openDB(t)
	team := newTeam(t, db)

	caller := newProject(t, db, team, "CORE")
	newTask(t, db, caller, 5, "tâche cinq")

	ts, tok := serveRef(t, db, caller)

	got := get(t, ts, tok, "core", 5)
	if got.status != http.StatusOK {
		t.Fatalf("statut = %d, attendu 200 pour core-5", got.status)
	}
	if got.Ref != "CORE-5" {
		t.Errorf("ref = %q, attendu CORE-5 — la référence rendue est normalisée", got.Ref)
	}
}
