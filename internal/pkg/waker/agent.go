package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                        | Ligne |
// |----------------|---------------------------------------------------------------|-------|
// | Agent          | How to launch one configured agent, fresh or by resume           | 43    |
// | Preset         | The built-in launch recipe for a known agent                    | 51    |
// | Custom         | An arbitrary command template for an unknown agent               | 75    |
// | Agent.LaunchArgv | Builds the argv for one wake, resuming when it can             | 88    |
// | substitute     | Fills {session} and {prompt} into a template                    | 98    |
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

// claudeAllowedTools names the MCP server whose tools a woken Claude may call without a prompt. The
// whole server is scoped in, so check_inbox and every answer/read tool it needs is granted, and
// nothing else is — a wake never silently gains file writes or shell. The name matches the one the
// `.mcp.json` (self-host) and flowlio.me (hosted) give the server: `flowlio-agents`.
const claudeAllowedTools = "mcp__flowlio-agents"

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
}

// Preset yields the built-in launch recipe for a known agent, or false for an unknown name. The
// presets carry the exact headless invocation so a user types nothing technical (DESIGN-WAKE §4.2).
func Preset(name string) (Agent, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude":
		// --allowedTools pre-approves the Flowlio MCP tools: a `-p` session is non-interactive and
		// cannot approve a tool at the prompt, so without this the woken agent is launched and then
		// blocked on its very first call to check_inbox. Scoped to this one server, so the wake grants
		// nothing beyond reading the inbox and answering — never file writes or shell.
		return Agent{
			Name:    "claude",
			Command: []string{"claude", "-p", "{prompt}", "--allowedTools", claudeAllowedTools},
			Resume:  []string{"claude", "-r", "{session}", "-p", "{prompt}", "--allowedTools", claudeAllowedTools},
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

// LaunchArgv builds the argv for one wake.
//
// It resumes when it can — the agent supports it (Resume set) and a live session id is known — and
// launches fresh otherwise. The two paths are the whole of §4.2: the same waker drives Claude back
// into its dead session and starts codex cold, from one configuration.
func (a Agent) LaunchArgv(sessionID, prompt string) []string {
	if len(a.Resume) > 0 && strings.TrimSpace(sessionID) != "" {
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
