package overview

// GARANTIES 1 ET 20 DU TABLEAU DE docs/DESIGN-TUI.md § « Garanties de sécurité ».
//
// Ce que ce fichier verrouille : la TABLE DE ROUTES elle-même — que les deux routes soient
// derrière `AdminOnly`, et qu'aucune d'elles n'accepte autre chose qu'un GET.
//
// IL PART DE Routes() ET DE RIEN D'AUTRE. Un test de handler qui recâblerait les routes à la main
// prouverait la portée de sa propre table, pas celle du module — la leçon a déjà été payée dans
// `workspace/module_test.go`, où une route passée de `admin` à `authed` laissait toute la suite
// verte.
//
// `mod` est construit directement plutôt que par NewModule : NewModule câblerait le vrai service
// sur un store à `*database.Queries` nul, et la première route qui atteint le handler
// déréférencerait nil. Ce qui est sous test ici est la table, pas le câblage des dépendances.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/google/uuid"
)

// stubService rend une réponse vide sur les trois méthodes du contrat.
//
// L'interface embarquée est nulle : toute méthode ajoutée demain et non redéfinie ici panique si
// une route l'appelle. C'est délibéré — une route qui laisserait passer un principal qu'elle
// devrait refuser ne rendrait pas un statut inattendu, elle exploserait, ce qui est plus dur à
// ignorer qu'un code de statut.
type stubService struct {
	service.Service
}

func (stubService) TeamBySlug(context.Context, string) (service.Team, error) {
	return service.Team{ID: uuid.New(), Slug: "acme", Name: "Acme"}, nil
}

func (stubService) TeamState(context.Context, uuid.UUID) (service.TeamState, error) {
	return service.TeamState{}, nil
}

func (stubService) RefDetail(context.Context, uuid.UUID, string, int64) (service.RefDetail, error) {
	return service.RefDetail{}, nil
}

// route décrit une entrée de la table de routes. Les deux sont admin : il n'y a pas de gate mixte
// sur cette surface, et ce fichier existe en partie pour qu'il n'y en ait jamais.
type route struct {
	method string
	path   string
}

// routes énumère les DEUX routes de Routes(), à la main.
//
// Écrite à la main et non dérivée du mux : `http.ServeMux` n'expose pas ses patterns, et dériver
// la liste de l'objet sous test lui ferait dire ce que le code fait plutôt que ce qu'il doit
// faire. Le compte est vérifié contre la source de module.go, plus bas.
var routes = []route{
	{http.MethodGet, "/?team=acme"},
	{http.MethodGet, "/refs/CORE/41?team=acme"},
}

// serve joue une requête sur les VRAIES routes, avec le token fourni.
func serve(t *testing.T, tok authtest.Token, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	m := &mod{h: handler.New(stubService{}), auth: tok.Auth}

	req := tok.Authorize(httptest.NewRequest(method, path, nil))
	rec := httptest.NewRecorder()
	m.Routes().ServeHTTP(rec, req)
	return rec
}

// GARANTIE 1 — un token de projet n'atteint AUCUNE route d'overview.
//
// C'est la garantie qui justifie l'existence d'un module séparé. Sous `auth.Middleware`, l'agent
// DOCS lirait le fil FRNT↔CORE, et les huit tests d'isolation de `task` et `issue` resteraient
// verts : ils prouvent que LEURS queries sont scopées, pas qu'aucune autre route ne contourne ce
// scope.
//
// MUTATION : dans module.go, remplacer `admin := m.auth.AdminOnly` par `m.auth.Middleware`.
//
// LE CODE DE STATUT SEUL NE TUE PAS CETTE MUTATION, et c'est la première chose qu'a montrée le
// test en la jouant : `handler.principal` refuse AUSSI un principal non admin, donc sous
// `Middleware` la requête rendait encore 403 — par la seconde défense, une couche plus loin. Un
// test qui n'aurait regardé que le code aurait été VERT sur la mutation qu'il existe pour tuer.
//
// D'où l'assertion sur le corps EXACT : `auth.deny` écrit `Forbidden` (StatusText) et pose
// `WWW-Authenticate`, le handler écrit `forbidden` et ne pose rien. C'est ce qui distingue « le
// middleware a refusé » de « le middleware a laissé passer et le handler a rattrapé ». La seconde
// défense reste en place — elle est ce qui protège le jour où quelqu'un monte une de ces routes
// sous `Middleware` — mais elle ne masque plus la régression.
func TestOverviewRefusesProjectToken(t *testing.T) {
	const refusDuMiddleware = `{"error":"Forbidden"}`

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Project(t, uuid.New(), uuid.New())

			rec := serve(t, tok, r.method, r.path)

			if rec.Code != http.StatusForbidden {
				t.Errorf("code = %d avec un token de projet, attendu %d — cette route est "+
					"ouverte aux agents", rec.Code, http.StatusForbidden)
			}
			if got := rec.Body.String(); got != refusDuMiddleware {
				t.Errorf("corps = %s, attendu %s — le refus ne vient plus d'AdminOnly mais "+
					"d'une couche plus profonde : la route a changé de middleware",
					got, refusDuMiddleware)
			}
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("WWW-Authenticate absent — ce refus n'est pas celui du middleware d'auth")
			}
		})
	}
}

// CONTRE-ÉPREUVE de la garantie 1 : un token admin passe.
//
// Sans elle, un middleware qui refuserait TOUT LE MONDE — ou une route qui n'existerait pas —
// ferait passer le test précédent pour correct.
func TestOverviewAcceptsAdminToken(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Admin(t, uuid.Nil)

			rec := serve(t, tok, r.method, r.path)

			if rec.Code != http.StatusOK {
				t.Errorf("code = %d avec un token admin, attendu %d — la route ne répond plus",
					rec.Code, http.StatusOK)
			}
		})
	}
}

// Sans token du tout, les deux routes rendent 401 et non 403 : « je ne sais pas qui tu es » et
// « je sais qui tu es et ce n'est pas assez » sont deux réponses différentes, et c'est le
// middleware qui les distingue.
func TestOverviewRefusesAnonymous(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Admin(t, uuid.Nil)
			m := &mod{h: handler.New(stubService{}), auth: tok.Auth}

			rec := httptest.NewRecorder()
			m.Routes().ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("code = %d sans token, attendu %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// GARANTIE 20 — aucune écriture par cette surface.
//
// Le second volet de la garantie est dans `make lint` : check-overview-scope.sh refuse tout
// INSERT/UPDATE/DELETE dans sql/queries/overview.sql. Celui-ci ferme l'autre bout : même une
// query d'écriture ajoutée par erreur n'aurait aucune route pour l'atteindre.
//
// MUTATION : monter une route d'écriture dans Routes().
func TestOverviewExposesOnlyGET(t *testing.T) {
	écritures := []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}

	for _, r := range routes {
		for _, method := range écritures {
			t.Run(method+" "+r.path, func(t *testing.T) {
				tok := authtest.Admin(t, uuid.Nil)

				rec := serve(t, tok, method, r.path)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("code = %d pour %s, attendu %d — une route d'écriture existe sur "+
						"cette surface", rec.Code, method, http.StatusMethodNotAllowed)
				}
			})
		}
	}
}

// La table du test couvre TOUTES les routes de Routes(), et le compte vient de la SOURCE de
// module.go.
//
// Comparer `len(routes)` à une constante écrite dans le même fichier ne garderait rien : une
// troisième route ajoutée à Routes() laisserait la suite verte. ServeMux n'expose pas ses
// patterns, donc compter les `r.Handle(` du fichier est le seul lien mécanique possible entre la
// table du test et la table réelle.
//
// MUTATION : ajouter une route à Routes() sans l'ajouter à `routes` → ce test rouge.
func TestLaTableDeRoutesEstComplete(t *testing.T) {
	source, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatalf("lecture de module.go: %v", err)
	}
	declarees := strings.Count(string(source), "r.Handle(")

	if declarees == 0 {
		t.Fatal("aucun r.Handle( trouvé dans module.go — le compteur ne mesure plus rien")
	}
	if len(routes) != declarees {
		t.Errorf("Routes() déclare %d routes, la table du test en porte %d — une route a été "+
			"ajoutée ou retirée sans que ce fichier suive", declarees, len(routes))
	}

	for _, r := range routes {
		rec := serve(t, authtest.Admin(t, uuid.Nil), r.method, r.path)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s : 404 — ce chemin n'existe plus dans Routes()", r.method, r.path)
		}
	}
}
