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

// trustAPI mounts a test API. It records every request received, which allows asserting on the
// PATH actually emitted — the part a re-reading does not see.
type trustAPI struct {
	edges    []service.TrustEdge
	projects []service.Project
	decision service.TrustDecision
	status   int

	seen []string
}

func (a *trustAPI) serve(t *testing.T) *client.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.seen = append(a.seen, r.Method+" "+r.URL.RequestURI())
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
		t.Errorf("a command is offered although no pair is possible:\n%s", out)
	}
	if !strings.Contains(out, "fewer than two projects") {
		t.Errorf("the output does not explain why there is nothing to do:\n%s", out)
	}
}

// The "X out of Y" count is what shows at a glance that something is left to open. Three projects
// make three possible pairs; two declared leave one.
func TestTrustListCountsThePossiblePairs(t *testing.T) {
	day := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	api := &trustAPI{
		projects: projectsNamed("CORE", "FRNT", "OPS"),
		edges: []service.TrustEdge{
			{First: "CORE", Second: "FRNT", CreatedAt: day},
			{First: "CORE", Second: "OPS", CreatedAt: day},
		},
	}

	out, err := capture(t, func() error {
		return trustList(context.Background(), api.serve(t), "acme")
	})
	if err != nil {
		t.Fatalf("trustList: %v", err)
	}

	for _, expected := range []string{"CORE ↔ FRNT", "CORE ↔ OPS", "2026-08-04", "2 pair(s) out of 3 possible"} {
		if !strings.Contains(out, expected) {
			t.Errorf("the output does not contain %q:\n%s", expected, out)
		}
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
			"can now raise issues to each other", "already allowed",
		},
		{
			"allow, replay", false,
			func(c *client.Client) error { return trustAllow(context.Background(), c, "acme", "CORE", "FRNT") },
			"already allowed, nothing to do", "can now",
		},
		{
			"deny, first time", true,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"trust withdrawn", "nothing to withdraw",
		},
		{
			"deny, replay", false,
			func(c *client.Client) error { return trustDeny(context.Background(), c, "acme", "CORE", "FRNT") },
			"no trust declared, nothing to withdraw", "trust withdrawn",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "FRNT", Changed: c.changed}}

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
// MUTATION: removing either of the two Println of trustDeny makes this test fall over.
func TestTrustDenyNamesTheRealCircuitBreaker(t *testing.T) {
	api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "OPS", Changed: true}}

	out, err := capture(t, func() error {
		return trustDeny(context.Background(), api.serve(t), "acme", "CORE", "OPS")
	})
	if err != nil {
		t.Fatalf("trustDeny: %v", err)
	}

	for _, expected := range []string{
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
			api := &trustAPI{decision: service.TrustDecision{First: "CORE", Second: "FRNT"}}
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

func TestPossiblePairs(t *testing.T) {
	for _, c := range []struct{ n, want int }{{0, 0}, {1, 0}, {2, 1}, {3, 3}, {4, 6}, {30, 435}} {
		if got := possiblePairs(c.n); got != c.want {
			t.Errorf("possiblePairs(%d) = %d, expected %d", c.n, got, c.want)
		}
	}
}
