package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// IDEMPOTENCE IS THE POINT OF THE MARKERS. `connect` is re-run by anybody unsure it worked, and by
// `setup` printing the same line twice. A second run must replace the block, not stack a twin — two
// pointers in `CLAUDE.md` is a file the user then has to repair by hand.
//
// MUTATION: append instead of replacing between the markers — this goes red.
func TestWriteBlockIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	const original = "# Doctrine\n\nThe rules of this repository.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("writing the existing file: %v", err)
	}

	first, err := writeBlock(path, "", prompt.Pointer())
	if err != nil {
		t.Fatalf("first writeBlock: %v", err)
	}
	if first != actionUpdated {
		t.Errorf("first write = %q, expected %q", first, actionUpdated)
	}
	afterFirst := read(t, path)

	second, err := writeBlock(path, "", prompt.Pointer())
	if err != nil {
		t.Fatalf("second writeBlock: %v", err)
	}
	if second != actionUnchanged {
		t.Errorf("second write = %q, expected %q", second, actionUnchanged)
	}
	if got := read(t, path); got != afterFirst {
		t.Errorf("the second run changed the file:\n%s", got)
	}

	if n := strings.Count(afterFirst, prompt.MarkerStart); n != 1 {
		t.Errorf("the file carries %d opening markers, expected exactly 1:\n%s", n, afterFirst)
	}
	if !strings.HasPrefix(afterFirst, original) {
		t.Errorf("the user's own content was not preserved verbatim:\n%s", afterFirst)
	}
}

// THE BLOCK COMES BACK OUT AND THE FILE IS THE ONE IT WAS. This is what `flowlio disconnect` rests
// on: without it, disconnecting means asking a human to edit the repository's doctrine file by hand.
//
// The comparison is on the WHOLE file, byte for byte. Asserting "does not contain the marker" would
// also pass on a file that had been emptied.
//
// MUTATION: leave one newline behind on removal — this goes red.
func TestWriteBlockThenRemoveBlockRestoresTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	for _, original := range []string{
		"# Doctrine\n\nThe rules of this repository.\n",
		"# Doctrine\n",
		"one line, no trailing newline",
	} {
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("writing the existing file: %v", err)
		}
		if _, err := writeBlock(path, "", prompt.Pointer()); err != nil {
			t.Fatalf("writeBlock: %v", err)
		}
		if _, err := removeBlock(path, ""); err != nil {
			t.Fatalf("removeBlock: %v", err)
		}

		// A file with no trailing newline comes back with one: that normalisation is documented on
		// writeBlock, and it is the only difference tolerated here.
		want := original
		if !strings.HasSuffix(want, "\n") {
			want += "\n"
		}
		if got := read(t, path); got != want {
			t.Errorf("round trip changed the file.\nwant %q\ngot  %q", want, got)
		}
	}
}

// A file we created for a client that had none is ours to take away again: leaving an empty
// `.cursor/rules/flowlio.mdc` behind would make `git status` dirty for no reason.
//
// The header stays OUTSIDE the block on purpose — a `.mdc` whose front-matter was removed with the
// block is a rule file Cursor refuses to load — so this also proves the header goes with the file
// rather than surviving it.
func TestRemoveBlockTakesTheFileWeCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flowlio.mdc")

	if _, err := writeBlock(path, cursorRuleHeader, prompt.Pointer()); err != nil {
		t.Fatalf("writeBlock: %v", err)
	}
	created := read(t, path)
	if !strings.HasPrefix(created, "---\n") {
		t.Errorf("the front-matter Cursor requires is missing:\n%s", created)
	}
	if strings.Contains(created[:strings.Index(created, prompt.MarkerStart)], prompt.MarkerEnd) {
		t.Error("the header ended up inside the block")
	}

	if _, err := removeBlock(path, cursorRuleHeader); err != nil {
		t.Fatalf("removeBlock: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the file we created survived the removal: %v", err)
	}
}

// A block whose closing marker was lost is NOT rewritten from the truncation to the end of the
// file: that would delete whatever the user wrote after it. A whole block is appended instead.
func TestWriteBlockLeavesATruncatedBlockAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	truncated := "# Doctrine\n\n" + prompt.MarkerStart + "\nhalf a block\n\n## Something of mine\n"
	if err := os.WriteFile(path, []byte(truncated), 0o644); err != nil {
		t.Fatalf("writing the existing file: %v", err)
	}

	if _, err := writeBlock(path, "", prompt.Pointer()); err != nil {
		t.Fatalf("writeBlock: %v", err)
	}

	got := read(t, path)
	if !strings.HasPrefix(got, truncated) {
		t.Errorf("what the user wrote after the truncation was lost:\n%s", got)
	}
	if !strings.Contains(got, prompt.MarkerEnd) {
		t.Errorf("no whole block was appended:\n%s", got)
	}
}

// The workflow file is rewritten when it carries another version, and left alone when it carries
// this one — that comparison is the only reason prompt.Version exists.
func TestWriteWorkflowFileFollowsTheVersion(t *testing.T) {
	dir := t.TempDir()

	path, action, err := writeWorkflowFile(dir)
	if err != nil {
		t.Fatalf("writeWorkflowFile: %v", err)
	}
	if action != actionWritten {
		t.Errorf("first write = %q, expected %q", action, actionWritten)
	}
	if body := read(t, path); !strings.Contains(body, "# Working with Flowlio — version "+prompt.Version) {
		t.Errorf("the file does not carry version %s:\n%.120s", prompt.Version, body)
	}

	if _, action, err = writeWorkflowFile(dir); err != nil || action != actionUnchanged {
		t.Errorf("second write = %q (err %v), expected %q", action, err, actionUnchanged)
	}

	if err := os.WriteFile(path, []byte("# Working with Flowlio — version 1\n"), 0o644); err != nil {
		t.Fatalf("writing an older version: %v", err)
	}
	if _, action, err = writeWorkflowFile(dir); err != nil || action != actionUpdated {
		t.Errorf("write over an older version = %q (err %v), expected %q", action, err, actionUpdated)
	}
	if body := read(t, path); !strings.Contains(body, "version "+prompt.Version) {
		t.Errorf("the older version was not replaced:\n%.120s", body)
	}
}

// THE SETTINGS FILE IS THE USER'S. Ours is merged in and found again by its stamp prefix, so a
// second `connect` replaces it rather than adding a twin that fires twice.
func TestWriteInboxHookMergesAndDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("creating .claude: %v", err)
	}
	settings := filepath.Join(dir, hookSettingsPath)

	existing := `{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo mine"}]}]}
}`
	if err := os.WriteFile(settings, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the existing settings: %v", err)
	}

	if _, action, err := writeInboxHook(dir, "API"); err != nil || action != actionWritten {
		t.Fatalf("writeInboxHook = %q (err %v)", action, err)
	}
	if _, _, err := writeInboxHook(dir, "API"); err != nil {
		t.Fatalf("second writeInboxHook: %v", err)
	}

	raw := read(t, settings)
	if n := strings.Count(raw, hookStampPrefix); n != 1 {
		t.Errorf("the hook appears %d times, expected exactly 1:\n%s", n, raw)
	}
	if !strings.Contains(raw, "echo mine") {
		t.Errorf("the user's own hook disappeared:\n%s", raw)
	}
	if !strings.Contains(raw, "Bash(go test:*)") {
		t.Errorf("a settings key we do not own was lost:\n%s", raw)
	}

	// The hook is keyed by repo so two repositories open side by side do not silence one another,
	// and it says which tool to call — a reminder that names nothing is a reminder nobody acts on.
	var decoded struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("the settings file is not valid JSON: %v", err)
	}
	var ours string
	for _, matcher := range decoded.Hooks[hookEvent] {
		for _, h := range matcher.Hooks {
			if strings.Contains(h.Command, hookStampPrefix) {
				ours = h.Command
			}
		}
	}
	for _, want := range []string{hookStampPrefix + "API", "check_inbox", "${TMPDIR:-/tmp}", "-ge 300"} {
		if !strings.Contains(ours, want) {
			t.Errorf("the hook command does not carry %q: %s", want, ours)
		}
	}
}

// `disconnect` gives the settings file back with everything else untouched.
func TestRemoveInboxHookLeavesTheRestAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("creating .claude: %v", err)
	}
	settings := filepath.Join(dir, hookSettingsPath)

	existing := `{"permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo mine"}]}]}}`
	if err := os.WriteFile(settings, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the existing settings: %v", err)
	}
	if _, _, err := writeInboxHook(dir, "API"); err != nil {
		t.Fatalf("writeInboxHook: %v", err)
	}

	if _, action, err := removeInboxHook(dir); err != nil || action != actionRemoved {
		t.Fatalf("removeInboxHook = %q (err %v)", action, err)
	}

	raw := read(t, settings)
	if strings.Contains(raw, hookStampPrefix) {
		t.Errorf("our hook survived:\n%s", raw)
	}
	if !strings.Contains(raw, "echo mine") || !strings.Contains(raw, "Bash(go test:*)") {
		t.Errorf("something that was not ours was removed with it:\n%s", raw)
	}

	// Twice is not an error: anybody unsure it worked runs it again.
	if _, action, err := removeInboxHook(dir); err != nil || action != actionAbsent {
		t.Errorf("second removeInboxHook = %q (err %v), expected %q", action, err, actionAbsent)
	}
}

// read returns the whole file, which is what the assertions above compare — a structure would hide
// exactly the whitespace differences these tests exist to catch.
func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}
