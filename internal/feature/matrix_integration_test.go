package feature_test

// GUARANTEE 2 OF THE TABLE IN docs/DESIGN-TUI.md — the scope matrix, across every module.
//
// WHAT THIS FILE GUARDS AND NOBODY ELSE GUARDS. Every module checks ITS scope in its own
// `module_test.go`. None of those files can see that a scope was aligned on its neighbour's by
// copy-paste: an `overview` moved under `Middleware` stays green at `task`, and conversely. The
// matrix is the only assertion that puts every surface against the others.
//
// WHY IT IS AN INTEGRATION TEST. The 401 and 403 cells never reach a store — they could hold on
// doubles. The 200 cells cannot: they cross the handler, the service and the query all the way to
// Postgres. Without them the matrix proves nothing, because a middleware rejecting EVERYBODY
// would still pass half of it. The 200 column is the positive control of the other two, and it is
// what makes `FLOWLIO_TEST_DATABASE_URL` mandatory.
//
// THE MODULES ARE MOUNTED THROUGH NewModule, NOT BY HAND. Rewiring the routes here would prove
// the scope of the test's own table. What is under test is what `buildModules()` builds.
//
// THE STATUS ALONE IS NOT ENOUGH, AND TWO VERSIONS OF THIS FILE LEARNED IT THE HARD WAY. Every
// module rejects TWICE: its route carries a guard, and its handler repeats it. Removing the route
// guard therefore leaves the status unchanged — the second layer catches it — and a matrix that
// only observes the status stays green on exactly the regression it exists to catch.
//
//	version 1 (status alone)      : survived `overview` moved from AdminOnly to Middleware
//	version 2 (+ auth layer)      : still survived `requireProjectScope` accepting the admin
//	version 3 (+ handler guard)   : both mutations fall over
//
// Every cell therefore carries TWO observations on top of the status:
//
//  1. the layer that must reject, read on `WWW-Authenticate: Bearer` — only `deny()` of
//     `internal/core/auth` sets it;
//  2. the silence of the handler guards, read on the log — see `handlerGuards`. In every correct
//     case, a rejected principal is rejected BEFORE entering the module.
//
// The second is the only one that tells `requireProjectScope` apart from `Handler.scope`: their
// HTTP responses are identical, body included.
//
// THE GUARD PHRASES ARE SUBSTRINGS OF THE HANDLER LOGS, AND NOTHING ELSE TIED THEM TOGETHER.
// Between lots 6a and 6c of FLWL-49 the handler logs were translated and this list was not: every
// `Contains` stopped matching, so the second observation reported nothing and the matrix stayed
// GREEN while it had stopped guarding anything. `TestHandlerGuardsStillExist` closes that gap —
// it is the mutation test of the guard list itself.

import (
	"bytes"
	"database/sql"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	_ "github.com/jackc/pgx/v5/stdlib"
)

// core is the minimal CoreServices the modules expect: the auth service of the token the current
// case presents, and nothing else.
type core struct{ svc auth.Service }

func (c core) Auth() auth.Service { return c.svc }

// expect is the expected outcome of a cell: the status, and the layer that must pronounce it.
//
// byAuth is true when the refusal must come from `internal/core/auth` — `Middleware` or
// `AdminOnly`. It is read on the `WWW-Authenticate: Bearer` header, which only `deny()` sets.
// Without this field, a cell covered by a second line of defence stays green although its route
// lost its guard.
type expect struct {
	status int
	byAuth bool
}

var (
	// allowed — the request reaches the store and yields its result.
	allowed = expect{status: http.StatusOK}
	// deniedByAuth — refusal pronounced by the auth layer, before entering the module.
	deniedByAuth = expect{status: http.StatusForbidden, byAuth: true}
	// deniedByModule — refusal pronounced by `requireProjectScope`, inside the module.
	deniedByModule = expect{status: http.StatusForbidden}
	// deniedWithoutToken — no header presented: the auth layer rejects with a 401.
	deniedWithoutToken = expect{status: http.StatusUnauthorized, byAuth: true}
)

// surface is one cell of the matrix: a route representative of a module, and the outcome expected
// for each of the two authenticated principals. An absent principal expects 401 everywhere, which
// needs no column of its own.
//
// The table is written BY HAND. Deriving it from the muxes would make it say what the code does
// rather than what it must do, and that is exactly the mistake the matrix exists to catch.
type surface struct {
	feature string
	path    string // {team} is replaced by the slug of the fixture's team
	project expect
	admin   expect
}

// matrix enumerates the eight surfaces representative of the seven modules.
//
// `workspace` carries TWO of them because its scope is mixed, and "partial" is not a status:
// `/projects` is open to any authenticated token, `/teams` is reserved to the admin. A single
// entry would have left half of that module outside the matrix.
var matrix = []surface{
	{workspace.Key, "/projects?team={team}", allowed, allowed},
	{workspace.Key, "/teams", deniedByAuth, allowed},
	{task.Key, "/", allowed, deniedByModule},
	{issue.Key, "/", allowed, deniedByModule},
	{inbox.Key, "/", allowed, deniedByModule},
	{overview.Key, "/?team={team}", deniedByAuth, allowed},
	// `memory` is project-scoped in BOTH directions, writes included: it is the one module whose
	// content is a repository's own reasoning, and an admin reading it is the third party M5
	// refused. The positive control is a real read against Postgres, empty but scoped.
	{memory.Key, "/", allowed, deniedByModule},
	// `ref` cites a CONCRETE reference, and the fixture lays down task CORE-1 for it. A
	// non-existent reference would yield 404: the cell would lose its positive control, and a
	// scope rejecting everybody would still pass it.
	{ref.Key, "/CORE/1", allowed, deniedByModule},
}

// fixture carries the team and the project the test's tokens are pinned to.
type fixture struct {
	teamID    uuid.UUID
	slug      string
	projectID uuid.UUID
}

// newFixture opens the test database and creates a throwaway team and its project in it.
//
// Deleting the team takes the project with it, by cascade — the cleanup is therefore one line,
// and no test leaves behind anything that could make the next one pass.
func newFixture(t *testing.T) (*sql.DB, fixture) {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL not set — integration test skipped")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("database unreachable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	f := fixture{slug: "matrix-" + strings.ToLower(uuid.NewString()[:8])}
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", f.slug, "Matrix team",
	).Scan(&f.teamID); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", f.teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", f.teamID, err)
		}
	})

	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		f.teamID, "CORE", "Project CORE",
	).Scan(&f.projectID); err != nil {
		t.Fatalf("creating the project: %v", err)
	}

	// A task for the `ref` cell: it resolves a reference, so it needs one that exists for its 200
	// to prove anything. The other cells read lists and see it as nothing more than one extra row.
	if _, err := db.Exec(
		"INSERT INTO tasks (team_id, project_id, number, title) VALUES ($1, $2, 1, $3)",
		f.teamID, f.projectID, "matrix task",
	); err != nil {
		t.Fatalf("creating task CORE-1: %v", err)
	}

	return db, f
}

// routers mounts the seven modules with their real stores, on the auth service of the given token.
//
// The `authtest.Store` double knows only one token: every principal therefore has its own set of
// modules. That is slower than a single mounting, and it is what guarantees a case cannot present
// another one's token.
func routers(t *testing.T, db *sql.DB, tok authtest.Token) map[string]http.Handler {
	t.Helper()

	registry := coreregistry.NewRegistry()
	cfg := module.ModuleConfig{
		DB:       database.New(db),
		RawDB:    db,
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
		// The registry is filled AS IN PRODUCTION: `ref` composes task and issue through it, and a
		// mounting that forgot it would make its cell answer 500 instead of proving its scope.
		registry.Register(m.Key(), m)
		mounted[m.Key()] = m.Routes()
	}
	return mounted
}

// call plays a GET on the route of a cell and yields the whole response.
//
// The recorder is returned rather than the status alone: the layer that rejected is read in the
// headers, and that is half of what every cell asserts.
//
// authorize is nil for the absent principal: it is the only difference between the 401 case and
// the other two, and it is carried by the request, not by a particular mounting.
func call(t *testing.T, mounted map[string]http.Handler, s surface, slug string, authorize func(*http.Request) *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()

	router, ok := mounted[s.feature]
	if !ok {
		t.Fatalf("no module mounted under key %q — the matrix cites a feature that no longer exists", s.feature)
	}

	req := httptest.NewRequest(http.MethodGet, strings.ReplaceAll(s.path, "{team}", slug), nil)
	if authorize != nil {
		req = authorize(req)
	}

	// The log is diverted for the duration of the call: the handler guards write to it, and it is
	// the only place where they differ from a refusal pronounced earlier — their HTTP responses
	// are identical, body included. No test of this file is parallel, so diverting a package-level
	// variable cannot spill over onto another case.
	var journal bytes.Buffer
	log.SetOutput(&journal)
	defer log.SetOutput(os.Stderr)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, journal.String()
}

// handlerGuards enumerates the phrases the handler guards write when they reject a principal.
//
// NO CELL OF THE MATRIX MUST MAKE THEM SPEAK. In every correct case, a rejected principal is
// rejected before entering the module; a guard that speaks proves it went further than it should
// have, and that the 403 obtained is right for the wrong reason.
//
// These are the only guard lines — the other `log.Printf` of the handlers report encoding or write
// failures, which say nothing about a scope.
//
// EVERY PHRASE MUST STILL EXIST IN A HANDLER SOURCE, and `TestHandlerGuardsStillExist` is what
// checks it. A phrase that matches nothing turns this whole observation into a no-op, silently.
var handlerGuards = []string{
	"route without a project token",
	"non-admin principal",
	"route without auth middleware",
}

// TestHandlerGuardsStillExist is the mutation test of the guard list itself.
//
// It runs in `make check`, without a database, deliberately: the flaw it catches is a rewording,
// and a rewording must not wait for `make test-integration` to be noticed.
//
// It reads the handler SOURCES rather than provoking the guards: provoking them would need the
// very mounting the matrix builds, hence a database, and would prove the phrase of the one guard
// reached rather than of all three.
//
// MUTATION: reword a `log.Printf` of a handler guard without following it here → this test goes
// red. It is exactly what happened between lots 6a and 6c of FLWL-49, and stayed unseen.
func TestHandlerGuardsStillExist(t *testing.T) {
	sources, err := filepath.Glob(filepath.Join("*", "handler", "*.go"))
	if err != nil {
		t.Fatalf("listing the handler sources: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("no handler source found — the check no longer measures anything")
	}

	var corpus strings.Builder
	for _, path := range sources {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		corpus.Write(raw)
	}

	for _, guard := range handlerGuards {
		if !strings.Contains(corpus.String(), guard) {
			t.Errorf("the guard phrase %q no longer appears in any handler — every Contains of "+
				"the matrix on it is a no-op, and the second observation of each cell guards "+
				"nothing", guard)
		}
	}
}

// assertCase compares the obtained response to the expected outcome: the status, then the layer
// that pronounced it.
//
// The two assertions are separate and both reported. A refusal sliding from one layer to the
// other keeps its status: that is exactly the regression the second line catches, and it would be
// invisible if the test stopped at the first gap.
func assertCase(t *testing.T, rec *httptest.ResponseRecorder, journal string, want expect, s surface, principal string) {
	t.Helper()

	// The handler guard first, before even the status: when it speaks, the status is right and
	// the reason is not, and that is the most useful diagnosis to report.
	for _, guard := range handlerGuards {
		if strings.Contains(journal, guard) {
			t.Errorf("the handler guard spoke (%q) for %s on %s%s — the principal REACHED the handler, "+
				"although an earlier layer should have stopped it. Status %d obtained for the wrong reason.",
				guard, principal, s.feature, s.path, rec.Code)
			return
		}
	}

	if rec.Code != want.status {
		t.Errorf("status = %d, expected %d for %s on %s%s",
			rec.Code, want.status, principal, s.feature, s.path)
		return
	}
	if want.status == http.StatusOK {
		return
	}

	// `deny()` of internal/core/auth is the only one to set this header. Its absence on a refusal
	// expected from the auth layer means the route lost its guard and that a deeper defence caught
	// the fall.
	byAuth := rec.Header().Get("WWW-Authenticate") == "Bearer"
	if byAuth == want.byAuth {
		return
	}

	rejecter := map[bool]string{true: "the auth layer (Middleware/AdminOnly)", false: "the module itself"}
	t.Errorf("refusal pronounced by %s, expected from %s for %s on %s%s — status %d is right, the layer is not",
		rejecter[byAuth], rejecter[want.byAuth], principal, s.feature, s.path, rec.Code)
}

// TestScopeMatrixProjectToken — an agent token reads its backlog and its queue, sees no
// administration surface, and does not reach overview.
//
// MUTATION PLAYED: in overview/module.go, `admin := m.auth.AdminOnly` replaced with
// `m.auth.Middleware` → the overview cell stays at 403, but pronounced by the handler guard. Red
// on "the handler guard spoke ("non-admin principal")".
//
// MUTATION PLAYED: in workspace/module.go, `authed(...)` removed from `GET /projects` → the cell
// falls to 401, red on the status.
func TestScopeMatrixProjectToken(t *testing.T) {
	db, f := newFixture(t)
	tok := authtest.Project(t, f.teamID, f.projectID)
	mounted := routers(t, db, tok)

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, tok.Authorize)
			assertCase(t, rec, journal, s.project, s, "a project token")
		})
	}
}

// TestScopeMatrixAdminToken — an admin token administers and reads the whole team, but neither
// writes nor reads in an agent's place.
//
// An admin rejected on `task`, `issue` and `inbox` is not a limitation to fix: those surfaces
// answer ON BEHALF of a project, and a principal without a project has none to give.
//
// MUTATION PLAYED: in task/module.go, `principal.Scope != auth.ScopeProject` replaced with
// `principal.Scope != auth.ScopeProject && !principal.IsAdmin()` → the task cell stays at 403,
// pronounced by `Handler.scope` instead of `requireProjectScope`. The two responses are identical
// down to the bit: red on "the handler guard spoke ("route without a project token")", and on
// nothing else. That is the mutation that cost two versions of this file.
//
// MUTATION PLAYED: in inbox/module.go, `Middleware(requireProjectScope(...))` replaced with
// `AdminOnly(...)` — the copy-paste alignment on the neighbouring module, the very flaw this
// matrix exists to catch → the inbox cell falls to 403 for a project token, red on the status in
// TestScopeMatrixProjectToken.
func TestScopeMatrixAdminToken(t *testing.T) {
	db, f := newFixture(t)
	tok := authtest.Admin(t, uuid.Nil)
	mounted := routers(t, db, tok)

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, tok.Authorize)
			assertCase(t, rec, journal, s.admin, s, "an admin token")
		})
	}
}

// TestScopeMatrixWithoutToken — no surface of the product is readable without a token.
//
// It is the line that must stay true when a route is added: a handler mounted outside the auth
// middleware shows up in no scope `module_test.go`, since those start from the principals they
// present.
//
// MUTATION PLAYED: in workspace/module.go, `authed(...)` removed from the `GET /projects` route →
// the request without a header reaches the handler, whose guard rejects with a 401. The expected
// status is 401 all the same: only the handler guard betrays the regression, and this test reads
// it.
func TestScopeMatrixWithoutToken(t *testing.T) {
	db, f := newFixture(t)
	mounted := routers(t, db, authtest.Admin(t, uuid.Nil))

	for _, s := range matrix {
		t.Run(s.feature+s.path, func(t *testing.T) {
			rec, journal := call(t, mounted, s, f.slug, nil)
			assertCase(t, rec, journal, deniedWithoutToken, s, "no Authorization header")
		})
	}
}
