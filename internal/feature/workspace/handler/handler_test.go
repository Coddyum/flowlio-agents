package handler

// Ce que ce fichier verrouille : un token admin qui porte une team ne peut agir QUE sur elle.
//
// Le scénario est monté par le HAUT — vrai middleware d'auth, vrai AdminOnly, vraies routes,
// vrai handler — parce que c'est exactement la chaîne qui donnait le faux sentiment de sécurité :
// `AdminOnly` accepte le principal, les huit tests d'isolation de la feature restent verts, et
// `POST /tokens?team=<voisin>` émet un token de projet chez le voisin, secret en clair.
//
// Seul le STORE d'auth est faux, et c'est obligatoire : depuis la migration 000006, la ligne
// `scope='admin' AND team_id IS NOT NULL` n'est plus insérable en base. Le scénario ne peut donc
// PLUS être monté de bout en bout par SQL — ce qui est le but de la migration. Le faux store est
// ce qui permet de continuer à prouver que le code tient sans la contrainte, et pas grâce à elle.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// fakeAuthStore rend un token unique dont le test choisit la portée — c'est tout son intérêt.
// Le préfixe présenté est vérifié : un double qui ignore ce qu'on lui demande ne prouve rien.
type fakeAuthStore struct {
	prefix string
	rec    auth.TokenRecord
}

func (f *fakeAuthStore) TokenByPrefix(_ context.Context, prefix string) (auth.TokenRecord, error) {
	if prefix != f.prefix {
		return auth.TokenRecord{}, auth.ErrTokenNotFound
	}
	return f.rec, nil
}

func (f *fakeAuthStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// fakeWorkspace enregistre ce que le handler lui demande. C'est là que se joue l'assertion qui
// compte : un refus de teamFor doit couper AVANT que le service ne travaille, sinon le refus
// n'est plus qu'un filtre de sortie et l'effet de bord a déjà eu lieu.
type fakeWorkspace struct {
	teams map[string]service.Team
	calls []string
}

func (f *fakeWorkspace) note(name string) { f.calls = append(f.calls, name) }

func (f *fakeWorkspace) TeamBySlug(_ context.Context, slug string) (service.Team, error) {
	f.note("TeamBySlug")
	team, ok := f.teams[slug]
	if !ok {
		return service.Team{}, service.ErrNotFound
	}
	return team, nil
}

func (f *fakeWorkspace) CreateTeam(context.Context, service.CreateTeamInput) (service.Team, error) {
	f.note("CreateTeam")
	return service.Team{}, nil
}

func (f *fakeWorkspace) ListTeams(context.Context) ([]service.Team, error) {
	f.note("ListTeams")
	return nil, nil
}

func (f *fakeWorkspace) CreateProject(context.Context, service.CreateProjectInput) (service.Project, error) {
	f.note("CreateProject")
	return service.Project{}, nil
}

func (f *fakeWorkspace) ListProjects(context.Context, uuid.UUID) ([]service.Project, error) {
	f.note("ListProjects")
	return nil, nil
}

func (f *fakeWorkspace) Whoami(context.Context, uuid.UUID, uuid.UUID) (service.Identity, error) {
	f.note("Whoami")
	return service.Identity{}, nil
}

func (f *fakeWorkspace) CreateToken(context.Context, service.CreateTokenInput) (service.CreatedToken, error) {
	f.note("CreateToken")
	return service.CreatedToken{}, nil
}

func (f *fakeWorkspace) ListTokens(context.Context, uuid.UUID, string) ([]service.TokenInfo, error) {
	f.note("ListTokens")
	return nil, nil
}

func (f *fakeWorkspace) RevokeToken(context.Context, uuid.UUID, uuid.UUID) error {
	f.note("RevokeToken")
	return nil
}

// adminServer monte les routes admin qui passent par teamFor, avec le vrai middleware d'auth,
// et renvoie le token brut à présenter. teamID est la team que PORTE le token admin :
// uuid.Nil pour l'admin global, celui qui existe réellement aujourd'hui.
func adminServer(t *testing.T, teamID uuid.UUID, svc service.Service) (http.Handler, string) {
	t.Helper()

	tok, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	authSvc := auth.New(&fakeAuthStore{
		prefix: tok.Prefix,
		rec: auth.TokenRecord{
			ID:         uuid.New(),
			Scope:      auth.ScopeAdmin,
			TeamID:     teamID,
			SecretHash: tok.Hash,
			LastUsedAt: time.Now(),
		},
	})

	h := New(authSvc, svc)
	admin := authSvc.AdminOnly

	mux := http.NewServeMux()
	mux.Handle("POST /projects", admin(http.HandlerFunc(h.CreateProject)))
	mux.Handle("GET /projects", admin(http.HandlerFunc(h.ListProjects)))
	mux.Handle("POST /tokens", admin(http.HandlerFunc(h.CreateToken)))
	mux.Handle("GET /tokens", admin(http.HandlerFunc(h.ListTokens)))
	mux.Handle("DELETE /tokens/{id}", admin(http.HandlerFunc(h.RevokeToken)))

	return mux, tok.Plain
}

// teamForRoute décrit une route dont la team est résolue par teamFor. Les cinq y sont : le garde
// vit dans teamFor, donc une route ajoutée demain qui l'appelle est protégée sans y penser — et
// une route qui ne l'appelle PAS doit sauter aux yeux de qui relit cette liste.
type teamForRoute struct {
	name    string
	method  string
	path    string
	body    string
	svcCall string
}

var teamForRoutes = []teamForRoute{
	{"POST /projects", http.MethodPost, "/projects", `{"key":"FRNT","name":"Front"}`, "CreateProject"},
	{"GET /projects", http.MethodGet, "/projects", "", "ListProjects"},
	{"POST /tokens", http.MethodPost, "/tokens", `{"project":"FRNT","name":"agent"}`, "CreateToken"},
	{"GET /tokens", http.MethodGet, "/tokens", "", "ListTokens"},
	{"DELETE /tokens/{id}", http.MethodDelete, "/tokens/" + uuid.NewString(), "", "RevokeToken"},
}

// call joue une requête admin sur une route et rend le statut, le corps, et les appels que le
// service a reçus.
func call(t *testing.T, teamID uuid.UUID, r teamForRoute, teams map[string]service.Team, slug string) (int, string, []string) {
	t.Helper()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, teamID, svc)

	req := httptest.NewRequest(r.method, r.path+"?team="+slug, strings.NewReader(r.body))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec.Code, strings.TrimSpace(rec.Body.String()), svc.calls
}

func contains(calls []string, name string) bool {
	for _, c := range calls {
		if c == name {
			return true
		}
	}
	return false
}

// fixtures monte deux teams : celle du token, et le voisin qu'il ne doit pas atteindre.
func fixtures() (map[string]service.Team, service.Team, service.Team) {
	mine := service.Team{ID: uuid.New(), Slug: "ma-team", Name: "La mienne"}
	other := service.Team{ID: uuid.New(), Slug: "team-du-voisin", Name: "Le voisin"}
	return map[string]service.Team{mine.Slug: mine, other.Slug: other}, mine, other
}

// Un admin qui porte une team est enfermé dedans, sur TOUTES les routes qui résolvent une team.
//
// MUTATION : retirer le garde `if p.TeamID != uuid.Nil && team.ID != p.TeamID` de teamFor fait
// tomber ce test sur les cinq routes — le voisin répond alors 2xx et le service est appelé.
func TestAdminPorteurDUneTeamNeSortPasDeLaSienne(t *testing.T) {
	teams, mine, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, mine.ID, r, teams, other.Slug)

			if code != http.StatusNotFound {
				t.Errorf("?team=%s : code = %d, attendu %d — un admin épinglé à %s a agi sur le voisin",
					other.Slug, code, http.StatusNotFound, mine.Slug)
			}
			if body != `{"error":"not found"}` {
				t.Errorf("corps = %s, attendu {\"error\":\"not found\"}", body)
			}
			if contains(calls, r.svcCall) {
				t.Errorf("le service a reçu %s : le refus arrive APRÈS le travail, donc l'effet de bord a eu lieu (appels: %v)",
					r.svcCall, calls)
			}
		})
	}
}

// Le refus doit être indiscernable d'une team inexistante : « elle existe mais pas pour toi »
// laisserait énumérer les teams de l'installation par balayage de slugs.
//
// MUTATION : répondre auth.ErrForbidden au lieu de service.ErrNotFound dans teamFor fait tomber
// ce test — 403 d'un côté, 404 de l'autre.
func TestLeRefusEstIndiscernableDUneTeamInexistante(t *testing.T) {
	teams, mine, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			refusCode, refusBody, _ := call(t, mine.ID, r, teams, other.Slug)
			inconnuCode, inconnuBody, _ := call(t, mine.ID, r, teams, "team-qui-nexiste-pas")

			if refusCode != inconnuCode {
				t.Errorf("codes distincts : voisin = %d, slug inconnu = %d — le code dit que la team existe",
					refusCode, inconnuCode)
			}
			if refusBody != inconnuBody {
				t.Errorf("corps distincts : voisin = %s, slug inconnu = %s", refusBody, inconnuBody)
			}
		})
	}
}

// Sa propre team reste évidemment accessible. Sans ce cas, un garde qui refuserait TOUT admin
// porteur d'une team passerait pour correct.
func TestAdminPorteurDUneTeamAgitSurLaSienne(t *testing.T) {
	teams, mine, _ := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, mine.ID, r, teams, mine.Slug)

			if code == http.StatusNotFound || code >= http.StatusInternalServerError {
				t.Errorf("code = %d (corps %s) : sa propre team lui est refusée", code, body)
			}
			if !contains(calls, r.svcCall) {
				t.Errorf("le service n'a pas reçu %s (appels: %v)", r.svcCall, calls)
			}
		})
	}
}

// L'admin GLOBAL — celui que l'amorçage crée réellement, sans team — garde sa portée sur toutes
// les teams. C'est le garde-fou dans l'autre sens : le correctif ne doit pas casser le seul
// token admin qui existe aujourd'hui.
func TestAdminGlobalAtteintNImporteQuelleTeam(t *testing.T) {
	teams, _, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, uuid.Nil, r, teams, other.Slug)

			if code == http.StatusNotFound || code >= http.StatusInternalServerError {
				t.Errorf("code = %d (corps %s) : l'admin global ne peut plus administrer", code, body)
			}
			if !contains(calls, r.svcCall) {
				t.Errorf("le service n'a pas reçu %s (appels: %v)", r.svcCall, calls)
			}
		})
	}
}
