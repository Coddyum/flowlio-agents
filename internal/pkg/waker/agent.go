package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                        | Ligne |
// |----------------|---------------------------------------------------------------|-------|
// | Agent          | How to launch one configured agent, fresh or by resume           | 67    |
// | Preset         | The built-in launch recipe for a known agent                    | 79    |
// | Custom         | An arbitrary command template for an unknown agent               | 105   |
// | Agent.Resumes  | Reports whether a wake with this session id resumes or goes fresh | 117   |
// | Agent.LaunchArgv | Builds the argv for one wake, resuming when it can             | 126   |
// | substitute     | Fills {session} and {prompt} into a template                    | 136   |
//
// Fin du sommaire.
// =====================================================================
//
// THE ONE REAL DEPARTURE FROM FLWL-8: agent-agnosticism (DESIGN-WAKE §4.2). FLWL-8 assumed Claude
// throughout — its resume mechanism (`claude -r <id> -p`) re-enters the exact dead session with its
// context. That stays Claude-specific. Every other agent has no verified equivalent, so the waker
// also carries a generic FRESH launch: run a command template in the repo's directory, the agent
// starts cold and rebuilds context from check_inbox.
//
// The choice is per agent, not global: Claude with a known session id resumes; Claude with none, and
// codex / opencode / any custom tool, launch fresh. The prompt is injected either way.

import "strings"

// WakePrompt is what the waker injects at {prompt}: the one instruction a woken agent needs. It does
// not describe the work — check_inbox does that — it only tells the agent to look.
const WakePrompt = "You have inbox items — run check_inbox and act on them, then stop."

// claudePermissionMode is the permission posture a woken Claude runs under. A `-p` session is
// non-interactive: it cannot approve a tool at the prompt. The earlier posture pre-approved ONLY the
// Flowlio MCP server, which left the woken agent able to read the inbox and answer over MCP but
// blocked the instant it needed to edit a file or run a command — so it stopped without doing the
// work (observed on WEB-13, 2026-08-13: "the edit tool keeps getting denied"). Closing the loop with
// no human means the woken agent must ACT on the repo, not only answer, so it runs with permissions
// bypassed — the same autonomy an AFK interactive agent is given.
//
// The guardrail is NOT the permission scope: it is that a sibling repo's text reaches the agent
// sealed as untrusted DATA (cmd/flowlio/mcp_untrusted.go, docs/MODELE-DE-CONFIANCE.md), so a hostile
// issue cannot turn this autonomy into an injected command. Chosen by Maxence on 2026-08-13 (FLWL-87).
const claudePermissionMode = "bypassPermissions"

// claudeEffortArgs maps a rigour tier onto the model a woken Claude runs (FLWL-84, DESIGN-WAKE §14).
// The whole point of the tier: a two-line answer to a trivial question does not need Opus, and this
// is where the sender's declared rigour finally becomes a model — for Claude, and only Claude, the
// one agent whose headless recipe FLWL-8 verified. codex/opencode carry no ladder yet (their Effort
// map is nil, so nothing is injected and they launch at their own default), left for FLWL-85.
//
// Model aliases, not pinned ids: `haiku`/`sonnet`/`opus` track the current generation the way a
// human's `claude --model opus` does, so the ladder does not rot as ids turn over. high and max both
// map to opus today — max is the reserved rung for when effort also drives a thinking budget.
var claudeEffortArgs = map[string][]string{
	"low":      {"--model", "haiku"},
	"standard": {"--model", "sonnet"},
	"high":     {"--model", "opus"},
	"max":      {"--model", "opus"},
}

// Agent is how to launch one configured agent.
//
// Command is the FRESH template, run in the repo directory; {prompt} is where the wake sentence
// goes. Resume, when set, is the CLAUDE-style re-entry into a live session: {session} takes the
// session id, {prompt} the sentence. Resume is used only when it is set AND a session id is known;
// otherwise the launch is fresh, which is why codex and opencode — with no Resume — are first-class.
type Agent struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Resume  []string `json:"resume,omitempty"`
	// Effort maps a rigour tier (internal/pkg/effort) onto the extra argv that selects a model for it.
	// Nil for an agent with no ladder, in which case a wake injects nothing and the agent launches at
	// its own default. Only the Claude preset carries one today.
	Effort map[string][]string `json:"-"`
}

// Preset yields the built-in launch recipe for a known agent, or false for an unknown name. The
// presets carry the exact headless invocation so a user types nothing technical (DESIGN-WAKE §4.2).
func Preset(name string) (Agent, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude":
		// --permission-mode bypassPermissions lets the woken agent act on the repo — answer over MCP,
		// edit files, run the build — instead of stalling on its first write in a `-p` session it cannot
		// approve interactively. See claudePermissionMode: the untrusted seal, not the permission scope,
		// is the guardrail. The MCP server is loaded by --mcp-config (hosted) or the repo .mcp.json
		// (self-host), and bypass grants its tools like any other.
		return Agent{
			Name:    "claude",
			Command: []string{"claude", "-p", "{prompt}", "--permission-mode", claudePermissionMode},
			Resume:  []string{"claude", "-r", "{session}", "-p", "{prompt}", "--permission-mode", claudePermissionMode},
			Effort:  claudeEffortArgs,
		}, true
	case "codex":
		return Agent{Name: "codex", Command: []string{"codex", "exec", "{prompt}"}}, true
	case "opencode":
		return Agent{Name: "opencode", Command: []string{"opencode", "run", "{prompt}"}}, true
	default:
		return Agent{}, false
	}
}

// Custom builds an agent from an arbitrary command template — the escape hatch for a tool no preset
// covers. The template is split on spaces; {prompt} is injected wherever it appears. An empty
// template, or one that names no program, yields false.
func Custom(template string) (Agent, bool) {
	fields := strings.Fields(template)
	if len(fields) == 0 {
		return Agent{}, false
	}
	return Agent{Name: "custom", Command: fields}, true
}

// Resumes reports whether a wake with this session id resumes rather than launches fresh: the agent
// supports resume (Resume set) and a live session id is known. It is the one condition LaunchArgv
// branches on, exported so Launch can read it — a launch that failed while resuming can still fall
// back to a fresh one, and only Resumes knows a resume was even attempted.
func (a Agent) Resumes(sessionID string) bool {
	return len(a.Resume) > 0 && strings.TrimSpace(sessionID) != ""
}

// LaunchArgv builds the argv for one wake.
//
// It resumes when it can — the agent supports it (Resume set) and a live session id is known — and
// launches fresh otherwise. The two paths are the whole of §4.2: the same waker drives Claude back
// into its dead session and starts codex cold, from one configuration.
func (a Agent) LaunchArgv(sessionID, prompt string) []string {
	if a.Resumes(sessionID) {
		return substitute(a.Resume, sessionID, prompt)
	}
	return substitute(a.Command, "", prompt)
}

// substitute fills {session} and {prompt} into every field of a template, leaving anything else
// untouched. A field is replaced whole when it is exactly the placeholder, and inline otherwise, so
// both `{prompt}` and `--message={prompt}` work.
func substitute(template []string, sessionID, prompt string) []string {
	out := make([]string, len(template))
	for i, field := range template {
		field = strings.ReplaceAll(field, "{session}", sessionID)
		field = strings.ReplaceAll(field, "{prompt}", prompt)
		out[i] = field
	}
	return out
}
