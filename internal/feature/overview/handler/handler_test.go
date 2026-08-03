package handler_test

// GARANTIES 3 ET 4 DU TABLEAU DE docs/DESIGN-TUI.md § « Garanties de sécurité ».
//
// Ce que ce fichier verrouille : D'OÙ VIENT LA TEAM. Elle vient de la résolution serveur du slug
// `?team=`, et de nulle part ailleurs — ni d'un paramètre d'URL, ni du principal.
//
// ÉCART ASSUMÉ AVEC LA NOTE : la garantie 4 y est classée `I`, avec une insertion SQL brute d'un
// token admin porteur d'une team. Elle est écrite ici en `U`. La forme sous test — un principal
// admin dont TeamID n'est pas nul — est exactement celle qu'`authtest.Admin(t, teamID)` présente,
// et le garde qu'elle exerce est du code Go, pas une contrainte de base. Un aller-retour en
// Postgres n'aurait rien prouvé de plus, et aurait fait dépendre de `make test-integration` une
// garantie qui peut vivre dans `make check`.
//
// Le service est un espion : ce qu'on veut observer n'est pas la réponse, c'est l'ARGUMENT que le
// handler lui passe.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/google/uuid"
)

// spyService enregistre ce que le handler lui passe, et rend ce que le test lui dit de rendre.
//
// L'interface embarquée est nulle : une méthode appelée sans être redéfinie panique, ce qui est
// plus dur à ignorer qu'un zéro rendu silencieusement.
type spyService struct {
	service.Service

	// resolved est l'identité que TeamBySlug rendra, quel que soit le slug.
	resolved service.Team

	// gotSlug et gotTeamID sont ce que le handler a réellement passé.
	gotSlug      string
	gotTeamID    uuid.UUID
	stateCalled  bool
	gotProjetKey string
	gotNumber    int64
}

func (s *spyService) TeamBySlug(_ context.Context, slug string) (service.Team, error) {
	s.gotSlug = slug
	return s.resolved, nil
}

func (s *spyService) TeamState(_ context.Context, teamID uuid.UUID) (service.TeamState, error) {
	s.gotTeamID = teamID
	s.stateCalled = true
	return service.TeamState{}, nil
}

func (s *spyService) RefDetail(_ context.Context, teamID uuid.UUID, key string, number int64) (service.RefDetail, error) {
	s.gotTeamID = teamID
	s.gotProjetKey = key
	s.gotNumber = number
	return service.RefDetail{}, nil
}

// serve monte les deux routes derrière le VRAI middleware AdminOnly et joue une requête.
//
// La table est recâblée ici, ce qui ne prouve rien de celle de `module.go` — c'est
// `overview/module_test.go` qui la garde. Ce fichier-ci teste ce qui se passe UNE FOIS la route
// atteinte.
func serve(t *testing.T, tok authtest.Token, spy *spyService, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	h := handler.New(spy)
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", tok.Auth.AdminOnly(http.HandlerFunc(h.TeamState)))
	mux.Handle("GET /refs/{project}/{number}", tok.Auth.AdminOnly(http.HandlerFunc(h.RefDetail)))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, tok.Authorize(httptest.NewRequest(method, path, nil)))
	return rec
}

// GARANTIE 3 — la team passée au service est celle que le SLUG a résolue.
//
// La requête porte en plus un `?team_id=` qui désigne une autre team. Il doit être purement et
// simplement ignoré : un identifiant fourni par le client ne scope jamais rien.
//
// MUTATION : faire lire un `?team_id=` au handler → le service reçoit l'UUID de l'URL, ce test
// rouge.
func TestOverviewTeamComesFromResolvedSlug(t *testing.T) {
	résolue := uuid.New()
	usurpée := uuid.New()
	spy := &spyService{resolved: service.Team{ID: résolue, Slug: "acme", Name: "Acme"}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy,
		http.MethodGet, "/?team=acme&team_id="+usurpée.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, attendu %d", rec.Code, http.StatusOK)
	}
	if spy.gotSlug != "acme" {
		t.Errorf("slug résolu = %q, attendu %q", spy.gotSlug, "acme")
	}
	if spy.gotTeamID != résolue {
		t.Errorf("team passée au service = %s, attendu %s (celle du slug) — un identifiant "+
			"fourni par le client scope la lecture", spy.gotTeamID, résolue)
	}
}

// Sans `?team=`, la requête est refusée AVANT toute résolution : 400, et le service n'est pas
// appelé du tout.
func TestOverviewRefusesMissingTeam(t *testing.T) {
	spy := &spyService{resolved: service.Team{ID: uuid.New()}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d sans ?team=, attendu %d", rec.Code, http.StatusBadRequest)
	}
	if spy.gotSlug != "" || spy.stateCalled {
		t.Error("le service a été appelé sans team résolue")
	}
}

// GARANTIE 4 — un admin PORTEUR d'une team y est enfermé.
//
// Cette forme n'est plus insérable en base depuis la migration 000006, et rien ne la produit. Le
// garde existe quand même : une défense qui repose sur une contrainte écrite dans un autre
// fichier n'est pas une défense. Sans lui, la première session qui aura une raison d'épingler un
// admin à une team arme un piège que ni AdminOnly ni les tests d'isolation ne voient.
//
// Le refus est un 404, jamais un 403 : « cette team existe mais pas pour toi » laisse énumérer
// les teams de l'installation par balayage de slugs.
//
// MUTATION : retirer `if p.TeamID != uuid.Nil && team.ID != p.TeamID` de `teamFor` — dans
// `overview` comme dans `workspace`.
func TestTeamScopedAdminIsLockedToItsTeam(t *testing.T) {
	sienne := uuid.New()
	voisine := uuid.New()
	spy := &spyService{resolved: service.Team{ID: voisine, Slug: "voisine", Name: "Voisine"}}

	rec := serve(t, authtest.Admin(t, sienne), spy, http.MethodGet, "/?team=voisine")

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d en visant la team voisine, attendu %d", rec.Code, http.StatusNotFound)
	}
	if spy.stateCalled {
		t.Errorf("le service a lu la team %s alors que le token est épinglé à %s",
			spy.gotTeamID, sienne)
	}
}

// CONTRE-ÉPREUVE de la garantie 4 : le même admin épinglé atteint SA team.
//
// Sans elle, un garde qui refuserait tout — y compris la team du token — ferait passer le test
// précédent pour correct.
func TestTeamScopedAdminReachesItsOwnTeam(t *testing.T) {
	sienne := uuid.New()
	spy := &spyService{resolved: service.Team{ID: sienne, Slug: "sienne", Name: "Sienne"}}

	rec := serve(t, authtest.Admin(t, sienne), spy, http.MethodGet, "/?team=sienne")

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d sur sa propre team, attendu %d", rec.Code, http.StatusOK)
	}
	if spy.gotTeamID != sienne {
		t.Errorf("team lue = %s, attendu %s", spy.gotTeamID, sienne)
	}
}

// La référence est découpée par le ROUTEUR, et le numéro arrive typé au service. Une clé de
// projet contenant un tiret ne peut donc pas décaler la lecture, ce qu'un split maison sur
// `CORE-41` ferait.
func TestRefDetailPassesRoutedRef(t *testing.T) {
	résolue := uuid.New()
	spy := &spyService{resolved: service.Team{ID: résolue, Slug: "acme"}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/refs/CORE/41?team=acme")

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, attendu %d", rec.Code, http.StatusOK)
	}
	if spy.gotTeamID != résolue || spy.gotProjetKey != "CORE" || spy.gotNumber != 41 {
		t.Errorf("service appelé avec (team=%s, key=%q, number=%d), attendu (%s, \"CORE\", 41)",
			spy.gotTeamID, spy.gotProjetKey, spy.gotNumber, résolue)
	}
}

// Un numéro qui n'est pas un entier est refusé par le handler, avant toute résolution de team :
// une URL bricolée ne doit pas coûter un aller-retour en base.
func TestRefDetailRefusesNonNumericRef(t *testing.T) {
	spy := &spyService{resolved: service.Team{ID: uuid.New()}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/refs/CORE/abc?team=acme")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d pour un numéro non entier, attendu %d", rec.Code, http.StatusBadRequest)
	}
	if spy.gotSlug != "" {
		t.Error("la team a été résolue alors que la référence était malformée")
	}
}
