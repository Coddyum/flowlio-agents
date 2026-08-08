package handler

// What this file locks down: an admin token carrying a team can act ONLY on that team.
//
// The scenario is built FROM THE TOP — real auth middleware, real AdminOnly, real routes, real
// handler — because that is exactly the chain that gave the false sense of safety: `AdminOnly`
// accepts the principal, the feature's eight isolation tests stay green, and
// `POST /tokens?team=<neighbour>` issues a project token at the neighbour's, secret in clear.
//
// Only the auth STORE is fake, and it has to be: since migration 000006, the row
// `scope='admin' AND team_id IS NOT NULL` is no longer insertable in the database. The scenario can
// therefore NO LONGER be built end to end through SQL — which is the point of the migration. The
// fake store is what allows us to keep proving the code holds without the constraint, rather than
// thanks to it.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// fakeWorkspace records what the handler asks of it. That is where the assertion that matters
// plays out: a teamFor refusal must cut in BEFORE the service does any work, otherwise the refusal
// is only an output filter and the side effect has already happened.
type fakeWorkspace struct {
	teams map[string]service.Team
	calls []string

	// gotPinned is the team `ListTeams` was scoped to. That argument is the whole assertion of
	// part 2: the route used to name no scope at all.
	gotPinned uuid.UUID

	// deleteErr is what DeleteProject answers. A fake that can only succeed proves no error path:
	// this field is what lets the 409 of `delete_project_test.go` be reached at all.
	deleteErr error
	// gotDeleted is the project identifier DeleteProject received, so a test can check the handler
	// passes the one from the path and not something it made up.
	gotDeleted uuid.UUID

	// deleteTeamErr is what DeleteTeam answers, and gotDeletedTeam the identifier it received. Both
	// exist for the same reason as the two fields above: a fake that can only succeed proves no
	// error path, and a fake that does not record its argument cannot say WHICH team was deleted —
	// which on this route is the entire boundary.
	deleteTeamErr  error
	gotDeletedTeam uuid.UUID
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

func (f *fakeWorkspace) ListTeams(_ context.Context, pinned uuid.UUID) ([]service.Team, error) {
	f.note("ListTeams")
	f.gotPinned = pinned
	return nil, nil
}

func (f *fakeWorkspace) DeleteTeam(_ context.Context, teamID uuid.UUID) error {
	f.note("DeleteTeam")
	f.gotDeletedTeam = teamID
	return f.deleteTeamErr
}

func (f *fakeWorkspace) CreateProject(context.Context, service.CreateProjectInput) (service.Project, error) {
	f.note("CreateProject")
	return service.Project{}, nil
}

func (f *fakeWorkspace) ListProjects(context.Context, uuid.UUID) ([]service.Project, error) {
	f.note("ListProjects")
	return nil, nil
}

func (f *fakeWorkspace) DeleteProject(_ context.Context, _ uuid.UUID, projectID uuid.UUID) error {
	f.note("DeleteProject")
	f.gotDeleted = projectID
	return f.deleteErr
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

func (f *fakeWorkspace) AllowTrust(context.Context, service.TrustPairInput) (service.TrustDecision, error) {
	f.note("AllowTrust")
	return service.TrustDecision{}, nil
}

func (f *fakeWorkspace) RevokeTrust(context.Context, service.TrustPairInput) (service.TrustDecision, error) {
	f.note("RevokeTrust")
	return service.TrustDecision{}, nil
}

func (f *fakeWorkspace) ListTrust(context.Context, uuid.UUID) ([]service.TrustEdge, error) {
	f.note("ListTrust")
	return nil, nil
}

// adminServer mounts the admin routes that go through teamFor, with the real auth middleware, and
// returns the raw token to present. teamID is the team the admin token CARRIES: uuid.Nil for the
// global admin, the one that actually exists today.
func adminServer(t *testing.T, teamID uuid.UUID, svc service.Service) (http.Handler, string) {
	t.Helper()
	return tokenServer(t, auth.TokenRecord{Scope: auth.ScopeAdmin, TeamID: teamID}, svc)
}

// tokenServer mounts the same routes behind the REAL AdminOnly, for a token whose scope the test
// chooses.
//
// The harness comes from authtest: `auth.contextKey` is private, so no test can drop a Principal in
// by hand — it has to go through the real middleware, on a real minted token. That is what makes it
// possible to present a PROJECT token to the admin routes and check it is refused BEFORE the
// handler.
func tokenServer(t *testing.T, rec auth.TokenRecord, svc service.Service) (http.Handler, string) {
	t.Helper()

	tok := authtest.New(t, rec)
	authSvc := tok.Auth

	h := New(authSvc, svc)
	admin := authSvc.AdminOnly

	mux := http.NewServeMux()
	// POST and GET on `/teams` resolve NO slug and therefore never reach teamFor: they are absent
	// from teamForRoutes and covered by their own tests, further down. The DELETE is the opposite —
	// it names a team, so it goes through teamFor like the five `?team=` routes, and it is in the
	// list.
	mux.Handle("POST /teams", admin(http.HandlerFunc(h.CreateTeam)))
	mux.Handle("GET /teams", admin(http.HandlerFunc(h.ListTeams)))
	mux.Handle("DELETE /teams/{slug}", admin(http.HandlerFunc(h.DeleteTeam)))
	mux.Handle("POST /projects", admin(http.HandlerFunc(h.CreateProject)))
	mux.Handle("GET /projects", admin(http.HandlerFunc(h.ListProjects)))
	mux.Handle("DELETE /projects/{id}", admin(http.HandlerFunc(h.DeleteProject)))
	mux.Handle("POST /tokens", admin(http.HandlerFunc(h.CreateToken)))
	mux.Handle("GET /tokens", admin(http.HandlerFunc(h.ListTokens)))
	mux.Handle("DELETE /tokens/{id}", admin(http.HandlerFunc(h.RevokeToken)))
	mux.Handle("GET /trust", admin(http.HandlerFunc(h.ListTrust)))
	mux.Handle("POST /trust", admin(http.HandlerFunc(h.AllowTrust)))
	mux.Handle("DELETE /trust/{from}/{to}", admin(http.HandlerFunc(h.RevokeTrust)))

	return mux, tok.Plain
}

// teamForRoute describes a route whose team is resolved by teamFor. All nine are here: the guard
// lives in teamFor, so a route added tomorrow that calls it is protected without thinking — and a
// route that does NOT call it must leap out at whoever reads this list.
type teamForRoute struct {
	name   string
	method string
	// path is a printf template when slugInPath is set, and a plain path otherwise.
	path    string
	body    string
	svcCall string
	// slugInPath says the team is named in the PATH rather than in `?team=`. Exactly one route does
	// that — `DELETE /teams/{slug}`, where the team is the object and not the scope — and it goes
	// through the same teamFor as the rest, which is why it belongs in this list rather than in a
	// harness of its own.
	slugInPath bool
}

// url builds the request target for a slug. The two spellings differ in one place only, and this
// method is that place.
func (r teamForRoute) url(slug string) string {
	if r.slugInPath {
		return fmt.Sprintf(r.path, slug)
	}
	return r.path + "?team=" + slug
}

var teamForRoutes = []teamForRoute{
	// Deleting a TEAM is the most destructive gesture in the whole API — every repo of the team,
	// their backlog, their threads, their memories and their tokens go in one statement. It resolves
	// its team through teamFor like everything else here, so an admin pinned to a team cannot delete
	// the neighbour's account.
	{"DELETE /teams/{slug}", http.MethodDelete, "/teams/%s", "", "DeleteTeam", true},
	{"POST /projects", http.MethodPost, "/projects", `{"key":"FRNT","name":"Front"}`, "CreateProject", false},
	{"GET /projects", http.MethodGet, "/projects", "", "ListProjects", false},
	// Deleting a repo is the most destructive gesture an admin token can make — the repo's tokens,
	// tasks, memories and trust edges go with it — so the boundary matters here more than anywhere
	// else in this list. An admin pinned to a team must not be able to delete the neighbour's repo.
	{"DELETE /projects/{id}", http.MethodDelete, "/projects/" + uuid.NewString(), "", "DeleteProject", false},
	{"POST /tokens", http.MethodPost, "/tokens", `{"project":"FRNT","name":"agent"}`, "CreateToken", false},
	{"GET /tokens", http.MethodGet, "/tokens", "", "ListTokens", false},
	{"DELETE /tokens/{id}", http.MethodDelete, "/tokens/" + uuid.NewString(), "", "RevokeToken", false},
	// The trust graph edits WHO MAY WRITE TO WHOM. An admin pinned to a team that could reach
	// `POST /trust?team=<neighbour>` would open the channel at the neighbour's — that is, exactly
	// the hole part 2 closes, reopened through its administration door. All three routes are
	// therefore in this list, and the four tests below cover them.
	{"GET /trust", http.MethodGet, "/trust", "", "ListTrust", false},
	{"POST /trust", http.MethodPost, "/trust", `{"from":"FRNT","to":"CORE"}`, "AllowTrust", false},
	{"DELETE /trust/{from}/{to}", http.MethodDelete, "/trust/FRNT/CORE", "", "RevokeTrust", false},
}

// call plays an admin request on a route and returns the status, the body, and the calls the
// service received.
func call(t *testing.T, teamID uuid.UUID, r teamForRoute, teams map[string]service.Team, slug string) (int, string, []string) {
	t.Helper()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, teamID, svc)

	req := httptest.NewRequest(r.method, r.url(slug), strings.NewReader(r.body))
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

// fixtures sets up two teams: the token's own, and the neighbour it must not reach.
func fixtures() (map[string]service.Team, service.Team, service.Team) {
	mine := service.Team{ID: uuid.New(), Slug: "my-team", Name: "Mine"}
	other := service.Team{ID: uuid.New(), Slug: "neighbour-team", Name: "The neighbour"}
	return map[string]service.Team{mine.Slug: mine, other.Slug: other}, mine, other
}

// An admin carrying a team is locked inside it, on EVERY route that resolves a team.
//
// MUTATION: removing the `if p.TeamID != uuid.Nil && team.ID != p.TeamID` guard from teamFor makes
// this test fail on all five routes — the neighbour then answers 2xx and the service is called.
func TestAdminCarryingATeamDoesNotLeaveIt(t *testing.T) {
	teams, mine, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, mine.ID, r, teams, other.Slug)

			if code != http.StatusNotFound {
				t.Errorf("?team=%s: code = %d, want %d — an admin pinned to %s acted on the neighbour",
					other.Slug, code, http.StatusNotFound, mine.Slug)
			}
			if body != `{"error":"not found"}` {
				t.Errorf("body = %s, want {\"error\":\"not found\"}", body)
			}
			if contains(calls, r.svcCall) {
				t.Errorf("the service received %s: the refusal comes AFTER the work, so the side effect happened (calls: %v)",
					r.svcCall, calls)
			}
		})
	}
}

// The refusal must be indistinguishable from a non-existent team: "it exists but not for you" would
// let one enumerate an installation's teams by sweeping slugs.
//
// MUTATION: answering auth.ErrForbidden instead of service.ErrNotFound in teamFor makes this test
// fail — 403 on one side, 404 on the other.
func TestTheRefusalIsIndistinguishableFromAMissingTeam(t *testing.T) {
	teams, mine, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			refusedCode, refusedBody, _ := call(t, mine.ID, r, teams, other.Slug)
			unknownCode, unknownBody, _ := call(t, mine.ID, r, teams, "team-that-does-not-exist")

			if refusedCode != unknownCode {
				t.Errorf("codes differ: neighbour = %d, unknown slug = %d — the code says the team exists",
					refusedCode, unknownCode)
			}
			if refusedBody != unknownBody {
				t.Errorf("bodies differ: neighbour = %s, unknown slug = %s", refusedBody, unknownBody)
			}
		})
	}
}

// Its own team stays reachable, obviously. Without this case, a guard refusing EVERY admin carrying
// a team would pass for correct.
func TestAdminCarryingATeamActsOnItsOwn(t *testing.T) {
	teams, mine, _ := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, mine.ID, r, teams, mine.Slug)

			if code == http.StatusNotFound || code >= http.StatusInternalServerError {
				t.Errorf("code = %d (body %s): its own team is refused to it", code, body)
			}
			if !contains(calls, r.svcCall) {
				t.Errorf("the service did not receive %s (calls: %v)", r.svcCall, calls)
			}
		})
	}
}

// The GLOBAL admin — the one bootstrapping actually creates, with no team — keeps its reach over
// every team. This is the guardrail in the other direction: the fix must not break the only admin
// token that exists today.
func TestGlobalAdminReachesAnyTeam(t *testing.T) {
	teams, _, other := fixtures()

	for _, r := range teamForRoutes {
		t.Run(r.name, func(t *testing.T) {
			code, body, calls := call(t, uuid.Nil, r, teams, other.Slug)

			if code == http.StatusNotFound || code >= http.StatusInternalServerError {
				t.Errorf("code = %d (body %s): the global admin can no longer administer", code, body)
			}
			if !contains(calls, r.svcCall) {
				t.Errorf("the service did not receive %s (calls: %v)", r.svcCall, calls)
			}
		})
	}
}

// --------------------------------------------------------------------------------------------
// The two `/teams` routes. Neither resolves a slug, so neither goes through teamFor: their guard
// is written where they are, and it is these four tests that hold it.
// --------------------------------------------------------------------------------------------

// callTeams plays a request on a `/teams` route under an admin token carrying teamID.
func callTeams(t *testing.T, teamID uuid.UUID, method, body string) (int, string, *fakeWorkspace) {
	t.Helper()

	teams, _, _ := fixtures()
	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, teamID, svc)

	req := httptest.NewRequest(method, "/teams", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec.Code, strings.TrimSpace(rec.Body.String()), svc
}

// PART 2 — `GET /teams` names a tenancy scope, which it did not before.
//
// It was the only read of the repository carrying none: under a shared installation, one admin
// token enumerated every team of the host, slugs and names included. The assertion is on the
// ARGUMENT, not on the response: a filter applied to the answer would leave the full list read.
//
// MUTATION: put `h.svc.ListTeams(r.Context(), uuid.Nil)` back in the handler, or drop the
// `pinned != uuid.Nil` branch of the service — this test goes red.
func TestListTeamsIsScopedToAPinnedAdmin(t *testing.T) {
	_, mine, _ := fixtures()

	code, body, svc := callTeams(t, mine.ID, http.MethodGet, "")

	if code != http.StatusOK {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusOK)
	}
	if !contains(svc.calls, "ListTeams") {
		t.Fatalf("the service was not called (calls: %v)", svc.calls)
	}
	if svc.gotPinned != mine.ID {
		t.Errorf("scope passed to ListTeams = %s, want %s — the route enumerates the whole installation",
			svc.gotPinned, mine.ID)
	}
}

// COUNTER-PROOF: the global admin — the only one bootstrapping creates today — keeps its reach.
// Without this case, a handler passing a random non-nil scope would look correct.
func TestListTeamsLeavesTheGlobalAdminUnbounded(t *testing.T) {
	code, body, svc := callTeams(t, uuid.Nil, http.MethodGet, "")

	if code != http.StatusOK {
		t.Fatalf("code = %d (body %s), want %d", code, body, http.StatusOK)
	}
	if svc.gotPinned != uuid.Nil {
		t.Errorf("scope passed to ListTeams = %s, want the nil UUID — the global admin can no "+
			"longer administer the installation", svc.gotPinned)
	}
}

// PART 1 — creating a team is an act ON THE INSTALLATION, so a pinned admin is refused.
//
// The refusal cuts in BEFORE the service: a 403 pronounced after the insert would be an output
// filter over a team that already exists.
//
// MUTATION: remove the `principal.TeamID != uuid.Nil` guard from CreateTeam — this test goes red.
func TestPinnedAdminCannotCreateATeam(t *testing.T) {
	_, mine, _ := fixtures()

	code, body, svc := callTeams(t, mine.ID, http.MethodPost, `{"slug":"third","name":"Third"}`)

	if code != http.StatusForbidden {
		t.Errorf("code = %d, want %d — an admin pinned to %s created a team outside its boundary",
			code, http.StatusForbidden, mine.Slug)
	}
	if body != `{"error":"forbidden"}` {
		t.Errorf("body = %s, want {\"error\":\"forbidden\"}", body)
	}
	if contains(svc.calls, "CreateTeam") {
		t.Errorf("the service received CreateTeam: the team was created, then the answer refused (calls: %v)",
			svc.calls)
	}
}

// COUNTER-PROOF: the global admin still creates teams. Without it, a guard refusing everybody
// would pass for correct.
func TestGlobalAdminStillCreatesATeam(t *testing.T) {
	code, body, svc := callTeams(t, uuid.Nil, http.MethodPost, `{"slug":"third","name":"Third"}`)

	if code != http.StatusCreated {
		t.Errorf("code = %d (body %s), want %d: the installation can no longer be administered",
			code, body, http.StatusCreated)
	}
	if !contains(svc.calls, "CreateTeam") {
		t.Errorf("the service did not receive CreateTeam (calls: %v)", svc.calls)
	}
}
