package overview

// GUARANTEES 1 AND 20 OF THE TABLE IN docs/DESIGN-TUI.md § "Garanties de sécurité".
//
// What this file locks down: the ROUTE TABLE itself — that both routes sit behind `AdminOnly`,
// and that neither accepts anything other than a GET.
//
// IT STARTS FROM Routes() AND FROM NOTHING ELSE. A handler test rewiring the routes by hand would
// prove the scope of its own table, not the module's — the lesson was already paid for in
// `workspace/module_test.go`, where a route moved from `admin` to `authed` left the whole suite
// green.
//
// `mod` is built directly rather than through NewModule: NewModule would wire the real service on
// a store with a nil `*database.Queries`, and the first route reaching the handler would
// dereference nil. What is under test here is the table, not the wiring of the dependencies.

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

// stubService returns an empty response on the three methods of the contract.
//
// The embedded interface is nil: any method added tomorrow and not redefined here panics if a
// route calls it. That is deliberate — a route letting through a principal it should reject would
// not return an unexpected status, it would blow up, which is harder to ignore than a status code.
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

// route describes one entry of the route table. Both are admin: there is no mixed gate on this
// surface, and this file exists in part so that there never is one.
type route struct {
	method string
	path   string
}

// routes enumerates the TWO routes of Routes(), by hand.
//
// Written by hand and not derived from the mux: `http.ServeMux` does not expose its patterns, and
// deriving the list from the object under test would make it say what the code does rather than
// what it must do. The count is checked against the source of module.go, further down.
var routes = []route{
	{http.MethodGet, "/?team=acme"},
	{http.MethodGet, "/refs/CORE/41?team=acme"},
}

// serve plays one request on the REAL routes, with the given token.
func serve(t *testing.T, tok authtest.Token, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	m := &mod{h: handler.New(stubService{}), auth: tok.Auth}

	req := tok.Authorize(httptest.NewRequest(method, path, nil))
	rec := httptest.NewRecorder()
	m.Routes().ServeHTTP(rec, req)
	return rec
}

// GUARANTEE 1 — a project token reaches NO overview route.
//
// It is the guarantee that justifies the existence of a separate module. Under `auth.Middleware`,
// the DOCS agent would read the FRNT↔CORE thread, and the eight isolation tests of `task` and
// `issue` would stay green: they prove THEIR queries are scoped, not that no other route bypasses
// that scope.
//
// MUTATION: in module.go, replace `admin := m.auth.AdminOnly` with `m.auth.Middleware`.
//
// THE STATUS CODE ALONE DOES NOT KILL THIS MUTATION, and that is the first thing the test showed
// when playing it: `handler.principal` ALSO rejects a non-admin principal, so under `Middleware`
// the request still returned 403 — through the second defence, one layer further. A test that had
// only looked at the code would have been GREEN on the very mutation it exists to kill.
//
// Hence the assertion on the EXACT body: `auth.deny` writes `Forbidden` (StatusText) and sets
// `WWW-Authenticate`, the handler writes `forbidden` and sets nothing. That is what tells "the
// middleware rejected" apart from "the middleware let through and the handler caught it". The
// second defence stays in place — it is what protects the day somebody mounts one of these routes
// under `Middleware` — but it no longer masks the regression.
func TestOverviewRefusesProjectToken(t *testing.T) {
	const middlewareRefusal = `{"error":"Forbidden"}`

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Project(t, uuid.New(), uuid.New())

			rec := serve(t, tok, r.method, r.path)

			if rec.Code != http.StatusForbidden {
				t.Errorf("code = %d with a project token, expected %d — this route is open "+
					"to agents", rec.Code, http.StatusForbidden)
			}
			if got := rec.Body.String(); got != middlewareRefusal {
				t.Errorf("body = %s, expected %s — the refusal no longer comes from AdminOnly "+
					"but from a deeper layer: the route changed middleware",
					got, middlewareRefusal)
			}
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("WWW-Authenticate missing — this refusal is not the auth middleware's")
			}
		})
	}
}

// COUNTER-PROOF of guarantee 1: an admin token gets through.
//
// Without it, a middleware rejecting EVERYBODY — or a route that would not exist — would make the
// previous test look correct.
func TestOverviewAcceptsAdminToken(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Admin(t, uuid.Nil)

			rec := serve(t, tok, r.method, r.path)

			if rec.Code != http.StatusOK {
				t.Errorf("code = %d with an admin token, expected %d — the route no longer answers",
					rec.Code, http.StatusOK)
			}
		})
	}
}

// With no token at all, both routes return 401 and not 403: "I do not know who you are" and "I
// know who you are and it is not enough" are two different answers, and it is the middleware that
// tells them apart.
func TestOverviewRefusesAnonymous(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			tok := authtest.Admin(t, uuid.Nil)
			m := &mod{h: handler.New(stubService{}), auth: tok.Auth}

			rec := httptest.NewRecorder()
			m.Routes().ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("code = %d without a token, expected %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// GUARANTEE 20 — no write through this surface.
//
// The second half of the guarantee is in `make lint`: check-overview-scope.sh rejects any
// INSERT/UPDATE/DELETE in sql/queries/overview.sql. This one closes the other end: even a write
// query added by mistake would have no route to reach it.
//
// MUTATION: mount a write route in Routes().
func TestOverviewExposesOnlyGET(t *testing.T) {
	writes := []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}

	for _, r := range routes {
		for _, method := range writes {
			t.Run(method+" "+r.path, func(t *testing.T) {
				tok := authtest.Admin(t, uuid.Nil)

				rec := serve(t, tok, method, r.path)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("code = %d for %s, expected %d — a write route exists on this "+
						"surface", rec.Code, method, http.StatusMethodNotAllowed)
				}
			})
		}
	}
}

// The test's table covers ALL the routes of Routes(), and the count comes from the SOURCE of
// module.go.
//
// Comparing `len(routes)` to a constant written in the same file would guard nothing: a third
// route added to Routes() would leave the suite green. ServeMux does not expose its patterns, so
// counting the `r.Handle(` of the file is the only mechanical link possible between the test's
// table and the real one.
//
// MUTATION: add a route to Routes() without adding it to `routes` → this test goes red.
func TestRouteTableIsComplete(t *testing.T) {
	source, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatalf("reading module.go: %v", err)
	}
	declared := strings.Count(string(source), "r.Handle(")

	if declared == 0 {
		t.Fatal("no r.Handle( found in module.go — the counter no longer measures anything")
	}
	if len(routes) != declared {
		t.Errorf("Routes() declares %d routes, the test's table carries %d — a route was added "+
			"or removed without this file following", declared, len(routes))
	}

	for _, r := range routes {
		rec := serve(t, authtest.Admin(t, uuid.Nil), r.method, r.path)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: 404 — this path no longer exists in Routes()", r.method, r.path)
		}
	}
}
