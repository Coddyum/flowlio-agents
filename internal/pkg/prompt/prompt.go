// Package prompt holds the Flowlio workflow prompt and the three lines that point a session at it.
//
// IT IS A PRODUCT DELIVERABLE, NOT DOCUMENTATION. `flowlio connect` writes it into the repository
// it is run from, and an agent reads it at the start of every session. That is also why the version
// is VISIBLE: a repository carrying version 1 has no way to know 2 exists unless the text says
// which one it is.
//
// TWO COPIES EXIST, AND THE ENGINE IS THE CANONICAL ONE. flowlio-core serves the same markdown to
// hosted customers from `internal/feature/agents/prompt/`. This repository owns the twelve tools the
// text describes, so it is the home the other one should eventually consume — having flowlio-core
// read this package is a card, not a regret. Until then the version number is what makes the drift
// visible from both sides: bump it here and there together, or find out from a customer.
package prompt

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | Workflow     | The workflow prompt itself, as written into the repository        | 67    |
// | Pointer      | The bounded block that sends a session to that file               | 78    |
//
// Fin du sommaire.
// =====================================================================

import (
	_ "embed"
	"strings"
)

// workflow is the prompt itself.
//
// EMBEDDED RATHER THAN READ FROM DISK. `flowlio` is a single binary a user may have copied anywhere;
// a file read at run time would work from a checkout and fail everywhere else. embed also makes a
// missing file a COMPILE error.
//
//go:embed workflow.md
var workflow string

const (
	// Version is the version of the prompt written. It is bumped BY HAND, here, when the text changes
	// in a way a repository that already carries it should know about — not on every typo.
	//
	// It has to match the heading of the markdown, and a test says so: a version that disagreed with
	// the one written in the text is worse than no version at all, because both look authoritative.
	Version = "2"

	// WorkflowPath is where `flowlio connect` writes the prompt, relative to the repository root.
	//
	// NEUTRAL ON PURPOSE — not under `.claude/`, not under `.cursor/`. Several agent clients can be
	// used in the same repository, and a prompt filed under one of their directories reads as
	// belonging to that client. The pointer is what makes each client find it.
	WorkflowPath = ".flowlio/workflow.md"

	// MarkerStart and MarkerEnd bound every block this CLI writes into a file that belongs to the
	// user. They buy the two properties the whole of `connect` rests on: re-running it REPLACES what
	// is between them rather than stacking a second copy, and `flowlio disconnect` can take the block
	// back out without a human editing the repository's own doctrine file by hand.
	MarkerStart = "<!-- flowlio:start -->"
	MarkerEnd   = "<!-- flowlio:end -->"
)

// Workflow returns the markdown of the prompt.
//
// Trimmed so the payload starts at the heading whatever the file's trailing newline does — it is
// written into a rules file, and a leading blank line changes how some editors render the first
// heading.
func Workflow() string {
	return strings.TrimSpace(workflow)
}

// Pointer returns the bounded block that sends a session to WorkflowPath.
//
// THE PROMPT ITSELF NEVER GOES INTO THE ENTRY FILE. Two hundred and fifty lines inside a file that
// is loaded on every session drown the repository's own doctrine and are re-read in full every
// conversation. But a rules file nothing points at is a file nothing opens. Neither half works
// alone — the reasoning is flowlio-core's, in `Flowlio/src/lib/agents/claude-md-pointer.ts`, and it
// is taken as it stands.
func Pointer() string {
	return MarkerStart + `
## Flowlio

This repository is connected to Flowlio through MCP: its tasks, its questions to the sibling
repositories and its memory live there, not in this file. Read ` + "`" + WorkflowPath + "`" + ` at the
start of every session and work the way it says.
` + MarkerEnd
}
