package handler

// What this file locks down: the three trust-graph routes are ADMIN, and an agent token is refused
// there BEFORE reaching the handler.
//
// This is the guarantee Q3 of docs/DESIGN-TRUST.md rests on. An agent has full power over the files
// of its own repo; a trust it could declare would therefore be self-signed by the very party it is
// meant to constrain. If `admin` became `authed` on one of the three lines of Routes(), the whole
// of part 2 would fall — and nothing else in the suite would see it.
//
// The four tests in handler_test.go already cover the other half (an admin pinned to a team does
// not leave it, on the three routes): they are in teamForRoutes.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// trustRoutes is the subset of teamForRoutes that edits or reads the graph. It is written
// separately rather than derived by a filter: a trust route added tomorrow must show up here
// explicitly, not be caught by a prefix nobody remembered to check.
var trustRoutes = []teamForRoute{
	{"GET /trust", http.MethodGet, "/trust", "", "ListTrust"},
	{"POST /trust", http.MethodPost, "/trust", `{"first":"FRNT","second":"CORE"}`, "AllowTrust"},
	{"DELETE /trust/{first}/{second}", http.MethodDelete, "/trust/FRNT/CORE", "", "RevokeTrust"},
}

// A PROJECT token — the one an agent carries — is refused on all three graph routes.
//
// MUTATION: replacing `admin` with `authed` on one of the three lines of Routes() makes this test
// fail on that route. It is the only thing preventing an agent from allowing itself.
func TestAnAgentTokenDoesNotTouchTheTrustGraph(t *testing.T) {
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
				t.Errorf("code = %d, want %d — an agent can edit the graph that constrains it",
					rec.Code, http.StatusForbidden)
			}
			// The refusal comes from the MIDDLEWARE, not the handler: the service must have seen
			// nothing. A 403 returned after a service call would be an output filter, and the write
			// would already have happened.
			if len(svc.calls) != 0 {
				t.Errorf("the service was called (%v): the refusal comes after the work", svc.calls)
			}
		})
	}
}

// A request with no Authorization at all is refused too, and with a 401 — not a 403.
//
// Without this case, a middleware refusing EVERYONE would pass for correct on the previous test. It
// is the counterpart of TestAdminCarryingATeamActsOnItsOwn.
func TestTheGraphRequiresAuthentication(t *testing.T) {
	teams, _, _ := fixtures()

	for _, r := range trustRoutes {
		t.Run(r.name, func(t *testing.T) {
			svc := &fakeWorkspace{teams: teams}
			mux, _ := adminServer(t, uuid.Nil, svc)

			req := httptest.NewRequest(r.method, r.path+"?team=my-team", strings.NewReader(r.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("code = %d with no Authorization header, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// The team comes from teamFor and from NOWHERE else: a `team_id` slipped into the body of
// `POST /trust` must be refused by the decoder, not silently ignored.
//
// The field carries `json:"-"`, so DisallowUnknownFields rejects it. Without this test, removing
// the tag would turn the body into a second team resolver — and a global admin could open an edge
// in a team it never named in `?team=`.
func TestTheTeamDoesNotSlipIntoTheBody(t *testing.T) {
	teams, mine, other := fixtures()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, uuid.Nil, svc)

	body := `{"first":"FRNT","second":"CORE","team_id":"` + other.ID.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/trust?team="+mine.Slug, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d with a team_id in the body, want %d", rec.Code, http.StatusBadRequest)
	}
	if contains(svc.calls, "AllowTrust") {
		t.Errorf("the service received AllowTrust (%v): an invalid body reached the logic", svc.calls)
	}
}

// The write is refused BEFORE the body is decoded when the team does not resolve.
//
// The order matters: decoding first would give a pinned admin a way to tell "invalid body" from
// "forbidden team", hence an oracle on the existence of neighbouring teams.
func TestAValidBodyDoesNotRescueAForbiddenTeam(t *testing.T) {
	teams, mine, other := fixtures()

	svc := &fakeWorkspace{teams: teams}
	mux, raw := adminServer(t, mine.ID, svc)

	req := httptest.NewRequest(http.MethodPost, "/trust?team="+other.Slug,
		strings.NewReader(`{"first":"FRNT","second":"CORE"}`))
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
		t.Errorf("body = %s, want {\"error\":\"not found\"}", rec.Body.String())
	}
	if contains(svc.calls, "AllowTrust") {
		t.Errorf("the service received AllowTrust (%v)", svc.calls)
	}
}

// Typing guardrail: the fake must stay a complete service.Service. If a method is added to the
// contract without being added to the fake, this is where it shows, with a readable message.
var _ service.Service = (*fakeWorkspace)(nil)
