package feature_test

// EVERY ROUTE OF EVERY MODULE HAS A DECLARED SCOPE, AND THIS FILE IS THE INVENTORY.
//
// WHAT IT ADDS TO matrix_integration_test.go, WHICH IS NOT THE SAME THING. The matrix picks ONE
// representative surface per module and drives it all the way to Postgres, because its 200 column
// is the positive control that makes the two refusal columns mean anything. That is its strength
// and its blind spot: a module is represented by one route, always a GET, and the seven other
// routes of `workspace` — three of which WRITE — are outside it. A `POST /trust` accidentally
// mounted under `Middleware` would leave the matrix entirely green.
//
// This file closes that gap the other way round: EVERY route, every method, but refusals only.
// The division is deliberate —
//
//	matrix          : one route per module, all three principals, real database, 200 included
//	this inventory  : all 28 routes, the principal that must be REFUSED, no database at all
//
// A refusal never reaches a store, which is why this runs in `make check` with a nil database. It
// is not a shortcut: a route that let the wrong principal through would reach the store and PANIC
// on that nil, and a panic is a louder failure than the assertion it replaces.
//
// THE INVENTORY IS CHECKED AGAINST THE SOURCES. `TestEveryMountedRouteIsInTheInventory` reads the
// `r.Handle(...)` lines out of every module.go and fails if one of them is missing here. Without
// it this table would be a list of what somebody once wrote down, and a route added tomorrow would
// be scoped by nobody.

import (
	"bytes"
	"database/sql"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	coreregistry "github.com/Coddyum/flowlio-agents/internal/core"
	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref"
	"github.com/Coddyum/flowlio-agents/internal/feature/task"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace"
	"github.com/google/uuid"
)

// Who a route admits. Every route is one of the two: there is no third answer, and a route whose
// answer is "it depends" is a route whose scope is not decided.
const (
	// adminOnly — an administration token, and a project token is refused.
	adminOnly = "admin"
	// projectOnly — a project token, and an admin token is refused.
	projectOnly = "project"
	// anyToken — any valid token. `GET /projects` and `GET /whoami`, and nothing else: they are
	// the two reads an agent needs to know who it is and who its siblings are.
	anyToken = "any"
)

// route is one mounted route and the scope it must enforce.
type route struct {
	feature string
	method  string
	// pattern is the route as written in module.go, so the inventory can be compared to the source
	// literally rather than through a transformation that could hide a mismatch.
	pattern string
	// path is what the request actually asks for: the pattern with its wildcards filled in.
	path  string
	scope string
}

// inventory is written BY HAND, exactly like the matrix, and for the same reason: derived from the
// muxes it would say what the code does rather than what it must do.
var inventory = []route{
	// workspace — the mixed module, and the one this file exists for. Ten of its eleven routes are
	// outside the matrix, and three of them write.
	{workspace.Key, http.MethodPost, "POST /teams", "/teams", adminOnly},
	{workspace.Key, http.MethodGet, "GET /teams", "/teams", adminOnly},
	{workspace.Key, http.MethodPost, "POST /projects", "/projects", adminOnly},
	{workspace.Key, http.MethodGet, "GET /projects", "/projects?team=x", anyToken},
	{workspace.Key, http.MethodPost, "POST /tokens", "/tokens", adminOnly},
	{workspace.Key, http.MethodGet, "GET /tokens", "/tokens", adminOnly},
	{workspace.Key, http.MethodDelete, "DELETE /tokens/{id}", "/tokens/" + uuid.NewString(), adminOnly},
	{workspace.Key, http.MethodGet, "GET /trust", "/trust", adminOnly},
	{workspace.Key, http.MethodPost, "POST /trust", "/trust", adminOnly},
	{workspace.Key, http.MethodDelete, "DELETE /trust/{from}/{to}", "/trust/CORE/FRNT", adminOnly},
	{workspace.Key, http.MethodGet, "GET /whoami", "/whoami", anyToken},

	// task — every route project-scoped, writes included.
	{task.Key, http.MethodPost, "POST /{$}", "/", projectOnly},
	{task.Key, http.MethodGet, "GET /{$}", "/", projectOnly},
	{task.Key, http.MethodGet, "GET /{number}", "/1", projectOnly},
	{task.Key, http.MethodPatch, "PATCH /{number}", "/1", projectOnly},
	{task.Key, http.MethodPost, "POST /{number}/blockers", "/1/blockers", projectOnly},
	{task.Key, http.MethodDelete, "DELETE /{number}/blockers/{blocker}", "/1/blockers/2", projectOnly},

	{issue.Key, http.MethodPost, "POST /{$}", "/", projectOnly},
	{issue.Key, http.MethodGet, "GET /{$}", "/", projectOnly},
	{issue.Key, http.MethodGet, "GET /{project}/{number}", "/CORE/1", projectOnly},
	{issue.Key, http.MethodPost, "POST /{project}/{number}/answer", "/CORE/1/answer", projectOnly},

	{inbox.Key, http.MethodGet, "GET /{$}", "/", projectOnly},

	{overview.Key, http.MethodGet, "GET /{$}", "/?team=x", adminOnly},
	{overview.Key, http.MethodGet, "GET /refs/{project}/{number}", "/refs/CORE/1?team=x", adminOnly},

	// memory — M5. Project-scoped without exception, the write as much as the reads: a
	// repository's reasoning belongs to that repository, and an admin token reading it would be
	// the third party the design refused.
	{memory.Key, http.MethodPost, "POST /{$}", "/", projectOnly},
	{memory.Key, http.MethodGet, "GET /{$}", "/", projectOnly},
	{memory.Key, http.MethodGet, "GET /index", "/index", projectOnly},
	{memory.Key, http.MethodGet, "GET /{slug}", "/D25", projectOnly},

	{ref.Key, http.MethodGet, "GET /{project}/{number}", "/CORE/1", projectOnly},
}

// mount builds the seven modules on the auth service of one token, with NO database.
//
// The nil *sql.DB is the point rather than a shortcut: every case below drives a principal that
// must be REFUSED, and a refusal is pronounced by the middleware or by the handler guard, before
// any store call. A route that let the principal through would reach the store and panic — which
// is a louder failure than the assertion it stands in for, and it is caught by the same test run.
func mount(t *testing.T, tok authtest.Token) map[string]http.Handler {
	t.Helper()

	registry := coreregistry.NewRegistry()
	cfg := module.ModuleConfig{
		DB:       database.New((*sql.DB)(nil)),
		RawDB:    nil,
		Core:     core{svc: tok.Auth},
		Registry: registry,
	}

	mounted := map[string]http.Handler{}
	for _, m := range []module.Module{
		workspace.NewModule(cfg),
		task.NewModule(cfg),
		issue.NewModule(cfg),
		inbox.NewModule(cfg),
		overview.NewModule(cfg),
		memory.NewModule(cfg),
		ref.NewModule(cfg),
	} {
		registry.Register(m.Key(), m)
		mounted[m.Key()] = m.Routes()
	}
	return mounted
}

// play sends one request and returns the recorder AND what the handler guards logged.
//
// THE STATUS ALONE IS NOT ENOUGH, and matrix_integration_test.go learned it the hard way twice.
// Every module refuses TWICE: the route carries a guard, and the handler repeats it. Removing the
// route guard therefore leaves the status at 403 — the handler catches it — and a test observing
// only the status stays green on exactly the regression it exists to catch.
//
// The handler guards are the only thing that speaks on that second path, so the log is what tells
// the two apart. In every correct case it must stay SILENT: a rejected principal is rejected
// before entering the module.
//
// The log is a package-level variable; no test in this file runs in parallel, so diverting it
// cannot spill onto another case.
func play(t *testing.T, mounted map[string]http.Handler, r route, tok *authtest.Token) (*httptest.ResponseRecorder, string) {
	t.Helper()

	router, ok := mounted[r.feature]
	if !ok {
		t.Fatalf("no module mounted under %q", r.feature)
	}

	req := httptest.NewRequest(r.method, r.path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if tok != nil {
		req = tok.Authorize(req)
	}

	var journal bytes.Buffer
	log.SetOutput(&journal)
	defer log.SetOutput(os.Stderr)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, journal.String()
}

// guardSpoke says whether a handler guard pronounced the refusal. The phrases are the ones
// `handlerGuards` already enumerates in matrix_integration_test.go, and `TestHandlerGuardsStillExist`
// is what keeps them matching a real source line.
func guardSpoke(journal string) string {
	for _, guard := range handlerGuards {
		if strings.Contains(journal, guard) {
			return guard
		}
	}
	return ""
}

// A PROJECT TOKEN IS REFUSED BY EVERY ADMIN ROUTE — writes included, which is where the matrix
// cannot see. `POST /trust` is the one that matters most: it edits WHO MAY WRITE TO WHOM, and an
// agent able to reach it would grant itself the channel the trust graph exists to withhold.
//
// MUTATION: mount any admin route under `m.auth.Middleware` instead of `admin` — this test goes
// red on that route.
func TestAProjectTokenIsRefusedByEveryAdminRoute(t *testing.T) {
	tok := authtest.Project(t, uuid.New(), uuid.New())
	mounted := mount(t, tok)

	for _, r := range inventory {
		if r.scope != adminOnly {
			continue
		}
		t.Run(r.feature+" "+r.pattern, func(t *testing.T) {
			rec, journal := play(t, mounted, r, &tok)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("code = %d, want %d — a project token reached an administration route",
					rec.Code, http.StatusForbidden)
			}
			if guard := guardSpoke(journal); guard != "" {
				t.Errorf("the handler guard %q spoke: the principal entered the module, so the "+
					"route's own gate is gone and only the second line of defence is standing",
					guard)
			}
			// Only `deny()` of internal/core/auth sets this header. Reading it is what tells the
			// route's own guard apart from a second line of defence inside the handler: without
			// it, a route that lost its middleware stays green because the handler catches it.
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("the refusal did not come from the auth layer — the route's own guard is gone, "+
					"and only a handler check is still standing (headers: %v)", rec.Header())
			}
		})
	}
}

// AN ADMIN TOKEN IS REFUSED BY EVERY PROJECT ROUTE. This is the direction M5 needs: `memory` is
// entirely project-scoped, and an admin reading it would be exactly the third party the card
// refused on 2026-08-05.
//
// The refusal comes from the MODULE here, not from the auth layer: `requireProjectScope` is a
// local middleware, and `deny()` is not what pronounces it. Asserting the absence of the header is
// therefore as load-bearing as asserting its presence above — it is what proves the refusal was
// pronounced where it should be.
//
// MUTATION: drop `requireProjectScope` from any project module's Routes() — this test goes red.
func TestAnAdminTokenIsRefusedByEveryProjectRoute(t *testing.T) {
	tok := authtest.Admin(t, uuid.Nil)
	mounted := mount(t, tok)

	for _, r := range inventory {
		if r.scope != projectOnly {
			continue
		}
		t.Run(r.feature+" "+r.pattern, func(t *testing.T) {
			rec, journal := play(t, mounted, r, &tok)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("code = %d, want %d — an admin token reached a project route",
					rec.Code, http.StatusForbidden)
			}
			// THIS IS THE ASSERTION THE STATUS CANNOT MAKE. `requireProjectScope` and the
			// handler's own `scope()` answer identically, body included; without the log, removing
			// the route's middleware leaves this test green. Verified by mutation: it does.
			if guard := guardSpoke(journal); guard != "" {
				t.Errorf("the handler guard %q spoke: requireProjectScope is no longer on this "+
					"route, and the admin token was only stopped by the handler repeating the check",
					guard)
			}
			if rec.Header().Get("WWW-Authenticate") == "Bearer" {
				t.Errorf("the refusal came from the auth layer, so the route is mounted behind " +
					"AdminOnly rather than behind requireProjectScope — the wrong answer for the " +
					"wrong reason")
			}
		})
	}
}

// NO ROUTE ANSWERS WITHOUT A TOKEN. The absent principal needs no scope column: 401 everywhere,
// including on the two routes open to any token.
func TestNoRouteAnswersWithoutAToken(t *testing.T) {
	mounted := mount(t, authtest.Project(t, uuid.New(), uuid.New()))

	for _, r := range inventory {
		t.Run(r.feature+" "+r.pattern, func(t *testing.T) {
			rec, _ := play(t, mounted, r, nil)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("code = %d without a token, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// handlePattern matches the route patterns of a module.go.
var handlePattern = regexp.MustCompile(`r\.Handle\("([^"]+)"`)

// THE INVENTORY IS COMPARED TO THE SOURCES, and this is what keeps it from becoming a list of what
// somebody once wrote down.
//
// It reads the `r.Handle("...")` lines out of every module.go and fails on any route the table
// above does not carry. A route added tomorrow — a `DELETE`, a second write path, a new module —
// therefore cannot be merged without someone stating who it admits.
//
// It fails in BOTH directions on purpose. A route missing from the inventory is scoped by nobody;
// an inventory entry matching no route is a test that has been proving nothing since the route was
// renamed, which is the quieter of the two failures and the one this repository has already paid
// for twice.
//
// MUTATION: add a route to any module.go without adding it here → red.
func TestEveryMountedRouteIsInTheInventory(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range inventory {
		declared[r.feature+" "+r.pattern] = true
	}

	sources, err := filepath.Glob(filepath.Join("*", "module.go"))
	if err != nil {
		t.Fatalf("listing the modules: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("no module.go found — this check no longer measures anything")
	}

	mounted := map[string]bool{}
	for _, path := range sources {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		feature := filepath.Dir(path)
		for _, m := range handlePattern.FindAllStringSubmatch(string(raw), -1) {
			mounted[feature+" "+m[1]] = true
		}
	}
	if len(mounted) == 0 {
		t.Fatal("no r.Handle( found in any module.go — the pattern of this check has drifted")
	}

	for key := range mounted {
		if !declared[key] {
			t.Errorf("route %q is mounted but carries no scope in the inventory — nobody has said "+
				"which principal it admits", key)
		}
	}
	for key := range declared {
		if !mounted[key] {
			t.Errorf("inventory entry %q matches no mounted route — it has been proving nothing "+
				"since that route was renamed or removed", key)
		}
	}

	if t.Failed() {
		keys := make([]string, 0, len(mounted))
		for k := range mounted {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Logf("routes actually mounted:\n  %s", strings.Join(keys, "\n  "))
	}
}

// authtest is imported for its side-effect-free helpers only; this reference keeps the linter from
// flagging the alias when the build tags change.
var _ = auth.ScopeProject
