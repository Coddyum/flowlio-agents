package main

// This file tests the WIRING of both screens — which URL actually leaves, and what the command
// does with the response. The rendering itself is pinned against literal strings in render_test.go.
//
// EVERY ASSERTION IS STATED IN POSITIVE FORM: each case pins the EXACT LIST of requests emitted,
// never the absence of one. A `must not contain` stays green when the command stops calling
// anything at all — it carves the assumption in stone instead of checking it.
//
// What these tests do NOT prove, and where it is proven: that the server really refuses a project
// token. That is held by `overview/module_test.go`, which mounts the routes behind `AdminOnly`.
// Here the 403 is postulated, to check what the CLI makes of it.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	overviewservice "github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
)

// fakeAPI records the URLs requested and answers whatever the test case decides.
type fakeAPI struct {
	mu   sync.Mutex
	urls []string
}

// start brings the server up and points the CLI at it through the environment, which wins over the
// local credentials file: the test never touches the machine's real credentials.
func (f *fakeAPI) start(t *testing.T, route func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.urls = append(f.urls, r.URL.String())
		f.mu.Unlock()
		route(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("FLOWLIO_API_URL", srv.URL)
	t.Setenv("FLOWLIO_TOKEN", "test-token")
}

// requested returns the URLs requested, in order.
func (f *fakeAPI) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.urls)
}

// writeTestJSON answers with a JSON test body.
func writeTestJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encoding the test response: %v", err)
	}
}

// captureStdout diverts standard output for the duration of one call. Both screens write to
// os.Stdout: without this diversion, "the command prints nothing" would not be checkable.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the pipe: %v", err)
	}

	saved := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = saved

	if err := w.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	return string(out), runErr
}

// healthyState is the state of a team carrying no debt.
func healthyState() overviewservice.TeamState {
	return overviewservice.TeamState{
		GeneratedAt: generatedAt,
		Projects:    []overviewservice.ProjectLine{{Key: "CORE"}, {Key: "WEB"}},
	}
}

// TestWatchIsSilentOnAHealthyTeam is the criterion of the task, end to end: nothing on standard
// output, no error — therefore exit status 0.
//
// It also pins the URL actually requested. The overview module REQUIRES `?team=<slug>` and answers
// 400 without it: the resolution convenience lives here, and if it gets the parameter name wrong,
// this list is what says so, rather than a 400 discovered in use.
func TestWatchIsSilentOnAHealthyTeam(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace/whoami":
			writeTestJSON(t, w, map[string]string{"scope": "admin", "team": "acme"})
		case "/api/overview/":
			writeTestJSON(t, w, healthyState())
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out, err := captureStdout(t, func() error { return runWatch(context.Background(), nil) })
	if err != nil {
		t.Fatalf("watch on a healthy team must succeed: %v", err)
	}
	if out != "" {
		t.Fatalf("watch on a healthy team must stay mute, got:\n%q", out)
	}

	want := []string{"/api/workspace/whoami", "/api/overview/?team=acme"}
	if got := api.requested(); !slices.Equal(got, want) {
		t.Fatalf("requests emitted %q, want %q", got, want)
	}
}

// TestWatchRefusesANonAdminTokenWithExitCodeTwo: the second criterion of the task. The 2 has to
// travel up to main, so it must cross runWatch without being flattened into an ordinary error.
func TestWatchRefusesANonAdminTokenWithExitCodeTwo(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace/whoami":
			writeTestJSON(t, w, map[string]string{"scope": "project", "team": "acme", "project": "WEB"})
		case "/api/overview/":
			w.WriteHeader(http.StatusForbidden)
			writeTestJSON(t, w, map[string]string{"error": "forbidden"})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := runWatch(context.Background(), nil)

	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("the refusal must carry its own exit status, got %v", err)
	}
	if exit.code != 2 {
		t.Fatalf("refusal exit status = %d, want 2", exit.code)
	}
}

// TestWatchHonoursTheTeamFlagWithoutAskingWhoami: --team wins and short-circuits the resolution.
// The exact request list is what proves it — one extra whoami would mean the flag is only a hint.
func TestWatchHonoursTheTeamFlagWithoutAskingWhoami(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/overview/" {
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeTestJSON(t, w, healthyState())
	})

	if _, err := captureStdout(t, func() error {
		return runWatch(context.Background(), []string{"--team", "chosen"})
	}); err != nil {
		t.Fatalf("watch --team: %v", err)
	}

	want := []string{"/api/overview/?team=chosen"}
	if got := api.requested(); !slices.Equal(got, want) {
		t.Fatalf("requests emitted %q, want %q", got, want)
	}
}

// TestWatchFallsBackToTheOnlyTeamOfTheInstance covers the development machine: a global admin
// token, a single team, no reason to make anyone type --team.
func TestWatchFallsBackToTheOnlyTeamOfTheInstance(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace/whoami":
			writeTestJSON(t, w, map[string]string{"scope": "admin"})
		case "/api/workspace/teams":
			writeTestJSON(t, w, []map[string]string{{"slug": "solo", "name": "Solo"}})
		case "/api/overview/":
			writeTestJSON(t, w, healthyState())
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if _, err := captureStdout(t, func() error { return runWatch(context.Background(), nil) }); err != nil {
		t.Fatalf("watch with a single team: %v", err)
	}

	want := []string{"/api/workspace/whoami", "/api/workspace/teams", "/api/overview/?team=solo"}
	if got := api.requested(); !slices.Equal(got, want) {
		t.Fatalf("requests emitted %q, want %q", got, want)
	}
}

// TestWatchRefusesToGuessBetweenTwoTeams: guessing would show one team's state to another team's
// supervisor, without saying a word. The request list pins that the command stops BEFORE hitting
// the supervision surface.
func TestWatchRefusesToGuessBetweenTwoTeams(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace/whoami":
			writeTestJSON(t, w, map[string]string{"scope": "admin"})
		case "/api/workspace/teams":
			writeTestJSON(t, w, []map[string]string{
				{"slug": "acme", "name": "Acme"},
				{"slug": "omiros", "name": "Omiros"},
			})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := runWatch(context.Background(), nil)
	if err == nil {
		t.Fatal("two teams and no --team: the command must refuse to choose")
	}
	if !strings.Contains(err.Error(), "--team") {
		t.Fatalf("the message must say what to type, got %q", err)
	}

	want := []string{"/api/workspace/whoami", "/api/workspace/teams"}
	if got := api.requested(); !slices.Equal(got, want) {
		t.Fatalf("requests emitted %q, want %q", got, want)
	}
}

// TestShowAsksForTheReferenceItWasGiven: the reference typed is exactly the one shown in the REF
// column of watch, and it travels to the API path untransformed.
func TestShowAsksForTheReferenceItWasGiven(t *testing.T) {
	api := &fakeAPI{}
	api.start(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace/whoami":
			writeTestJSON(t, w, map[string]string{"scope": "admin", "team": "acme"})
		case "/api/overview/refs/CORE/41":
			writeTestJSON(t, w, overviewservice.RefDetail{
				Kind: "issue", Ref: "CORE-41", From: "WEB", State: "open",
				Title: "Schema migration", CreatedAt: generatedAt, UpdatedAt: generatedAt,
			})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out, err := captureStdout(t, func() error {
		return runShow(context.Background(), []string{"CORE-41"})
	})
	if err != nil {
		t.Fatalf("show CORE-41: %v", err)
	}
	if !strings.HasPrefix(out, "CORE-41 — issue open\nSchema migration\n") {
		t.Fatalf("unexpected detail header:\n%s", out)
	}

	want := []string{"/api/workspace/whoami", "/api/overview/refs/CORE/41?team=acme"}
	if got := api.requested(); !slices.Equal(got, want) {
		t.Fatalf("requests emitted %q, want %q", got, want)
	}
}
