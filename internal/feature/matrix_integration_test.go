package feature_test

// GARANTIE 2 DU TABLEAU DE docs/DESIGN-TUI.md — la matrice de portée, tous modules confondus.
//
// CE QUE CE FICHIER GARDE ET QUE PERSONNE D'AUTRE NE GARDE. Chaque module vérifie SA portée dans
// son propre `module_test.go`. Aucun de ces fichiers ne peut voir qu'une portée a été alignée sur
// celle du voisin par copier-coller : un `overview` passé sous `Middleware` reste vert chez
// `task`, et réciproquement. La matrice est la seule assertion qui oppose toutes les surfaces.
//
// POURQUOI C'EST UN TEST D'INTÉGRATION. Les cases 401 et 403 n'atteignent jamais un store — elles
// pourraient tenir sur des doubles. Les cases 200, non : elles traversent le handler, le service
// et la query jusqu'à Postgres. Sans elles la matrice ne prouve rien, parce qu'un middleware qui
// refuserait TOUT LE MONDE la passerait encore à moitié. La colonne 200 est le contrôle positif
// des deux autres, et c'est elle qui impose `FLOWLIO_TEST_DATABASE_URL`.
//
// LES MODULES SONT MONTÉS PAR NewModule, PAS À LA MAIN. Recâbler les routes ici prouverait la
// portée de la table du test. Ce qui est sous test est ce que `buildModules()` construit.
//
// LE STATUT SEUL NE SUFFIT PAS, ET DEUX VERSIONS DE CE FICHIER L'ONT APPRIS À LEURS DÉPENS.
// Chaque module refuse DEUX fois : sa route porte une garde, et son handler la reprend. Retirer
// la garde de route laisse donc le statut inchangé — la seconde couche rattrape — et une matrice
// qui n'observe que le statut reste verte sur exactement la régression qu'elle existe pour
// attraper.
//
//	version 1 (statut seul)      : survivait à `overview` passé de AdminOnly à Middleware
//	version 2 (+ couche d'auth)  : survivait encore à `requireProjectScope` acceptant l'admin
//	version 3 (+ garde de handler) : les deux mutations tombent
//
// Chaque case porte donc DEUX observations en plus du statut :
//
//  1. la couche qui doit refuser, lue sur `WWW-Authenticate: Bearer` — seul `deny()` de
//     `internal/core/auth` le pose ;
//  2. le silence des gardes de handler, lu sur le journal — voir `handlerGuards`. Dans tous les
//     cas corrects, un principal refusé l'est AVANT d'entrer dans le module.
//
// La seconde est la seule qui distingue `requireProjectScope` de `Handler.scope` : leurs réponses
// HTTP sont identiques, corps compris.

import (
	"bytes"
	"database/sql"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	coreregistry "github.com/Coddyum/flowlio-agents/internal/core"
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref"
	"github.com/Coddyum/flowlio-agents/internal/feature/task"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// core est le CoreServices minimal que les modules attendent : le service d'auth du token que le
// cas en cours présente, et rien d'autre.
type core struct{ svc auth.Service }

func (c core) Auth() auth.Service { return c.svc }

// expect est le résultat attendu d'une case : le statut, et la couche qui doit le prononcer.
//
// byAuth vaut vrai quand le refus doit venir de `internal/core/auth` — `Middleware` ou
// `AdminOnly`. Il se lit sur l'en-tête `WWW-Authenticate: Bearer`, que `deny()` est seul à poser.
// Sans ce champ, une case couverte par une seconde ligne de défense reste verte alors que sa
// route a perdu sa garde.
type expect struct {
	status int
	byAuth bool
}

var (
	// allowed — la requête atteint le store et rend son résultat.
	allowed = expect{status: http.StatusOK}
	// deniedByAuth — refus prononcé par la couche d'auth, avant d'entrer dans le module.
	deniedByAuth = expect{status: http.StatusForbidden, byAuth: true}
	// deniedByModule — refus prononcé par `requireProjectScope`, dans le module.
	deniedByModule = expect{status: http.StatusForbidden}
	// deniedWithoutToken — aucun en-tête présenté : la couche d'auth refuse en 401.
	deniedWithoutToken = expect{status: http.StatusUnauthorized, byAuth: true}
)

// surface est une case de la matrice : une route représentative d'un module, et le résultat
// attendu pour chacun des deux principaux authentifiés. Un principal absent attend 401 partout,
// ce qui n'a pas besoin d'une colonne.
//
// La table est écrite À LA MAIN. La dériver des mux lui ferait dire ce que le code fait plutôt que
// ce qu'il doit faire, et c'est exactement l'erreur que la matrice existe pour attraper.
type surface struct {
	feature string
	path    string // {team} est remplacé par le slug de la team de la fixture
	project expect
	admin   expect
}

// matrix énumère les sept surfaces représentatives des six modules.
//
// `workspace` en porte DEUX parce que sa portée est mixte, et « partiel » n'est pas un statut :
// `/projects` est ouvert à tout token authentifié, `/teams` est réservé à l'admin. Une seule
// entrée aurait laissé la moitié de ce module hors matrice.
var matrix = []surface{
	{workspace.Key, "/projects?team={team}", allowed, allowed},
	{workspace.Key, "/teams", deniedByAuth, allowed},
	{task.Key, "/", allowed, deniedByModule},
	{issue.Key, "/", allowed, deniedByModule},
	{inbox.Key, "/", allowed, deniedByModule},
	{overview.Key, "/?team={team}", deniedByAuth, allowed},
	// `ref` cite une référence CONCRÈTE, et la fixture pose la tâche CORE-1 pour elle. Une
	// référence inexistante rendrait 404 : la case perdrait son contrôle positif, et une portée
	// qui refuserait tout le monde la passerait encore.
	{ref.Key, "/CORE/1", allowed, deniedByModule},
}

// fixture porte la team et le projet auxquels les tokens du test sont épinglés.
type fixture struct {
	teamID    uuid.UUID
	slug      string
	projectID uuid.UUID
}

// newFixture ouvre la base de test et y crée une team jetable et son projet.
//
// La suppression de la team emporte le projet en cascade — le nettoyage n'a donc qu'une ligne, et
// aucun test ne laisse derrière lui de quoi faire passer le suivant.
func newFixture(t *testing.T) (*sql.DB, fixture) {
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

	f := fixture{slug: "matrix-" + strings.ToLower(uuid.NewString()[:8])}
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", f.slug, "Team de matrice",
	).Scan(&f.teamID); err != nil {
		t.Fatalf("création de la team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", f.teamID); err != nil {
			t.Errorf("nettoyage de la team %s: %v", f.teamID, err)
		}
	})

	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		f.teamID, "CORE", "Projet CORE",
	).Scan(&f.projectID); err != nil {
		t.Fatalf("création du projet: %v", err)
	}

	// Une tâche pour la case `ref` : elle résout une référence, donc il lui en faut une qui existe
	// pour que son 200 prouve quelque chose. Les autres cases lisent des listes et ne la voient pas
	// autrement que comme une ligne de plus.
	if _, err := db.Exec(
		"INSERT INTO tasks (team_id, project_id, number, title) VALUES ($1, $2, 1, $3)",
		f.teamID, f.projectID, "tâche de matrice",
	); err != nil {
		t.Fatalf("création de la tâche CORE-1: %v", err)
	}

	return db, f
}

// routers monte les six modules avec leurs vrais stores, sur le service d'auth du token fourni.
//
// Le double `authtest.Store` ne connaît qu'un token : chaque principal a donc son propre jeu de
// modules. C'est plus lent qu'un montage unique, et c'est ce qui garantit qu'un cas ne peut pas
// présenter le token d'un autre.
func routers(t *testing.T, db *sql.DB, tok authtest.Token) map[string]http.Handler {
	t.Helper()

	registry := coreregistry.NewRegistry()
	cfg := module.ModuleConfig{
		DB:       database.New(db),
		RawDB:    db,
		Core:     core{svc: tok.Auth},
		Registry: registry,
	}
	mounted := map[string]http.Handler{}
	for _, m := range []module.Module{
		workspace.NewModule(cfg),
		task.NewModule(cfg),
		issue.NewModule(cfg),
		inbox.NewModule(cfg),
		overview.NewModule(cfg),
		ref.NewModule(cfg),
	} {
		// Le registre est rempli COMME EN PRODUCTION : `ref` compose task et issue par lui, et un
		// montage qui l'oublierait ferait répondre 500 à sa case au lieu de prouver sa portée.
		registry.Register(m.Key(), m)
		mounted[m.Key()] = m.Routes()
	}
	return mounted
}

// call joue un GET sur la route d'une case et rend la réponse entière.
//
// Le recorder est rendu plutôt que le seul statut : la couche qui a refusé se lit dans les
// en-têtes, et c'est la moitié de ce que chaque case affirme.
//
// authorize est nil pour le principal absent : c'est la seule différence entre le cas 401 et les
// deux autres, et elle est portée par la requête, pas par un montage particulier.
func call(t *testing.T, mounted map[string]http.Handler, s surface, slug string, authorize func(*http.Request) *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()

	router, ok := mounted[s.feature]
	if !ok {
		t.Fatalf("aucun module monté sous la clé %q — la matrice cite une feature qui n'existe plus", s.feature)
	}

	req := httptest.NewRequest(http.MethodGet, strings.ReplaceAll(s.path, "{team}", slug), nil)
	if authorize != nil {
		req = authorize(req)
	}

	// Le journal est détourné le temps de l'appel : les gardes de handler y écrivent, et c'est
	// le seul endroit où elles se distinguent d'un refus prononcé plus tôt — leurs réponses
	// HTTP sont identiques, corps compris. Aucun test de ce fichier n'est parallèle, donc le
	// détournement d'une variable de paquet ne peut pas déborder sur un autre cas.
	var journal bytes.Buffer
	log.SetOutput(&journal)
	defer log.SetOutput(os.Stderr)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, journal.String()
}

// handlerGuards énumère les phrases que les gardes de handler écrivent quand elles refusent un
// principal.
//
// AUCUNE CASE DE LA MATRICE NE DOIT LES FAIRE PARLER. Dans tous les cas corrects, un principal
// refusé l'est avant d'entrer dans le module ; une garde qui parle prouve qu'il est allé plus
// loin qu'il n'aurait dû, et que le 403 obtenu l'est pour la mauvaise raison.
//
// Ce sont les seules lignes de garde — les autres `log.Printf` des handlers rapportent des échecs
// d'encodage ou d'écriture, qui ne disent rien d'une portée.
var handlerGuards = []string{
	"route sans token de projet",
	"principal non admin",
	"route sans middleware d'auth",
}

// assertCase compare la réponse obtenue au résultat attendu : le statut, puis la couche qui l'a
// prononcé.
//
// Les deux assertions sont séparées et toutes deux rapportées. Un refus qui glisse d'une couche à
// l'autre garde son statut : c'est exactement la régression que la seconde ligne attrape, et elle
// serait invisible si le test s'arrêtait au premier écart.
func assertCase(t *testing.T, rec *httptest.ResponseRecorder, journal string, want expect, s surface, principal string) {
	t.Helper()

	// D'abord la garde de handler, avant même le statut : quand elle parle, le statut est bon et
	// la raison ne l'est pas, et c'est le diagnostic le plus utile à rapporter.
	for _, guard := range handlerGuards {
		if strings.Contains(journal, guard) {
			t.Errorf("la garde du handler a parlé (%q) pour %s sur %s%s — le principal a ATTEINT le handler, "+
				"alors qu'une couche antérieure aurait dû l'arrêter. Statut %d obtenu pour la mauvaise raison.",
				guard, principal, s.feature, s.path, rec.Code)
			return
		}
	}

	if rec.Code != want.status {
		t.Errorf("statut = %d, attendu %d pour %s sur %s%s",
			rec.Code, want.status, principal, s.feature, s.path)
		return
	}
	if want.status == http.StatusOK {
		return
	}

	// `deny()` d'internal/core/auth est le seul à poser cet en-tête. Son absence sur un refus
	// attendu de la couche d'auth signifie que la route a perdu sa garde et qu'une défense plus
	// profonde a rattrapé le coup.
	byAuth := rec.Header().Get("WWW-Authenticate") == "Bearer"
	if byAuth == want.byAuth {
		return
	}

	refuser := map[bool]string{true: "la couche d'auth (Middleware/AdminOnly)", false: "le module lui-même"}
	t.Errorf("refus prononcé par %s, attendu de %s pour %s sur %s%s — le statut %d est bon, la couche ne l'est pas",
		refuser[byAuth], refuser[want.byAuth], principal, s.feature, s.path, rec.Code)
}

// TestScopeMatrixProjectToken — un token d'agent lit son backlog et sa file, ne voit aucune
// surface d'administration, et n'atteint pas overview.
//
// MUTATION JOUÉE : dans overview/module.go, `admin := m.auth.AdminOnly` remplacé par
// `m.auth.Middleware` → la case overview reste à 403, mais prononcé par la garde du handler.
// Rouge sur « la garde du handler a parlé ("principal non admin") ».
//
// MUTATION JOUÉE : dans workspace/module.go, `authed(...)` retiré de `GET /projects` → la case
// tombe à 401, rouge sur le statut.
func TestScopeMatrixProjectToken(t *testing.T) {
	db, f := newFixture(t)
	tok := authtest.Project(t, f.teamID, f.projectID)
	mounted := routers(t, db, tok)

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, tok.Authorize)
			assertCase(t, rec, journal, s.project, s, "un token de projet")
		})
	}
}

// TestScopeMatrixAdminToken — un token admin administre et lit l'ensemble de la team, mais
// n'écrit ni ne lit à la place d'un agent.
//
// Un admin refusé sur `task`, `issue` et `inbox` n'est pas une limitation à corriger : ces
// surfaces répondent AU NOM d'un projet, et un principal sans projet n'en a aucun à donner.
//
// MUTATION JOUÉE : dans task/module.go, `principal.Scope != auth.ScopeProject` remplacé par
// `principal.Scope != auth.ScopeProject && !principal.IsAdmin()` → la case task reste à 403,
// prononcé par `Handler.scope` au lieu de `requireProjectScope`. Les deux réponses sont
// identiques au bit près : rouge sur « la garde du handler a parlé ("route sans token de
// projet") », et sur rien d'autre. C'est la mutation qui a coûté deux versions de ce fichier.
//
// MUTATION JOUÉE : dans inbox/module.go, `Middleware(requireProjectScope(...))` remplacé par
// `AdminOnly(...)` — l'alignement par copier-coller sur le module voisin, le défaut même que
// cette matrice existe pour attraper → la case inbox tombe à 403 pour un token de projet, rouge
// sur le statut dans TestScopeMatrixProjectToken.
func TestScopeMatrixAdminToken(t *testing.T) {
	db, f := newFixture(t)
	tok := authtest.Admin(t, uuid.Nil)
	mounted := routers(t, db, tok)

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, tok.Authorize)
			assertCase(t, rec, journal, s.admin, s, "un token admin")
		})
	}
}

// TestScopeMatrixWithoutToken — aucune surface du produit n'est lisible sans token.
//
// C'est la ligne qui doit rester vraie quand une route est ajoutée : un handler monté hors du
// middleware d'auth ne se voit dans aucun `module_test.go` de portée, puisque ceux-ci partent des
// principaux qu'ils présentent.
//
// MUTATION JOUÉE : dans workspace/module.go, `authed(...)` retiré de la route `GET /projects` →
// la requête sans en-tête atteint le handler, dont la garde refuse en 401. Le statut attendu est
// pourtant 401 : seule la garde de handler trahit la régression, et ce test la lit.
func TestScopeMatrixWithoutToken(t *testing.T) {
	db, f := newFixture(t)
	mounted := routers(t, db, authtest.Admin(t, uuid.Nil))

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, nil)
			assertCase(t, rec, journal, deniedWithoutToken, s, "aucun en-tête Authorization")
		})
	}
}
