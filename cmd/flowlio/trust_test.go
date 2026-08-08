package main

// What this file locks down: what the HUMAN READS.
//
// The three `trust` commands have no business logic — they compose a path, decode a response and
// print. The only thing that can be wrong in them is therefore the text, and that is exactly what
// nobody usually tests. Yet that text is the only surface where the truth of the graph is
// readable, and the first thing read by somebody whose agent just got `not found`.
//
// The server is an httptest: the paths and bodies actually emitted are checked, not assumed.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// capture diverts os.Stdout for the duration of fn and yields what was written to it.
//
// The commands print with fmt.Printf rather than to an injected io.Writer, like the six other
// commands of the binary. Diverting the descriptor keeps this test aligned on the shape of the
// rest of the CLI instead of forcing an injection on a single family of commands.
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	// defer, and not an inline restore: if fn panics, os.Stdout would stay plugged into a pipe
	// nobody reads and the WHOLE rest of the package would lose its output — a failure elsewhere
	// would then become undebuggable.
	defer func() { os.Stdout = saved }()

	runErr := fn()

	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the pipe: %v", err)
	}
	return string(out), runErr
}

// trustAPI mounts a test API. It records every request received — method, path AND body — which
// allows asserting on what is actually emitted, the part a re-reading does not see.
//
// The BODY is recorded because the JSON field names are a contract other repositories are written
// against: flowlio-core sends `{"from":...,"to":...}` to this same route. A rename here that nobody
// asserts is a silently broken integration on somebody else's machine.
type trustAPI struct {
	edges    []service.TrustEdge
	projects []service.Project
	decision service.TrustDecision
	status   int

	seen   []string
	bodies []string
}

func (a *trustAPI) serve(t *testing.T) *client.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.seen = append(a.seen, r.Method+" "+r.URL.RequestURI())
		raw, _ := io.ReadAll(r.Body)
		a.bodies = append(a.bodies, strings.TrimSpace(string(raw)))
		w.Header().Set("Content-Type", "application/json")

		if a.status != 0 && a.status >= http.StatusBadRequest {
			w.WriteHeader(a.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/api/workspace/projects"):
			_ = json.NewEncoder(w).Encode(a.projects)
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(a.edges)
		default:
			_ = json.NewEncoder(w).Encode(a.decision)
		}
	}))
	t.Cleanup(srv.Close)

	return client.New(srv.URL, "flw_test_secret")
}

func projectsNamed(keys ...string) []service.Project {
	out := make([]service.Project, 0, len(keys))
	for _, k := range keys {
		out = append(out, service.Project{Key: k, Name: "Project " + k})
	}
	return out
}

// An EMPTY graph is the case that counts: it is the default state of every team after the
// migration, hence the one a human lands in after an agent handed them `not found`.
//
// The output must name the projects AND give the exact command to type. Printing nothing would be
// technically correct and practically unusable.
func TestTrustListOnAnEmptyGraphSaysWhatToType(t *testing.T) {
	api := &trustAPI{projects: projectsNamed("CORE", "FRNT", "OPS")}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	for _, expected := range []string{
		"no trust declared",
		"CORE, FRNT, OPS",
		"open one direction",
		"flowlio trust allow CORE FRNT --team acme",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("the output does not contain %q:\n%s", expected, out)
		}
	}
}

// A team of a SINGLE project has no possible pair: offering to open one would be offering a
// command that cannot exist.
func TestTrustListOffersNothingWithoutAPossiblePair(t *testing.T) {
	api := &trustAPI{projects: projectsNamed("CORE")}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	if strings.Contains(out, "trust allow") {
		t.Errorf("a command is offered although no edge is possible:\n%s", out)
	}
	if !strings.Contains(out, "fewer than two projects") {
		t.Errorf("the output does not explain why there is nothing to do:\n%s", out)
	}
}

// The "X out of Y" count is what shows at a glance that something is left to open. Three projects
// make SIX possible directed edges — n(n−1), not n(n−1)/2 — and two declared leave four.
//
// MUTATION: leaving possibleEdges as n(n−1)/2 prints "out of 3" and this test goes red. That figure
// is the only place the CLI says how much of the graph is still closed, and halving it would tell a
// customer with three repos and every edge open that they had opened twice what exists.
func TestTrustListCountsThePossibleEdges(t *testing.T) {
	day := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	api := &trustAPI{
		projects: projectsNamed("CORE", "FRNT", "OPS"),
		edges: []service.TrustEdge{
			{From: "CORE", To: "FRNT", CreatedAt: day},
			{From: "CORE", To: "OPS", CreatedAt: day},
		},
	}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	for _, expected := range []string{
		"CORE → FRNT", "CORE → OPS", "2026-08-04", "2 directed edge(s) out of 6 possible",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("the output does not contain %q:\n%s", expected, out)
		}
	}
}

// THE TEST CARD 13 EXISTS FOR: `flowlio trust list` MUST SHOW THE DIRECTION of each edge.
//
// A list of pairs printed after migration 000013 would be a screen that stopped being true without
// anything failing — no test, no build, no lint. The customer would read "WEB and CORE are linked"
// off a database that says WEB may question CORE and CORE may not question WEB, and would only find
// out through an agent handed a `not found` nothing explains. That is the FLWL-78 failure mode, and
// this test is what makes it impossible to repeat.
//
// The assertion is in two halves, and BOTH are needed:
//
//   - each graph must print its OWN arrow, so a renderer printing a fixed string cannot pass;
//   - the two graphs must not print the SAME THING, which is what a pair renderer does — `WEB ↔
//     CORE` for both. That comparison is the one that catches a regression to a symmetric display,
//     and no `Contains` assertion can catch it on its own.
//
// MUTATION: printing `%s ↔ %s` instead of `%s → %s` in trustList fails the first half; sorting the
// two ends before printing fails the second.
func TestTrustListShowsTheDirectionOfEachEdge(t *testing.T) {
	day := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		edge service.TrustEdge
		want string
	}{
		{"WEB may question CORE", service.TrustEdge{From: "WEB", To: "CORE", CreatedAt: day}, "WEB → CORE"},
		{"CORE may question WEB", service.TrustEdge{From: "CORE", To: "WEB", CreatedAt: day}, "CORE → WEB"},
	}

	printed := make(map[string]string, len(cases))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{
				projects: projectsNamed("CORE", "WEB"),
				edges:    []service.TrustEdge{c.edge},
			}

			out, err := capture(t, func() error {
				return trustList(context.Background(), api.serve(t), "acme")
			})
			if err != nil {
				t.Fatalf("trustList: %v", err)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("the output does not show %q — the direction is not on screen:\n%s", c.want, out)
			}
			printed[c.name] = out
		})
	}

	if printed[cases[0].name] == printed[cases[1].name] {
		t.Errorf("the two opposite graphs print IDENTICALLY — the screen has stopped telling "+
			"`WEB → CORE` from `CORE → WEB`:\n%s", printed[cases[0].name])
	}
}

// The header line above the edges tells the reader HOW to read the arrow. Without it, `WEB → CORE`
// is a shape on screen, and the two plausible readings — "WEB may ask CORE" and "questions flow
// towards WEB" — are opposites.
func TestTrustListSaysHowToReadTheArrow(t *testing.T) {
	api := &trustAPI{
		projects: projectsNamed("CORE", "WEB"),
		edges:    []service.TrustEdge{{From: "WEB", To: "CORE", CreatedAt: time.Now()}},
	}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}
	if !strings.Contains(out, "<from> may open a question at <to>") {
		t.Errorf("the output never says which way to read the arrow:\n%s", out)
	}
}

// allow and deny say what CHANGED. A replay is not an error, but letting the human believe they
// just changed something would be one.
func TestTrustAnnouncesWhatChanged(t *testing.T) {
	cases := []struct {
		name      string
		changed   bool
		run       func(*client.Client) error
		expected  string
		forbidden string
	}{
		{
			"allow, first time", true,
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"CORE can now raise issues at FRNT", "already allowed",
		},
		{
			"allow, replay", false,
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"already allowed, nothing to do", "can now",
		},
		{
			"deny, first time", true,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"CORE can no longer open an issue at FRNT", "nothing to withdraw",
		},
		{
			"deny, replay", false,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"no trust declared, nothing to withdraw", "trust withdrawn",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{decision: service.TrustDecision{From: "CORE", To: "FRNT", Changed: c.changed}}

			out, err := capture(t, func() error { return c.run(api.serve(t)) })
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if !strings.Contains(out, c.expected) {
				t.Errorf("the output does not contain %q:\n%s", c.expected, out)
			}
			if strings.Contains(out, c.forbidden) {
				t.Errorf("the output contains %q, which is the other case's message:\n%s", c.forbidden, out)
			}
		})
	}
}

// THE THREE MOST IMPORTANT LINES OF THE COMMAND.
//
// `trust deny` is not a containment tool: the threads already open stay answerable, with no time
// bound. Without these lines, somebody who has just discovered a compromised repo would believe
// they had cut it off. The message therefore names explicitly the tool that really cuts.
//
// Since card 11 there is a THIRD thing the human has to be told: cutting `CORE → OPS` leaves
// `OPS → CORE` standing. Under a directed graph, "I cut that repo off" is one command short of
// true, and the same person who would have believed `deny` contained an incident will now believe
// one `deny` closed a channel that is still half open.
//
// MUTATION: removing either of the two Println of trustDeny, or the "other direction" line, makes
// this test fall over.
func TestTrustDenyNamesTheRealCircuitBreaker(t *testing.T) {
	api := &trustAPI{decision: service.TrustDecision{From: "CORE", To: "OPS", Changed: true}}

	out, err := capture(t, func() error {
		return trustDeny(context.Background(), api.serve(t), "acme", "CORE", "OPS")
	})
	if err != nil {
		t.Fatalf("trustDeny: %v", err)
	}

	for _, expected := range []string{
		"The other direction is untouched: cut it with flowlio trust deny OPS CORE --team acme",
		"Threads already open stay readable and answerable.",
		"flowlio token revoke",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("the output does not say %q — the human will believe they contained the repo:\n%s", expected, out)
		}
	}
}

// The paths actually emitted. This is the part a re-reading does not see: a key forgotten in the
// DELETE URL, or a lost ?team=, only shows up at run time.
func TestTrustEmitsTheRightPaths(t *testing.T) {
	cases := []struct {
		name     string
		run      func(*client.Client) error
		expected string
	}{
		{
			"allow",
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"POST /api/workspace/trust?team=acme",
		},
		{
			"deny",
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"DELETE /api/workspace/trust/CORE/FRNT?team=acme",
		},
		{
			"deny without a team (project token)",
			func(c *client.Client) error { return trustDeny(context.Background(), c, "", "CORE", "FRNT") },
			"DELETE /api/workspace/trust/CORE/FRNT",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{decision: service.TrustDecision{From: "CORE", To: "FRNT"}}
			cl := api.serve(t)

			if _, err := capture(t, func() error { return c.run(cl) }); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(api.seen) == 0 || api.seen[0] != c.expected {
				t.Errorf("path emitted = %v, expected %q", api.seen, c.expected)
			}
		})
	}
}

// A bare 403, on a command run right after being told to export FLOWLIO_TOKEN, is the worst
// message this product can yield: the human has just followed the instructions and gets rejected.
//
// MUTATION: removing explainAdminToken from runTrust makes this test fall over.
func TestA403SaysWhichTokenToUse(t *testing.T) {
	api := &trustAPI{status: http.StatusForbidden}
	cl := api.serve(t)

	_, err := capture(t, func() error {
		return explainAdminToken(trustList(context.Background(), cl, "acme"))
	})
	if err == nil {
		t.Fatal("no error although the API returned 403")
	}

	for _, expected := range []string{"ADMIN token", "credentials.json", "FLOWLIO_TOKEN"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not contain %q:\n%v", expected, err)
		}
	}
}

// An error that is NOT a 403 goes through explainAdminToken without being dressed up. Without this
// case, a helper rewriting everything into "wrong token" would look correct.
func TestAnotherErrorIsNotDressedUp(t *testing.T) {
	api := &trustAPI{status: http.StatusNotFound}
	cl := api.serve(t)

	_, err := capture(t, func() error {
		return explainAdminToken(trustList(context.Background(), cl, "acme"))
	})
	if err == nil {
		t.Fatal("no error although the API returned 404")
	}
	if strings.Contains(err.Error(), "ADMIN token") {
		t.Errorf("a 404 is presented as a token problem:\n%v", err)
	}
}

// n(n−1), twice the number of pairs: each couple is two independent declarations since 000013.
func TestPossibleEdges(t *testing.T) {
	for _, c := range []struct{ n, want int }{{0, 0}, {1, 0}, {2, 2}, {3, 6}, {4, 12}, {30, 870}} {
		if got := possibleEdges(c.n); got != c.want {
			t.Errorf("possibleEdges(%d) = %d, expected %d", c.n, got, c.want)
		}
	}
}

// THE WIRE CONTRACT. `POST /api/workspace/trust` carries `{"from":..., "to":...}`, and flowlio-core
// is written against exactly those two names. Renaming a JSON tag in service.TrustPairInput
// compiles, passes every other test in this package, and breaks an integration nobody in this
// repository would see.
//
// The body is asserted as a whole, not field by field: an EXTRA field would be just as much of a
// change, and `Contains` would not notice it.
func TestTrustAllowSendsTheContractedFieldNames(t *testing.T) {
	api := &trustAPI{decision: service.TrustDecision{From: "WEB", To: "CORE", Changed: true}}
	cl := api.serve(t)

	if _, err := capture(t, func() error {
		return trustAllow(context.Background(), cl, "acme", "WEB", "CORE")
	}); err != nil {
		t.Fatalf("trustAllow: %v", err)
	}

	if len(api.bodies) == 0 {
		t.Fatal("no request reached the API")
	}
	const want = `{"from":"WEB","to":"CORE"}`
	if api.bodies[0] != want {
		t.Errorf("body sent = %s, want %s", api.bodies[0], want)
	}
}
