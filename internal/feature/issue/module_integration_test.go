package issue_test

// T1 — THE INDISTINGUISHABLE REFUSAL, GUARDED RATHER THAN WIRED.
//
// `docs/DESIGN-TRUST.md` § Le refus indiscernable (channels 1 and 2) states that the three refusals
// of `create_issue` — unknown key, key from another team, undeclared trust pair — are identical
// BYTE FOR BYTE. Until FLWL-45 that property was only true by WIRING: the refusal took the common
// error path, so it returned the right thing, but no test observed it. Mutation M3 — an `if` in
// `internal/feature/issue/handler/handler.go` returning `403` on the trust refusal alone — survived
// the whole suite.
//
// This file mounts the REAL API: a real `httptest.Server`, the real routes of `Routes()`, the real
// auth middleware on a real minted token, the real store on the dev database. The transport headers
// (`Content-Length`) are therefore the ones `net/http` actually emits, which an
// `httptest.ResponseRecorder` does not produce.
//
// WHAT IT DOES NOT GUARD. The MCP surface is the business of `cmd/flowlio/mcp_refusal_test.go`:
// this package does not know the MCP client, and bringing it in here would make a feature depend on
// a binary.

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/auth/authtest"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// refusalBody is the EXACT shape of a refusal, written out by hand.
//
// It is repeated here rather than derived from the handler: a constant shared with the code under
// test would make the test say "the response equals whatever the handler writes", that is, nothing.
const refusalBody = `{"error":"not found"}`

// refusalLength is the announced length of that body, also written out by hand.
const refusalLength = "21"

// project is a test project and its tenancy scope: exactly what a token carries.
type project struct {
	teamID uuid.UUID
	id     uuid.UUID
	key    string
}

// core is the minimal CoreServices a module needs: the auth service, and nothing else.
type core struct {
	authSvc auth.Service
}

func (c core) Auth() auth.Service { return c.authSvc }

// reply is what a caller observes of a response, and the whole of what it observes.
type reply struct {
	status        int
	contentType   string
	contentLength string
	body          []byte
}

func openDB(t *testing.T) *sql.DB {
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

	return db
}

// newTeam creates a throwaway team. Deleting it takes everything with it in cascade.
func newTeam(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()

	slug := "test-" + strings.ToLower(uuid.NewString()[:8])
	var teamID uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING id", slug, "Test team",
	).Scan(&teamID); err != nil {
		t.Fatalf("creating the team: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM teams WHERE id = $1", teamID); err != nil {
			t.Errorf("cleaning up team %s: %v", teamID, err)
		}
	})
	return teamID
}

// newProject creates a project in a team, through direct SQL: the issue feature depends on no other
// feature, not even in its tests.
func newProject(t *testing.T, db *sql.DB, teamID uuid.UUID, key string) project {
	t.Helper()

	var id uuid.UUID
	if err := db.QueryRow(
		"INSERT INTO projects (team_id, key, name) VALUES ($1, $2, $3) RETURNING id",
		teamID, key, "Project "+key,
	).Scan(&id); err != nil {
		t.Fatalf("creating project %s: %v", key, err)
	}
	return project{teamID: teamID, id: id, key: key}
}

// trust declares a trust between two projects. Laid down by hand in the test that needs it: hiding
// it inside newProject would mask the very guarantee this file exists to prove.
func trust(t *testing.T, db *sql.DB, a, b project) {
	t.Helper()

	if _, err := db.Exec(
		`INSERT INTO project_trust (team_id, low_project_id, high_project_id)
		 VALUES ($1, least($2::uuid, $3::uuid), greatest($2::uuid, $3::uuid))`,
		a.teamID, a.id, b.id,
	); err != nil {
		t.Fatalf("trust %s ↔ %s: %v", a.key, b.key, err)
	}
}

// serveAPI mounts the feature's real API, as seen by the calling project.
//
// The module is built through NewModule, not by hand: what is under test includes the
// store → service → handler wiring, since that is what decides the shape of the refusal today.
func serveAPI(t *testing.T, db *sql.DB, caller project) (*httptest.Server, authtest.Token) {
	t.Helper()

	tok := authtest.Project(t, caller.teamID, caller.id)
	mod := issue.NewModule(module.ModuleConfig{
		DB:    database.New(db),
		RawDB: db,
		Core:  core{authSvc: tok.Auth},
	})

	ts := httptest.NewServer(mod.Routes())
	t.Cleanup(ts.Close)
	return ts, tok
}

// createIssue plays a create_issue against the real API and returns EVERYTHING the caller observes.
func createIssue(t *testing.T, ts *httptest.Server, tok authtest.Token, toProject string) reply {
	t.Helper()

	payload := `{"to_project":"` + toProject +
		`","title":"has the /v2/cards contract changed?","body":"the endpoint no longer answers"}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("building the request towards %s: %v", toProject, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(tok.Authorize(req))
	if err != nil {
		t.Fatalf("call towards %s: %v", toProject, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body for %s: %v", toProject, err)
	}

	return reply{
		status:        resp.StatusCode,
		contentType:   resp.Header.Get("Content-Type"),
		contentLength: resp.Header.Get("Content-Length"),
		body:          body,
	}
}

// TestThreeRefusalsAreIndistinguishable compares the three refusals byte for byte.
//
// Two checks, and BOTH are needed:
//
//   - each against the expected shape written out by hand, otherwise a mutation making all three
//     identically WRONG (403 everywhere) satisfies the cross-comparison;
//   - the three against each other, because that is the statement of the guarantee itself — and it
//     is that comparison M3 breaks, by altering only one of the three.
//
// The WITNESS opening the test is not decorative: without it, three identical refusals could be so
// because nothing works at all, and the test would be measuring its own consistency.
func TestThreeRefusalsAreIndistinguishable(t *testing.T) {
	db := openDB(t)

	team := newTeam(t, db)
	elsewhere := newTeam(t, db)

	frnt := newProject(t, db, team, "FRNT")        // the caller
	sibling := newProject(t, db, team, "CORE")     // trusted sibling — the witness
	untrusted := newProject(t, db, team, "OPS")    // sibling, no trust declared
	foreign := newProject(t, db, elsewhere, "FAR") // project of another team

	trust(t, db, frnt, sibling)

	ts, tok := serveAPI(t, db, frnt)

	if got := createIssue(t, ts, tok, sibling.key); got.status != http.StatusCreated {
		t.Fatalf("witness: creation towards a trusted sibling = %d %s, want 201 — "+
			"three identical refusals prove nothing if the nominal path is broken",
			got.status, got.body)
	}

	refusals := []struct {
		name string
		got  reply
	}{
		{"unknown key", createIssue(t, ts, tok, "ZZZZ")},
		{"key from another team", createIssue(t, ts, tok, foreign.key)},
		{"undeclared pair", createIssue(t, ts, tok, untrusted.key)},
	}

	for _, refusal := range refusals {
		if refusal.got.status != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d", refusal.name, refusal.got.status, http.StatusNotFound)
		}
		if string(refusal.got.body) != refusalBody {
			t.Errorf("%s: body = %q, want %q", refusal.name, refusal.got.body, refusalBody)
		}
		if refusal.got.contentLength != refusalLength {
			t.Errorf("%s: Content-Length = %q, want %q",
				refusal.name, refusal.got.contentLength, refusalLength)
		}
	}

	reference := refusals[0]
	for _, refusal := range refusals[1:] {
		if refusal.got.status != reference.got.status {
			t.Errorf("%s: status %d ≠ %d of %s — the refusal is distinguishable",
				refusal.name, refusal.got.status, reference.got.status, reference.name)
		}
		if string(refusal.got.body) != string(reference.got.body) {
			t.Errorf("%s: body %q ≠ %q of %s — the refusal is distinguishable",
				refusal.name, refusal.got.body, reference.got.body, reference.name)
		}
		if refusal.got.contentLength != reference.got.contentLength {
			t.Errorf("%s: Content-Length %q ≠ %q of %s — the refusal is distinguishable by its size",
				refusal.name, refusal.got.contentLength, reference.got.contentLength, reference.name)
		}
		if refusal.got.contentType != reference.got.contentType {
			t.Errorf("%s: Content-Type %q ≠ %q of %s — the refusal is distinguishable by its header",
				refusal.name, refusal.got.contentType, reference.got.contentType, reference.name)
		}
	}
}
