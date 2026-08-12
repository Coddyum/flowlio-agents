package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher sends the agent's output to the log file, not the console: a woken agent prints a
// whole session, and the terminal is the operator's event log, not its firehose.
func TestAgentLauncherWritesToLogNotConsole(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	launcher := newAgentLauncher(logPath)

	if err := launcher(context.Background(), dir, []string{"sh", "-c", "echo hello-from-agent"}); err != nil {
		t.Fatalf("launcher: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("no log file written: %v", err)
	}
	if !strings.Contains(string(raw), "hello-from-agent") {
		t.Errorf("log = %q, want the agent's stdout captured into it", raw)
	}
	if !strings.Contains(string(raw), "echo hello-from-agent") {
		t.Errorf("log = %q, want each run headed with its argv", raw)
	}
}

// An empty argv is a launch that names no program: it fails rather than opening a shell.
func TestAgentLauncherRefusesEmptyArgv(t *testing.T) {
	launcher := newAgentLauncher(filepath.Join(t.TempDir(), "agent.log"))
	if err := launcher(context.Background(), t.TempDir(), nil); err == nil {
		t.Error("empty argv returned no error")
	}
}
