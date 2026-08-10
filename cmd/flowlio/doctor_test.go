package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// The pair `doctor` reports on is the one an agent will actually resolve its credentials from, so
// it is read out of the entry itself rather than from anything we could have remembered.
func TestConnectedNamesReadsThePairFromTheEntry(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := writeMCPConfig(dir, "acme", "API"); err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}

	project, repo, err := connectedNames(dir)
	if err != nil {
		t.Fatalf("connectedNames: %v", err)
	}
	if project != "acme" || repo != "API" {
		t.Errorf("read %s/%s, expected acme/API", project, repo)
	}
}

// A repository set up by an older binary carries an entry with an address and a token reference and
// no names at all. That is not a corrupt file, it is an out-of-date one, and the message has to say
// which — a "not readable JSON" here would send somebody hunting for a syntax error.
func TestConnectedNamesRecognisesAnEntryFromAnOlderVersion(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"mcpServers": {"flowlio-agents": {"command": "flowlio", "args": ["mcp"],
	  "env": {"FLOWLIO_API_URL": "http://localhost:42058", "FLOWLIO_TOKEN": "${FLOWLIO_TOKEN}"}}}}`
	if err := os.WriteFile(filepath.Join(dir, mcpConfigName), []byte(legacy), 0o644); err != nil {
		t.Fatalf("writing the legacy config: %v", err)
	}

	_, _, err := connectedNames(dir)
	if err == nil {
		t.Fatal("an entry with no names was accepted")
	}
	if !strings.Contains(err.Error(), "flowlio connect") {
		t.Errorf("the error does not name the command that fixes it: %v", err)
	}
}

// An older workflow file is a FAILURE, not a remark: it is what an agent reads every session.
func TestWorkflowCheckFollowsTheVersion(t *testing.T) {
	dir := t.TempDir()

	if outcome := workflowCheck(dir); outcome.ok {
		t.Error("a missing workflow file passed")
	}

	if _, _, err := writeWorkflowFile(dir); err != nil {
		t.Fatalf("writeWorkflowFile: %v", err)
	}
	if outcome := workflowCheck(dir); !outcome.ok {
		t.Errorf("a freshly written workflow file failed: %s", outcome.cause)
	}

	path := filepath.Join(dir, prompt.WorkflowPath)
	if err := os.WriteFile(path, []byte("# Working with Flowlio — version 1\n"), 0o644); err != nil {
		t.Fatalf("writing an older version: %v", err)
	}
	outcome := workflowCheck(dir)
	if outcome.ok {
		t.Error("an older version passed")
	}
	if !strings.Contains(outcome.cause, "flowlio connect") {
		t.Errorf("the cause does not say how to refresh it: %s", outcome.cause)
	}
}

// ONE POINTER IS ENOUGH, and no client at all is a SKIP rather than a failure. A repository worked
// in with one tool is not broken because it does not carry three pointers, and one that shows no
// sign of any agent client has nothing for us to have got wrong.
func TestPointerCheck(t *testing.T) {
	t.Run("no client detected is skipped, not failed", func(t *testing.T) {
		outcome := pointerCheck(t.TempDir())
		if !outcome.skipped {
			t.Errorf("outcome = ok:%v cause:%q, expected a skip", outcome.ok, outcome.cause)
		}
	})

	t.Run("a client with no pointer fails", func(t *testing.T) {
		dir := t.TempDir()
		seed(t, dir, map[string]string{"CLAUDE.md": "# Doctrine\n"})

		outcome := pointerCheck(dir)
		if outcome.ok || outcome.skipped {
			t.Errorf("outcome = ok:%v skipped:%v, expected a failure", outcome.ok, outcome.skipped)
		}
	})

	t.Run("one pointer among two clients passes", func(t *testing.T) {
		dir := t.TempDir()
		seed(t, dir, map[string]string{"CLAUDE.md": "# Doctrine\n", "AGENTS.md": "# Agents\n"})
		if _, err := writeBlock(filepath.Join(dir, "AGENTS.md"), "", prompt.Pointer()); err != nil {
			t.Fatalf("writeBlock: %v", err)
		}

		outcome := pointerCheck(dir)
		if !outcome.ok {
			t.Errorf("outcome failed with %q, expected a pass", outcome.cause)
		}
		if !strings.Contains(outcome.label, "AGENTS.md") {
			t.Errorf("the passing line does not say which file carries the pointer: %s", outcome.label)
		}
	})
}

// A SKIPPED CHECK NEVER FAILS THE COMMAND. Nothing was observed, so nothing is asserted — and a red
// exit status for something nobody could look at teaches the reader to ignore the status.
func TestReportChecksCountsOnlyRealFailures(t *testing.T) {
	var out strings.Builder
	err := reportChecks(&out, []checkOutcome{
		{label: "green", ok: true},
		{label: "unknowable", skipped: true, cause: "nothing to try"},
	})
	if err != nil {
		t.Errorf("a report with a skip failed the command: %v", err)
	}
	if !strings.Contains(out.String(), "ok    green") || !strings.Contains(out.String(), "skip  unknowable") {
		t.Errorf("the report does not carry both lines:\n%s", out.String())
	}

	out.Reset()
	err = reportChecks(&out, []checkOutcome{
		{label: "green", ok: true},
		{label: "red", cause: "it is broken"},
	})
	if err == nil {
		t.Fatal("a report with a failure exited clean")
	}
	if !strings.Contains(out.String(), "fail  red — it is broken") {
		t.Errorf("the failing line does not carry its cause:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("the error does not say how much failed: %v", err)
	}
}
