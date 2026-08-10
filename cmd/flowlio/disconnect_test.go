package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// THE REPOSITORY COMES BACK AS IT WAS. This is the guarantee `disconnect` exists for, and the only
// honest way to state it is a snapshot of every file — an assertion that merely looked for the
// absence of a marker would also pass on a repository that had been emptied.
//
// TEXT FILES ARE COMPARED BYTE FOR BYTE. That is where the risk was: `CLAUDE.md` is the
// repository's own doctrine, and a stray newline there is a diff a human has to explain.
//
// THE TWO JSON FILES ARE COMPARED BY CONTENT, AND THE REASON IS WRITTEN DOWN RATHER THAN HIDDEN.
// `.mcp.json` and `.claude/settings.json` are decoded, edited and re-encoded, so the FIRST connect
// re-indents them into encoding/json's canonical shape. Everything that was in them is still in
// them, and the next test proves the normalisation happens once and never again — but a repository
// whose `.mcp.json` was hand-formatted will show one whitespace diff after its first connect.
//
// MUTATION: leave the workflow file behind, or one newline in CLAUDE.md — this goes red.
func TestConnectThenDisconnectLeavesTheRepositoryAsItWas(t *testing.T) {
	dir := t.TempDir()

	// A repository that already has a doctrine file, another MCP server, a Cursor directory and its
	// own Claude Code settings — that is, one somebody actually works in.
	seed(t, dir, map[string]string{
		"CLAUDE.md":               "# Doctrine\n\nThe rules of this repository.\n",
		".mcp.json":               "{\n  \"mcpServers\": {\n    \"github\": {\"command\": \"gh-mcp\"}\n  }\n}\n",
		".claude/settings.json":   "{\n  \"permissions\": {\"allow\": [\"Bash(go test:*)\"]}\n}\n",
		".cursor/rules/other.mdc": "---\nalwaysApply: false\n---\n\nSomething of theirs.\n",
	})
	before := snapshot(t, dir)

	connectInto(t, dir)

	// The connection really happened — without this, everything below would pass on a no-op.
	after := snapshot(t, dir)
	if len(after) <= len(before) {
		t.Fatalf("connect wrote nothing: %d files before, %d after", len(before), len(after))
	}
	for _, want := range []string{prompt.WorkflowPath, filepath.Join(".cursor", "rules", "flowlio.mdc")} {
		if _, found := after[want]; !found {
			t.Fatalf("connect did not write %s", want)
		}
	}
	if !strings.Contains(after["CLAUDE.md"], prompt.MarkerStart) {
		t.Fatal("connect did not write the pointer into CLAUDE.md")
	}

	if err := disconnectRepo(dir, io.Discard); err != nil {
		t.Fatalf("disconnectRepo: %v", err)
	}

	restored := snapshot(t, dir)
	for path, want := range before {
		got, found := restored[path]
		if !found {
			t.Errorf("%s disappeared", path)
			continue
		}
		if filepath.Ext(path) == ".json" {
			if !sameJSON(t, got, want) {
				t.Errorf("%s lost or gained content.\nwant %s\ngot  %s", path, want, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%s came back different.\nwant %q\ngot  %q", path, want, got)
		}
	}
	for path := range restored {
		if _, found := before[path]; !found {
			t.Errorf("%s was left behind", path)
		}
	}
}

// THE JSON NORMALISATION HAPPENS ONCE. A second connect/disconnect cycle over an already-connected
// repository must leave every file byte for byte as it found it, JSON included — otherwise every
// run would produce a diff and the round trip would never settle.
//
// MUTATION: re-encode with different indentation on one of the two paths — this goes red.
func TestASecondRoundTripChangesNothingAtAll(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, map[string]string{
		"CLAUDE.md":             "# Doctrine\n\nThe rules of this repository.\n",
		".mcp.json":             "{\n  \"mcpServers\": {\n    \"github\": {\"command\": \"gh-mcp\"}\n  }\n}\n",
		".claude/settings.json": "{\n  \"permissions\": {\"allow\": [\"Bash(go test:*)\"]}\n}\n",
	})

	// First cycle: this is the one allowed to re-indent the JSON.
	connectInto(t, dir)
	if err := disconnectRepo(dir, io.Discard); err != nil {
		t.Fatalf("first disconnectRepo: %v", err)
	}
	settled := snapshot(t, dir)

	connectInto(t, dir)
	if err := disconnectRepo(dir, io.Discard); err != nil {
		t.Fatalf("second disconnectRepo: %v", err)
	}

	for path, want := range settled {
		if got := snapshot(t, dir)[path]; got != want {
			t.Errorf("%s changed on the second cycle.\nwant %q\ngot  %q", path, want, got)
		}
	}
}

// Twice is not an error. Anybody who is not sure the first run worked runs it again, and a second
// `disconnect` has to say "nothing to remove" rather than fail.
func TestDisconnectTwiceIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, map[string]string{"CLAUDE.md": "# Doctrine\n"})
	connectInto(t, dir)

	if err := disconnectRepo(dir, io.Discard); err != nil {
		t.Fatalf("first disconnectRepo: %v", err)
	}
	var out strings.Builder
	if err := disconnectRepo(dir, &out); err != nil {
		t.Fatalf("second disconnectRepo: %v", err)
	}
	if !strings.Contains(out.String(), string(actionAbsent)) {
		t.Errorf("the second run does not say there was nothing to remove:\n%s", out.String())
	}
}

// connectInto plays exactly what `connect` writes, without the network half: the token resolution
// and the self-test both need an instance, and neither writes a file.
func connectInto(t *testing.T, dir string) {
	t.Helper()

	if _, _, err := writeMCPConfig(dir, "acme", "API"); err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if _, _, err := writeWorkflowFile(dir); err != nil {
		t.Fatalf("writeWorkflowFile: %v", err)
	}
	for _, entry := range detectEntryFiles(dir) {
		if _, err := writeBlock(filepath.Join(dir, entry.Path), entry.Header, prompt.Pointer()); err != nil {
			t.Fatalf("writeBlock %s: %v", entry.Path, err)
		}
	}
	if _, _, err := writeInboxHook(dir, "API"); err != nil {
		t.Fatalf("writeInboxHook: %v", err)
	}
}

// sameJSON says whether two documents carry the same content, whatever their whitespace.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()

	var left, right any
	if err := json.Unmarshal([]byte(a), &left); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, a)
	}
	if err := json.Unmarshal([]byte(b), &right); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b)
	}
	return reflect.DeepEqual(left, right)
}

// seed writes a repository's starting state, creating the directories it needs.
func seed(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}

// snapshot reads every file under dir, keyed by its path relative to it. Directories are not
// recorded: an empty one left behind is not a change git would report.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	sort.Strings(paths)
	return files
}
