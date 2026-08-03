package issue_test

// T1 — LE REFUS INDISCERNABLE, GARDÉ PLUTÔT QUE CÂBLÉ.
//
// `docs/DESIGN-TRUST.md` § Le refus indiscernable (canaux 1 et 2) annonce que les trois refus de
// `create_issue` — clé inconnue, clé d'une autre team, paire de confiance non déclarée — sont
// identiques OCTET POUR OCTET. Jusqu'à FLWL-45 cette propriété n'était vraie que par CÂBLAGE : le
// refus empruntait le chemin d'erreur commun, donc il rendait la bonne chose, mais aucun test ne
// l'observait. La mutation M3 — un `if` dans `internal/feature/issue/handler/handler.go` rendant
// `403` sur le seul refus de confiance — survivait à toute la suite.
//
// Ce fichier monte l'API RÉELLE : vrai `httptest.Server`, vraies routes de `Routes()`, vrai
// middleware d'auth sur un vrai token frappé, vrai store sur la base de dev. Les en-têtes de
// transport (`Content-Length`) sont donc ceux que `net/http` émet réellement, ce qu'un
// `httptest.ResponseRecorder` ne produit pas.
//
// CE QU'IL NE GARDE PAS. La surface MCP est l'affaire de `cmd/flowlio/mcp_refusal_test.go` : ce
// paquet-ci ne connaît pas le client MCP, et l'y faire entrer ferait dépendre une feature d'un
// binaire.

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// refusalBody est la forme EXACTE d'un refus, écrite en dur.
//
// Elle est répétée ici plutôt que dérivée du handler : une constante partagée avec le code sous
// test ferait dire au test « la réponse vaut ce que le handler écrit », c'est-à-dire rien.
const refusalBody = `{"error":"not found"}`

// refusalLength est la longueur annoncée de ce corps, également écrite en dur.
const refusalLength = "21"

// project est un projet de test et son scope de tenancy : exactement ce qu'un token porte.
type project struct {
	teamID uuid.UUID
	id     uuid.UUID
	key    string
}

// core est le CoreServices minimal dont un module a besoin : le service d'auth, et rien d'autre.
type core struct {
	authSvc auth.Service
}

func (c core) Auth() auth.Service { return c.authSvc }

// reply est ce qu'un appelant observe d'une réponse, et la totalité de ce qu'il en observe.
type reply struct {
	status        int
	contentType   string
	contentLength string
	body          []byte
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

// newTeam crée une team jetable. La suppression emporte tout en cascade.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Team de test",
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

// newProject crée un projet dans une team, par SQL direct : la feature issue ne dépend d'aucune
// autre feature, pas même dans ses tests.
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

// trust déclare une confiance entre deux projets. Posée à la main dans le test qui en a besoin :
// la cacher dans newProject masquerait la garantie que ce fichier existe pour prouver.
func trust(t *testing.T, db *sql.DB, a, b project) {
	t.Helper()

	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, low_project_id, high_project_id)
		 VALUES ($1, least($2::uuid, $3::uuid), greatest($2::uuid, $3::uuid))`,
		a.teamID, a.id, b.id,
	); err != nil {
		t.Fatalf("confiance %s ↔ %s: %v", a.key, b.key, err)
	}
}

// serveAPI monte l'API réelle de la feature, vue par le projet appelant.
//
// Le module est construit par NewModule, pas à la main : ce qui est sous test inclut le câblage
// store → service → handler, puisque c'est lui qui décide aujourd'hui de la forme du refus.
func serveAPI(t *testing.T, db *sql.DB, caller project) (*httptest.Server, authtest.Token) {
	t.Helper()

	tok := authtest.Project(t, caller.teamID, caller.id)
	mod := issue.NewModule(module.ModuleConfig{
		DB:    database.New(db),
		RawDB: db,
		Core:  core{authSvc: tok.Auth},
	})

	ts := httptest.NewServer(mod.Routes())
	t.Cleanup(ts.Close)
	return ts, tok
}

// createIssue joue un create_issue sur l'API réelle et rend TOUT ce que l'appelant en observe.
func createIssue(t *testing.T, ts *httptest.Server, tok authtest.Token, toProject string) reply {
	t.Helper()

	payload := `{"to_project":"` + toProject +
		`","title":"le contrat de /v2/cards a-t-il changé ?","body":"le endpoint ne répond plus"}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("construction de la requête vers %s: %v", toProject, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(tok.Authorize(req))
	if err != nil {
		t.Fatalf("appel vers %s: %v", toProject, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lecture du corps pour %s: %v", toProject, err)
	}

	return reply{
		status:        resp.StatusCode,
		contentType:   resp.Header.Get("Content-Type"),
		contentLength: resp.Header.Get("Content-Length"),
		body:          body,
	}
}

// TestThreeRefusalsAreIndistinguishable compare les trois refus octet pour octet.
//
// Deux vérifications, et il faut LES DEUX :
//
//   - chacun contre la forme attendue écrite en dur, sinon une mutation rendant les trois
//     identiquement FAUX (403 partout) satisfait la comparaison croisée ;
//   - les trois entre eux, parce que c'est l'énoncé même de la garantie — et c'est cette
//     comparaison-là que M3 fait tomber, en n'altérant qu'un seul des trois.
//
// Le TÉMOIN qui ouvre le test n'est pas décoratif : sans lui, trois refus identiques pourraient
// l'être parce que rien ne fonctionne du tout, et le test mesurerait sa propre cohérence.
func TestThreeRefusalsAreIndistinguishable(t *testing.T) {
	db := openDB(t)

	team := newTeam(t, db)
	elsewhere := newTeam(t, db)

	frnt := newProject(t, db, team, "FRNT")        // l'appelant
	sibling := newProject(t, db, team, "CORE")     // frère de confiance — le témoin
	untrusted := newProject(t, db, team, "OPS")    // frère, aucune confiance déclarée
	foreign := newProject(t, db, elsewhere, "FAR") // projet d'une autre team

	trust(t, db, frnt, sibling)

	ts, tok := serveAPI(t, db, frnt)

	if got := createIssue(t, ts, tok, sibling.key); got.status != http.StatusCreated {
		t.Fatalf("témoin: création vers un frère de confiance = %d %s, attendu 201 — "+
			"trois refus identiques ne prouvent rien si le chemin nominal est cassé",
			got.status, got.body)
	}

	refusals := []struct {
		name string
		got  reply
	}{
		{"clé inconnue", createIssue(t, ts, tok, "ZZZZ")},
		{"clé d'une autre team", createIssue(t, ts, tok, foreign.key)},
		{"paire non déclarée", createIssue(t, ts, tok, untrusted.key)},
	}

	for _, refusal := range refusals {
		if refusal.got.status != http.StatusNotFound {
			t.Errorf("%s: statut = %d, attendu %d", refusal.name, refusal.got.status, http.StatusNotFound)
		}
		if string(refusal.got.body) != refusalBody {
			t.Errorf("%s: corps = %q, attendu %q", refusal.name, refusal.got.body, refusalBody)
		}
		if refusal.got.contentLength != refusalLength {
			t.Errorf("%s: Content-Length = %q, attendu %q",
				refusal.name, refusal.got.contentLength, refusalLength)
		}
	}

	reference := refusals[0]
	for _, refusal := range refusals[1:] {
		if refusal.got.status != reference.got.status {
			t.Errorf("%s: statut %d ≠ %d de %s — le refus est distinguable",
				refusal.name, refusal.got.status, reference.got.status, reference.name)
		}
		if string(refusal.got.body) != string(reference.got.body) {
			t.Errorf("%s: corps %q ≠ %q de %s — le refus est distinguable",
				refusal.name, refusal.got.body, reference.got.body, reference.name)
		}
		if refusal.got.contentLength != reference.got.contentLength {
			t.Errorf("%s: Content-Length %q ≠ %q de %s — le refus est distinguable par sa taille",
				refusal.name, refusal.got.contentLength, reference.got.contentLength, reference.name)
		}
		if refusal.got.contentType != reference.got.contentType {
			t.Errorf("%s: Content-Type %q ≠ %q de %s — le refus est distinguable par son en-tête",
				refusal.name, refusal.got.contentType, reference.got.contentType, reference.name)
		}
	}
}
