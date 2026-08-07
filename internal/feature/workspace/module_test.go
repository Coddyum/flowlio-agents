package workspace

// What this file locks down: the ROUTE TABLE itself — which route sits behind `admin`, which route
// sits behind `authed`.
//
// WHY IT EXISTS. The tests in `handler/` mount their own `http.ServeMux` and rewire the routes by
// hand. That is the right call for what they prove (teamFor, AdminOnly, the handler), but it
// duplicates the table — and a duplicated table proves nothing about the original. Mutation played:
// replacing `admin` with `authed` on `POST /trust` in Routes() left the WHOLE suite green,
// including the test that exists to forbid exactly that.
//
// This test starts from `Routes()` and from nothing else. There is no second table any more.
//
// It builds `mod` directly rather than through `NewModule`: NewModule would wire the REAL service
// onto a store with a nil `*database.Queries`, and the first `authed` route reaching the handler
// would dereference nil. What is under test here is the table, not the wiring of the dependencies.

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

// stubService implements ONLY what the `authed` routes call.
//
// The embedded interface is nil: any method not redefined panics when called. That is deliberate —
// if a route we believe closed were to let a project token through, the test would not return an
// unexpected status, it would blow up, which is harder to ignore.
type stubService struct {
	service.Service
}

func (stubService) ListProjects(context.Context, uuid.UUID) ([]service.Project, error) {
	return nil, nil
}

func (stubService) Whoami(context.Context, uuid.UUID, uuid.UUID) (service.Identity, error) {
	return service.Identity{}, nil
}

// route describes one entry of the table, and the scope it MUST require.
type route struct {
	method string
	path   string
	body   string
	// adminOnly says what the route promises. It is the only piece of data in this file; everything
	// else is mechanical.
	adminOnly bool
}

// routes enumerates the ELEVEN routes of Routes(), by hand.
//
// Written by hand rather than derived from the mux: `http.ServeMux` does not expose its patterns,
// and even if it did, deriving the list from the object under test would make the test state what
// the code does rather than what it must do. A route added without being added here escapes the
// test — that is the price, and it is why the count is checked below.
var routes = []route{
	{http.MethodPost, "/teams", `{"slug":"t","name":"T"}`, true},
	{http.MethodGet, "/teams", "", true},
	{http.MethodPost, "/projects", `{"key":"FRNT","name":"F"}`, true},
	{http.MethodGet, "/projects", "", false},
	{http.MethodPost, "/tokens", `{"project":"FRNT","name":"a"}`, true},
	{http.MethodGet, "/tokens", "", true},
	{http.MethodDelete, "/tokens/" + uuid.NewString(), "", true},
	{http.MethodGet, "/trust", "", true},
	{http.MethodPost, "/trust", `{"from":"FRNT","to":"CORE"}`, true},
	{http.MethodDelete, "/trust/FRNT/CORE", "", true},
	{http.MethodGet, "/whoami", "", false},
}

// serveWithProjectToken plays a request against the REAL routes, with a PROJECT token.
func serveWithProjectToken(t *testing.T, r route) *httptest.ResponseRecorder {
	t.Helper()

	tok := authtest.Project(t, uuid.New(), uuid.New())
	m := &mod{h: handler.New(tok.Auth, stubService{}), auth: tok.Auth}

	req := tok.Authorize(httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
	rec := httptest.NewRecorder()
	m.Routes().ServeHTTP(rec, req)
	return rec
}

// Every route requires exactly the scope the table promises.
//
// The three trust-graph routes are the most sensitive: an agent has full power over the files of
// its own repo, so a trust it declared would be self-signed by the very party it constrains.
// `admin` there is the only thing holding part 2 up.
//
// MUTATION: turning an `admin` route into an `authed` one in Routes() makes this test fail on it,
// and the reverse too.
func TestEveryRouteRequiresTheScopeItAnnounces(t *testing.T) {
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := serveWithProjectToken(t, r)

			if r.adminOnly {
				if rec.Code != http.StatusForbidden {
					t.Errorf("code = %d with a project token, want %d — this route is open to agents",
						rec.Code, http.StatusForbidden)
				}
				return
			}

			// Counter-check: without it, a middleware refusing EVERYONE would make the admin half of
			// the test pass for correct.
			if rec.Code == http.StatusForbidden {
				t.Errorf("code = %d, this route must stay open to a project token", rec.Code)
			}
		})
	}
}

// The test's table covers EVERY route of Routes(), and the count comes from Routes() itself.
//
// THE FIRST VERSION OF THIS TEST GUARDED NOTHING. It compared `len(routes)` to a constant written
// ten lines above, in the SAME file: adding a twelfth route to Routes(), under `authed`, left the
// whole suite green. A test comparing a table to its own constant measures its internal
// consistency, not the code's — this was the third time in that session the exact pattern showed up.
//
// The count is now READ FROM THE SOURCE of module.go. It is ugly, and it is the price: ServeMux
// does not expose its patterns, so there is no way to ask it what it carries. Counting the
// `r.Handle(` occurrences of a file is the only mechanical link possible between the test's table
// and the real one.
//
// MUTATION: adding a route to Routes() without adding it to `routes` → this test goes red.
func TestTheRouteTableIsComplete(t *testing.T) {
	source, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatalf("reading module.go: %v", err)
	}
	declared := strings.Count(string(source), "r.Handle(")

	if declared == 0 {
		t.Fatal("no r.Handle( found in module.go — the counter no longer measures anything")
	}
	if len(routes) != declared {
		t.Errorf("Routes() declares %d routes, the test table carries %d — a route was added or "+
			"removed without this file following", declared, len(routes))
	}

	for _, r := range routes {
		rec := serveWithProjectToken(t, r)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: 404 — this path no longer exists in Routes()", r.method, r.path)
		}
	}
}
