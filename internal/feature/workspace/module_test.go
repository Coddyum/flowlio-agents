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
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// stubAuthStore rend un unique token dont le test choisit la portée.
type stubAuthStore struct {
	prefix string
	rec    auth.TokenRecord
}

func (s *stubAuthStore) TokenByPrefix(_ context.Context, prefix string) (auth.TokenRecord, error) {
	if prefix != s.prefix {
		return auth.TokenRecord{}, auth.ErrTokenNotFound
	}
	return s.rec, nil
}

func (s *stubAuthStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

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

	tok, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	authSvc := auth.New(&stubAuthStore{
		prefix: tok.Prefix,
		rec: auth.TokenRecord{
			ID:         uuid.New(),
			Scope:      auth.ScopeProject,
			TeamID:     uuid.New(),
			ProjectID:  uuid.New(),
			SecretHash: tok.Hash,
			LastUsedAt: time.Now(),
		},
	})

	m := &mod{h: handler.New(authSvc, stubService{}), auth: authSvc}

	req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	req.Header.Set("Authorization", "Bearer "+tok.Plain)
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

// Le compte de routes est figé.
//
// C'est le seul garde-fou contre l'angle mort de la table écrite à la main : une route ajoutée à
// Routes() sans l'être ici passerait inaperçue. Ce test ne dit pas laquelle manque — il dit qu'il
// faut aller voir, ce qui suffit.
//
// Le compteur est obtenu en jouant chaque chemin de la table contre le mux : un 404 signale un
// chemin qui n'existe plus, et le total est comparé à ce que Routes() déclare.
func TestLaTableDeRoutesEstComplete(t *testing.T) {
	const declarees = 11

	if len(routes) != declarees {
		t.Fatalf("la table du test porte %d routes, Routes() en déclare %d — mettre `routes` à jour",
			len(routes), declarees)
	}

	for _, r := range routes {
		rec := serveWithProjectToken(t, r)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s : 404 — ce chemin n'existe plus dans Routes()", r.method, r.path)
		}
	}
}
