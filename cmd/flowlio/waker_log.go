package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                          | Ligne |
// |-------------------|-----------------------------------------------------------------|-------|
// | wakeLog           | One timestamped event line about a repository, to the console     | 35    |
// | agentLogPath      | Where a repository's agent output is kept, beside its record      | 41    |
// | newAgentLauncher  | A launcher that sends the agent's output to a log, not the console | 54    |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THE CONSOLE IS AN EVENT LOG, NOT A FIREHOSE. A woken agent is a whole Claude/codex session: it
// prints its reasoning, its tool calls, its prose. Piped straight to the waker's terminal — which is
// what it did — a handful of wakes buried the one thing an operator watches for (did a wake fire, did
// it finish) under pages an agent wrote for itself. So the agent's stream goes to a per-repository
// log FILE, and the console carries only timestamped lifecycle lines: launched, done, dropped,
// failed. The file is named on a failure, so nothing is lost — it is one `tail` away.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// wakeLog writes one event line for a repository: a wall-clock time, the repo key, and what happened.
// It is the console's whole vocabulary in the waker — everything an operator reads while it runs.
func wakeLog(repo, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s  %-8s %s\n", time.Now().Format("15:04:05"), repo, fmt.Sprintf(format, args...))
}

// agentLogPath yields where a repository's agent output is kept — beside its credential, so the two
// share a lifetime and a `flowlio remove` that takes one takes the other.
func agentLogPath(rf credentials.RepoFile) (string, error) {
	recordPath, err := credentials.RepoRecordPath(rf)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(recordPath, ".json") + ".agent.log", nil
}

// newAgentLauncher returns a Launcher that runs the agent with its output sent to logPath instead of
// the console, so the terminal stays the clean event log wakeLog writes.
//
// The file is opened per launch, appended to, and each run is headed with its time and argv — the
// argv names the config and flags but never a secret, which lives in the config file it points at.
func newAgentLauncher(logPath string) func(context.Context, string, []string) error {
	return func(ctx context.Context, dir string, argv []string) error {
		if len(argv) == 0 {
			return errors.New("empty launch command")
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("opening agent log %s: %w", logPath, err)
		}
		defer func() { _ = f.Close() }()

		_, _ = fmt.Fprintf(f, "\n=== %s  %s ===\n", time.Now().Format(time.RFC3339), strings.Join(argv, " "))
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Stdout = f
		cmd.Stderr = f
		return cmd.Run()
	}
}
