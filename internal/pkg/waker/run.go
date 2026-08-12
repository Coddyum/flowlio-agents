package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                             | Ligne |
// |-----------|--------------------------------------------------------------------|-------|
// | Launcher  | Runs the agent argv in a directory and waits for it to exit          | 28    |
// | Repo      | One repository the waker drives: where, how, and which session       | 35    |
// | Launch    | Builds the argv and runs it, under the relaunch cap                  | 58    |
// | Repo.argv | The agent argv for a session id, with ExtraArgs appended             | 79    |
// | ProbeDelay | Turns a server-dictated next_probe_after into a sleep               | 91    |
//
// Fin du sommaire.
// =====================================================================
//
// THE LAUNCH, tying the pieces together (DESIGN-WAKE §8). A wake becomes an argv (agent.go), gated
// by the relaunch cap (cap.go), run in the repository's own directory. The launcher is injected so
// the orchestration is testable without spawning a process, and so the day a repo is driven over ssh
// or inside a sandbox, only the launcher changes.

import (
	"context"
	"time"
)

// Launcher runs argv in dir and waits for the process to exit. NON-INTERACTIVE by contract: the
// woken agent is launched with `-p`, cannot ask anything, and returns to nothing (DESIGN-WAKE §4.2).
type Launcher func(ctx context.Context, dir string, argv []string) error

// Repo is one repository the waker drives.
//
// SessionID is the Claude session captured by the SessionStart hook when a human started a session;
// it enables resume. It is empty for every other agent, and for Claude with no live session — in
// both cases the launch is fresh.
type Repo struct {
	Project   string
	Key       string
	Path      string
	Agent     Agent
	SessionID string
	// ExtraArgs are appended to the agent's argv on every launch, resume or fresh. In hosted they
	// carry `--mcp-config <file> --strict-mcp-config` so a woken Claude authenticates the engine's MCP
	// with the account token instead of an OAuth flow it cannot run headless (DESIGN-WAKE §6). Empty
	// in self-host, where the repo's own `.mcp.json` already resolves to a local token.
	ExtraArgs []string
	// Effort is the rigour tier this wake launches at, already clamped to the daemon's ceiling
	// (internal/pkg/effort). Set per launch like SessionID, because it varies with the work found.
	// It selects the agent's model through Agent.Effort; an empty tier or an agent with no ladder
	// injects nothing and the agent keeps its own default.
	Effort string
}

// Launch builds the argv for a wake and runs it in the repo's directory, under the relaunch cap.
//
// It returns launched=false, with no error and no run, when the cap refuses: a dropped wake is a
// deliberate guardrail, not a failure. Otherwise it runs the agent and returns whatever the launcher
// returns.
func Launch(ctx context.Context, cap *Cap, run Launcher, repo Repo, now time.Time) (launched bool, err error) {
	if !cap.Allow(repo.Key, now) {
		return false, nil
	}

	err = run(ctx, repo.Path, repo.argv(repo.SessionID))

	// A resume that failed is most often a session Claude no longer has — deleted, or aged out of its
	// local store — and `claude -r <dead>` exits non-zero. A fresh launch still reads the inbox and
	// answers, so fall back to one rather than drop the wake. Bounded: the fallback passes an empty
	// session id, so it can never itself resume, and this runs at most once.
	if err != nil && repo.Agent.Resumes(repo.SessionID) {
		err = run(ctx, repo.Path, repo.argv(""))
	}
	return true, err
}

// argv is the agent's launch argv for a session id, with the repo's ExtraArgs appended. It is one
// place so the resume attempt and its fresh fallback carry the same extra flags — the hosted
// `--mcp-config` among them, which a fallback that dropped it would launch an agent unable to reach
// its inbox.
func (r Repo) argv(sessionID string) []string {
	base := r.Agent.LaunchArgv(sessionID, WakePrompt)
	// The model selection goes between the agent's own argv and the ExtraArgs, so a `--model` sits
	// before the hosted `--mcp-config` exactly as a human would type it. A nil Effort map (an agent
	// with no ladder) or an unlisted tier yields nil, appended as nothing.
	base = append(base, r.Agent.Effort[r.Effort]...)
	return append(base, r.ExtraArgs...)
}

// ProbeDelay turns the server's next_probe_after into a sleep, with a floor so a misread or missing
// value can never become a busy loop. The SERVER owns the cadence (DESIGN-WAKE §3): this only clamps
// a pathological answer, it never shortens a legitimate one.
func ProbeDelay(nextProbeAfter int) time.Duration {
	const floor = 30 * time.Second
	d := time.Duration(nextProbeAfter) * time.Second
	if d < floor {
		return floor
	}
	return d
}
