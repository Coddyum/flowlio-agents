package handler

// Ce que ce fichier verrouille : les trois routes du graphe de confiance sont ADMIN, et un token
// d'agent y est refusé AVANT d'atteindre le handler.
//
// C'est la garantie qui fonde Q3 de docs/DESIGN-TRUST.md. Un agent a plein pouvoir sur les
// fichiers de son propre repo ; une confiance qu'il pourrait déclarer serait donc auto-signée par
// la partie qu'elle est censée contraindre. Si `admin` devenait `authed` sur l'une des trois
// lignes de Routes(), tout le volet 2 tomberait — et rien d'autre dans la suite ne le verrait.
//
// Les quatre tests de handler_test.go couvrent déjà l'autre moitié (un admin épinglé à une team
// ne sort pas de la sienne, sur les trois routes) : elles sont dans teamForRoutes.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// trustRoutes est le sous-ensemble de teamForRoutes qui édite ou lit le graphe. Il est écrit à
// part, et pas dérivé par filtre : une route de confiance ajoutée demain doit apparaître ici
// explicitement, pas être capturée par un préfixe qu'on aurait oublié de vérifier.
var trustRoutes = []teamForRoute{
	{"GET /trust", http.MethodGet, "/trust", "", "ListTrust"},
	{"POST /trust", http.MethodPost, "/trust", `{"first":"FRNT","second":"CORE"}`, "AllowTrust"},
	{"DELETE /trust/{first}/{second}", http.MethodDelete, "/trust/FRNT/CORE", "", "RevokeTrust"},
}

// Un token de PROJET — celui que porte un agent — est refusé sur les trois routes du graphe.
//
// MUTATION : remplacer `admin` par `authed` sur l'une des trois lignes de Routes() fait tomber ce
// test sur cette route. C'est la seule chose qui empêche un agent de s'autoriser lui-même.
func TestUnTokenDAgentNeTouchePasAuGrapheDeConfiance(t *testing.T) {
	teams, mine, _ := fixtures()

	for _, r := range trustRoutes {
		t.Run(r.name, func(t *testing.T) {
			svc := &fakeWorkspace{teams: teams}
			mux, raw := tokenServer(t, auth.TokenRecord{
				Scope:     auth.ScopeProject,
				TeamID:    mine.ID,
				ProjectID: uuid.New(),
			}, svc)

			req := httptest.NewRequest(r.method, r.path+"?team="+mine.Slug, strings.NewReader(r.body))
			req.Header.Set("Authorization", "Bearer "+raw)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("code = %d, attendu %d — un agent peut éditer le graphe qui le contraint",
					rec.Code, http.StatusForbidden)
			}
			// Le refus vient du MIDDLEWARE, pas du handler : le service ne doit avoir rien vu.
			// Un 403 rendu après un appel au service serait un filtre de sortie, et l'écriture
			// aurait déjà eu lieu.
			if len(svc.calls) != 0 {
				t.Errorf("le service a été appelé (%v) : le refus arrive après le travail", svc.calls)
			}
		})
	}
}

// Un token sans Authorization du tout est refusé aussi, et en 401 — pas en 403.
//
// Sans ce cas, un middleware qui refuserait TOUT le monde passerait pour correct au test
// précédent. C'est le pendant de TestAdminPorteurDUneTeamAgitSurLaSienne.
func TestLeGrapheExigeUneAuthentification(t *testing.T) {
	teams, _, _ := fixtures()

	for _, r := range trustRoutes {
		t.Run(r.name, func(t *testing.T) {
			svc := &fakeWorkspace{teams: teams}
			mux, _ := adminServer(t, uuid.Nil, svc)

			req := httptest.NewRequest(r.method, r.path+"?team=ma-team", strings.NewReader(r.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("code = %d sans en-tête Authorization, attendu %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// La team vient de teamFor et de NULLE PART ailleurs : un `team_id` glissé dans le corps de
// `POST /trust` doit être refusé par le décodeur, pas ignoré en silence.
//
// Le champ porte `json:"-"`, donc DisallowUnknownFields le rejette. Sans ce test, retirer le tag
// transformerait le corps en second résolveur de team — et un admin global pourrait ouvrir une
// arête dans une team qu'il n'a pas nommée dans `?team=`.
func TestLaTeamNeSeGlissePasDansLeCorps(t *testing.T) {
	teams, mine, other := fixtures()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, uuid.Nil, svc)

	body := `{"first":"FRNT","second":"CORE","team_id":"` + other.ID.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/trust?team="+mine.Slug, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d avec un team_id dans le corps, attendu %d", rec.Code, http.StatusBadRequest)
	}
	if contains(svc.calls, "AllowTrust") {
		t.Errorf("le service a reçu AllowTrust (%v) : un corps invalide a atteint la logique", svc.calls)
	}
}

// L'écriture est refusée AVANT le décodage du corps quand la team ne se résout pas.
//
// L'ordre compte : décoder d'abord donnerait à un admin épinglé un moyen de distinguer « corps
// invalide » de « team interdite », donc un oracle sur l'existence des teams voisines.
func TestUnCorpsValideNeSauvePasUneTeamInterdite(t *testing.T) {
	teams, mine, other := fixtures()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, mine.ID, svc)

	req := httptest.NewRequest(http.MethodPost, "/trust?team="+other.Slug,
		strings.NewReader(`{"first":"FRNT","second":"CORE"}`))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
		t.Errorf("corps = %s, attendu {\"error\":\"not found\"}", rec.Body.String())
	}
	if contains(svc.calls, "AllowTrust") {
		t.Errorf("le service a reçu AllowTrust (%v)", svc.calls)
	}
}

// Garde-fou de typage : le fake doit rester un service.Service complet. Si une méthode est ajoutée
// au contrat sans être ajoutée au fake, c'est ici que ça se voit, avec un message lisible.
var _ service.Service = (*fakeWorkspace)(nil)
