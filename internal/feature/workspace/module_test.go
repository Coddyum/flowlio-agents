package workspace

// Ce que ce fichier verrouille : la TABLE DE ROUTES elle-même — quelle route est derrière
// `admin`, quelle route est derrière `authed`.
//
// POURQUOI IL EXISTE. Les tests de `handler/` montent leur propre `http.ServeMux` et y recâblent
// les routes à la main. C'est le bon choix pour ce qu'ils prouvent (teamFor, AdminOnly, le
// handler), mais ça duplique la table — et une table dupliquée ne prouve rien de l'originale.
// Mutation jouée : remplacer `admin` par `authed` sur `POST /trust` dans Routes() laisse TOUTE la
// suite verte, y compris le test qui existe pour interdire exactement ça.
//
// Ce test-ci part de `Routes()` et de rien d'autre. Il n'y a plus de seconde table.
//
// Il construit `mod` directement plutôt que par `NewModule` : NewModule câblerait le VRAI service
// sur un store à `*database.Queries` nul, et la première route `authed` qui atteint le handler
// déréférencerait nil. Ce qui est sous test ici est la table, pas le câblage des dépendances.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// stubService n'implémente QUE ce que les routes `authed` appellent.
//
// L'interface embarquée est nulle : toute méthode non redéfinie panique si elle est appelée.
// C'est délibéré — si une route qu'on croit fermée laissait passer un token de projet, le test ne
// rendrait pas un statut inattendu, il exploserait, ce qui est plus dur à ignorer.
type stubService struct {
	service.Service
}

func (stubService) ListProjects(context.Context, uuid.UUID) ([]service.Project, error) {
	return nil, nil
}

func (stubService) Whoami(context.Context, uuid.UUID, uuid.UUID) (service.Identity, error) {
	return service.Identity{}, nil
}

// route décrit une entrée de la table, et la portée qu'elle DOIT exiger.
type route struct {
	method string
	path   string
	body   string
	// adminOnly dit ce que la route promet. C'est la seule donnée du fichier ; tout le reste
	// est mécanique.
	adminOnly bool
}

// routes énumère les ONZE routes de Routes(), à la main.
//
// Écrite à la main et pas dérivée du mux : `http.ServeMux` n'expose pas ses patterns, et même s'il
// le faisait, dériver la liste de l'objet sous test ferait dire au test ce que le code fait plutôt
// que ce qu'il doit faire. Une route ajoutée sans être ajoutée ici échappe au test — c'est le prix,
// et c'est pour ça que le compte est vérifié plus bas.
var routes = []route{
	{http.MethodPost, "/teams", `{"slug":"t","name":"T"}`, true},
	{http.MethodGet, "/teams", "", true},
	{http.MethodPost, "/projects", `{"key":"FRNT","name":"F"}`, true},
	{http.MethodGet, "/projects", "", false},
	{http.MethodPost, "/tokens", `{"project":"FRNT","name":"a"}`, true},
	{http.MethodGet, "/tokens", "", true},
	{http.MethodDelete, "/tokens/" + uuid.NewString(), "", true},
	{http.MethodGet, "/trust", "", true},
	{http.MethodPost, "/trust", `{"first":"FRNT","second":"CORE"}`, true},
	{http.MethodDelete, "/trust/FRNT/CORE", "", true},
	{http.MethodGet, "/whoami", "", false},
}

// serveWithProjectToken joue une requête sur les VRAIES routes, avec un token de PROJET.
func serveWithProjectToken(t *testing.T, r route) *httptest.ResponseRecorder {
	t.Helper()

	tok := authtest.Project(t, uuid.New(), uuid.New())
	m := &mod{h: handler.New(tok.Auth, stubService{}), auth: tok.Auth}

	req := tok.Authorize(httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
	rec := httptest.NewRecorder()
	m.Routes().ServeHTTP(rec, req)
	return rec
}

// Chaque route exige exactement la portée que la table promet.
//
// Les trois routes du graphe de confiance sont les plus sensibles : un agent a plein pouvoir sur
// les fichiers de son propre repo, donc une confiance qu'il déclarerait serait auto-signée par la
// partie qu'elle contraint. `admin` y est la seule chose qui tienne le volet 2 debout.
//
// MUTATION : passer une route `admin` en `authed` dans Routes() fait tomber ce test sur elle, et
// l'inverse aussi.
func TestChaqueRouteExigeLaPorteeQuElleAnnonce(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := serveWithProjectToken(t, r)

			if r.adminOnly {
				if rec.Code != http.StatusForbidden {
					t.Errorf("code = %d avec un token de projet, attendu %d — cette route est ouverte aux agents",
						rec.Code, http.StatusForbidden)
				}
				return
			}

			// Contre-épreuve : sans elle, un middleware qui refuserait TOUT LE MONDE ferait
			// passer la moitié admin du test pour correcte.
			if rec.Code == http.StatusForbidden {
				t.Errorf("code = %d, cette route doit rester ouverte à un token de projet", rec.Code)
			}
		})
	}
}

// La table du test couvre TOUTES les routes de Routes(), et le compte vient de Routes() lui-même.
//
// LA PREMIÈRE VERSION DE CE TEST NE GARDAIT RIEN. Elle comparait `len(routes)` à une constante
// écrite dix lignes plus haut, dans le MÊME fichier : ajouter une douzième route à Routes(), sous
// `authed`, laissait toute la suite verte. Un test qui compare une table à sa propre constante
// mesure sa cohérence interne, pas celle du code — c'est la troisième fois de cette session que ce
// motif exact apparaît.
//
// Le compte est désormais LU DANS LA SOURCE de module.go. C'est laid, et c'est le prix : ServeMux
// n'expose pas ses patterns, donc il n'existe aucun moyen de lui demander ce qu'il porte. Compter
// les `r.Handle(` d'un fichier est le seul lien mécanique possible entre la table du test et la
// table réelle.
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
		rec := serveWithProjectToken(t, r)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s : 404 — ce chemin n'existe plus dans Routes()", r.method, r.path)
		}
	}
}
