package main

// GUARANTEE 21 OF THE TABLE IN docs/DESIGN-TUI.md § "Garanties de sécurité".
//
// What this file locks down: NO MCP TOOL TOUCHES `/api/overview`.
//
// WHY THIS IS THE EASIEST GUARANTEE TO LOSE. `/api/overview` renders the state of a WHOLE team,
// conversation threads included. An agent reaching it would read the questions its sibling repos
// ask each other — and the product's isolation promise would fall ON READS, without a single
// tenancy test turning red. The route is admin-only, so an agent token would be refused today; but
// the day a "handy" tool is wired onto it with the admin token from the credentials file, nothing
// stops it any more.
//
// THE NAME OF THIS TEST AVOIDS THE WORD `scripts/check-overview-scope.sh` FORBIDS. That guard
// rejects the capitalised token — the name of the generated queries — in any `.go` outside
// `internal/feature/overview/`. It is deliberately coarse: it catches a mention as readily as a
// call. A file whose whole purpose is to forbid access to that surface was therefore rejected by
// the rule it serves. The name and the comments only use the lowercase form of the HTTP path, and
// the guard stays strict — relaxing it for a test would have opened the door to the contributor
// who finds the query "handy".
//
// WHY A SOURCE SCAN AND NOT A WALK OVER `tools()`. `toolDef` carries no HTTP path: the path is
// chosen inside the call bodies (mcp_call.go, mcp_task_tools.go, mcp_issue_tools.go). There is
// therefore no table to walk, and the only mechanical link available is the package's own text.
//
// `scripts/check-overview-scope.sh` DOES NOT COVER THIS CASE: it rejects the name of the generated
// queries, which is capitalised. The string `"/api/overview"` is lowercase and escapes it entirely.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// supervisionDoor is the ONLY file allowed to write the path of the team-wide surface.
//
// WHY AN EXCEPTION, AND WHY IT PIERCES NOTHING. The `flowlio watch` and `flowlio show` screens are
// built for the HUMAN supervising their team, with their admin token, in their terminal. They live
// in the same binary as the MCP server, but not on the same surface: the agent only ever sees
// `tools()`, locked to eight entries by the next test.
//
// What the exception does NOT excuse: an MCP file calling those screens sideways. That is what the
// second scan below is for — without it, the exception would be worked around in one line,
// `runWatch` called from some "handy" tool.
const supervisionDoor = "watch.go"

// MUTATION: add a `team_overview` tool calling `/api/overview` → this test goes red.
// MUTATION: call `runWatch` from any `mcp_*.go` → this test goes red.
func TestMCPToolsNeverReachTheTeamWideSurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package: %v", err)
	}

	// The ways into the team-wide surface, as an MCP file would write them if it wanted to hook
	// itself on: the path itself, the constant carrying it, and both commands.
	forbiddenToMCP := []string{"/api/overview", "overviewAPI", "runWatch", "runShow"}

	scanned, doorSeen := 0, false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		source, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		scanned++

		if name == supervisionDoor {
			doorSeen = true
			continue
		}

		if strings.HasPrefix(name, "mcp") {
			for _, forbidden := range forbiddenToMCP {
				if strings.Contains(string(source), forbidden) {
					t.Errorf("%s mentions %q — the MCP server does not hook itself onto the "+
						"supervision surface, not even through the CLI", name, forbidden)
				}
			}
			continue
		}

		if strings.Contains(string(source), "/api/overview") {
			t.Errorf("%s reaches /api/overview — the supervision surface is team-scoped, and it "+
				"enters this package through %s only", name, supervisionDoor)
		}
	}

	// Without this guard, a test that no longer scanned any file — directory moved, suffix changed
	// — would pass for green while checking nothing.
	if scanned == 0 {
		t.Fatal("no source file scanned: this test no longer measures anything")
	}

	// An exception matching no file is an exception that outlived what it protected: it would then
	// allow a future `watch.go` without anyone having decided so.
	if !doorSeen {
		t.Fatalf("%s is gone: drop the exception instead of leaving it open", supervisionDoor)
	}
}

// The tool list is written HERE, by hand. A tool added without being added to this list turns this
// test red — including a tool that would not call `/api/overview` but would widen the MCP surface,
// which is paid for in the agent's context on EVERY turn.
//
// This is the second half of the guarantee: the scan above catches the path, this one catches the
// tool.
func TestMCPToolSurfaceIsClosed(t *testing.T) {
	expected := map[string]bool{
		"list_tasks":   true,
		"get":          true,
		"create_task":  true,
		"update_task":  true,
		"block_task":   true,
		"unblock_task": true,
		"create_issue": true,
		"list_issues":  true,
		"answer_issue": true,
		"check_inbox":  true,
		// M5 (FLWL-7). Two, and the pair is the smallest that covers writing, reading, searching
		// and retiring. The index is deliberately NOT among them: it rides the handshake, so
		// reading the memory is not a decision the agent gets to skip.
		"remember": true,
		"recall":   true,
	}

	declared := tools()
	if len(declared) != len(expected) {
		t.Errorf("tools() exposes %d tools, %d expected — the MCP surface changed without this "+
			"file following", len(declared), len(expected))
	}

	for _, tool := range declared {
		if !expected[tool.Name] {
			t.Errorf("unexpected tool: %q — every addition to the MCP surface is paid for on "+
				"every turn of every session", tool.Name)
		}
	}
}
