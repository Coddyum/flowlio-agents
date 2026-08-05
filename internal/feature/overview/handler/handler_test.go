package handler_test

// GUARANTEES 3 AND 4 OF THE TABLE IN docs/DESIGN-TUI.md § "Garanties de sécurité".
//
// What this file locks down: WHERE THE TEAM COMES FROM. It comes from the server-side resolution
// of the `?team=` slug, and from nowhere else — neither from a URL parameter nor from the
// principal.
//
// ACCEPTED GAP WITH THE NOTE: guarantee 4 is classified `I` there, with a raw SQL insert of an
// admin token carrying a team. It is written here as `U`. The shape under test — an admin
// principal whose TeamID is not nil — is exactly the one `authtest.Admin(t, teamID)` presents,
// and the guard it exercises is Go code, not a database constraint. A round trip to Postgres
// would have proven nothing more, and would have made a guarantee that can live in `make check`
// depend on `make test-integration`.
//
// The service is a spy: what we want to observe is not the response, it is the ARGUMENT the
// handler passes to it.

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

// spyService records what the handler passes to it, and returns what the test tells it to return.
//
// The embedded interface is nil: a method called without being redefined panics, which is harder
// to ignore than a zero returned silently.
type spyService struct {
	service.Service

	// resolved is the identity TeamBySlug will return, whatever the slug.
	resolved service.Team

	// gotSlug and gotTeamID are what the handler actually passed.
	gotSlug       string
	gotTeamID     uuid.UUID
	stateCalled   bool
	gotProjectKey string
	gotNumber     int64
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
	s.gotProjectKey = key
	s.gotNumber = number
	return service.RefDetail{}, nil
}

// serve mounts the two routes behind the REAL AdminOnly middleware and plays one request.
//
// The table is rewired here, which proves nothing about the one in `module.go` — that is what
// `overview/module_test.go` guards. This file tests what happens ONCE the route is reached.
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

// GUARANTEE 3 — the team passed to the service is the one the SLUG resolved.
//
// The request additionally carries a `?team_id=` designating another team. It must be purely and
// simply ignored: a client-supplied identifier never scopes anything.
//
// MUTATION: make the handler read a `?team_id=` → the service receives the UUID from the URL,
// this test goes red.
func TestOverviewTeamComesFromResolvedSlug(t *testing.T) {
	resolved := uuid.New()
	spoofed := uuid.New()
	spy := &spyService{resolved: service.Team{ID: resolved, Slug: "acme", Name: "Acme"}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy,
		http.MethodGet, "/?team=acme&team_id="+spoofed.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected %d", rec.Code, http.StatusOK)
	}
	if spy.gotSlug != "acme" {
		t.Errorf("resolved slug = %q, expected %q", spy.gotSlug, "acme")
	}
	if spy.gotTeamID != resolved {
		t.Errorf("team passed to the service = %s, expected %s (the slug's one) — a "+
			"client-supplied identifier scopes the read", spy.gotTeamID, resolved)
	}
}

// Without `?team=`, the request is rejected BEFORE any resolution: 400, and the service is not
// called at all.
func TestOverviewRefusesMissingTeam(t *testing.T) {
	spy := &spyService{resolved: service.Team{ID: uuid.New()}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d without ?team=, expected %d", rec.Code, http.StatusBadRequest)
	}
	if spy.gotSlug != "" || spy.stateCalled {
		t.Error("the service was called without a resolved team")
	}
}

// GUARANTEE 4 — an admin that CARRIES a team is locked into it.
//
// This shape can no longer be inserted in the database since migration 000006, and nothing
// produces it. The guard exists all the same: a defence resting on a constraint written in
// another file is not a defence. Without it, the first session that has a reason to pin an admin
// to a team arms a trap neither AdminOnly nor the isolation tests can see.
//
// The refusal is a 404, never a 403: "this team exists but not for you" lets the installation's
// teams be enumerated by sweeping slugs.
//
// MUTATION: remove `if p.TeamID != uuid.Nil && team.ID != p.TeamID` from `teamFor` — in
// `overview` just as in `workspace`.
func TestTeamScopedAdminIsLockedToItsTeam(t *testing.T) {
	own := uuid.New()
	neighbour := uuid.New()
	spy := &spyService{resolved: service.Team{ID: neighbour, Slug: "neighbour", Name: "Neighbour"}}

	rec := serve(t, authtest.Admin(t, own), spy, http.MethodGet, "/?team=neighbour")

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d when targeting the neighbouring team, expected %d", rec.Code, http.StatusNotFound)
	}
	if spy.stateCalled {
		t.Errorf("the service read team %s although the token is pinned to %s",
			spy.gotTeamID, own)
	}
}

// COUNTER-PROOF of guarantee 4: the same pinned admin reaches ITS team.
//
// Without it, a guard rejecting everything — including the token's own team — would make the
// previous test look correct.
func TestTeamScopedAdminReachesItsOwnTeam(t *testing.T) {
	own := uuid.New()
	spy := &spyService{resolved: service.Team{ID: own, Slug: "own", Name: "Own"}}

	rec := serve(t, authtest.Admin(t, own), spy, http.MethodGet, "/?team=own")

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d on its own team, expected %d", rec.Code, http.StatusOK)
	}
	if spy.gotTeamID != own {
		t.Errorf("team read = %s, expected %s", spy.gotTeamID, own)
	}
}

// The reference is split by the ROUTER, and the number reaches the service already typed. A
// project key containing a dash therefore cannot shift the reading, which a hand-rolled split on
// `CORE-41` would do.
func TestRefDetailPassesRoutedRef(t *testing.T) {
	resolved := uuid.New()
	spy := &spyService{resolved: service.Team{ID: resolved, Slug: "acme"}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/refs/CORE/41?team=acme")

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected %d", rec.Code, http.StatusOK)
	}
	if spy.gotTeamID != resolved || spy.gotProjectKey != "CORE" || spy.gotNumber != 41 {
		t.Errorf("service called with (team=%s, key=%q, number=%d), expected (%s, \"CORE\", 41)",
			spy.gotTeamID, spy.gotProjectKey, spy.gotNumber, resolved)
	}
}

// A number that is not an integer is rejected by the handler, before any team resolution: a
// hand-made URL must not cost a round trip to the database.
func TestRefDetailRefusesNonNumericRef(t *testing.T) {
	spy := &spyService{resolved: service.Team{ID: uuid.New()}}

	rec := serve(t, authtest.Admin(t, uuid.Nil), spy, http.MethodGet, "/refs/CORE/abc?team=acme")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d for a non-integer number, expected %d", rec.Code, http.StatusBadRequest)
	}
	if spy.gotSlug != "" {
		t.Error("the team was resolved although the reference was malformed")
	}
}
