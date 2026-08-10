package prompt_test

// THE PROMPT IS A PRODUCT DELIVERABLE, so it is tested like one. It mirrors the test that guards the
// same markdown in flowlio-core: the two copies drift the day only one of them is checked.
//
// Every check below is guarded by one that says the body is really there — an empty string satisfies
// "does not mention a tool that does not exist", and a prompt that failed to embed would pass every
// negative assertion in this file.

import (
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// A prompt that failed to embed is an empty string, and it would silently pass most of what
// follows. This is the check that stops that.
func TestThePromptIsActuallyEmbedded(t *testing.T) {
	body := prompt.Workflow()
	if len(body) < 2000 {
		t.Fatalf("the prompt is %d bytes: it did not embed, or it lost most of itself", len(body))
	}
	if !strings.HasPrefix(body, "# Working with Flowlio") {
		t.Fatalf("the prompt does not start with its heading: %.80q", body)
	}
}

// THE VERSION IS VISIBLE, and the two places it appears agree. `flowlio connect` rewrites the file
// when the version differs from the binary's, so a Version that does not match the text would make
// that comparison meaningless — and both numbers look authoritative to whoever reads them.
//
// MUTATION: bump Version without touching the markdown heading — this goes red.
func TestTheServedVersionIsTheOneWrittenInTheText(t *testing.T) {
	if prompt.Version == "" {
		t.Fatal("the prompt is written without a version")
	}
	want := "# Working with Flowlio — version " + prompt.Version
	if !strings.Contains(prompt.Workflow(), want) {
		t.Fatalf("the markdown does not carry version %q; wanted the heading %q", prompt.Version, want)
	}
}

// IT DESCRIBES THE SURFACE IT IS SERVED NEXT TO. A prompt naming a tool that does not exist teaches
// an agent to make calls that fail, and it fails at the worst moment: the first session.
//
// MUTATION: drop any of the twelve from the text — this goes red.
func TestThePromptNamesTheTwelveRepositoryTools(t *testing.T) {
	body := prompt.Workflow()

	for _, tool := range []string{
		"check_inbox", "list_tasks", "get", "create_task", "update_task",
		"block_task", "unblock_task", "create_issue", "list_issues", "answer_issue",
		"remember", "recall",
	} {
		if !strings.Contains(body, tool) {
			t.Fatalf("the prompt does not mention %q", tool)
		}
	}
}

// IT NAMES THE SERVER THE WAY `connect` INSTALLS IT. The prompt tells an agent how to tell this
// surface apart from the hosted team board, and it does so by name: a text saying `flowlio` while
// the `.mcp.json` says `flowlio-agents` would teach the distinction against the wrong label.
//
// MUTATION: change mcpServerKey without the text — this goes red.
func TestThePromptNamesTheServerAsItIsInstalled(t *testing.T) {
	body := prompt.Workflow()

	for _, phrase := range []string{
		"**They share nothing.**",
		"a task lives on one surface, and only one",
		"`flowlio-agents`",
	} {
		if !strings.Contains(body, phrase) {
			t.Fatalf("the prompt no longer separates the two surfaces: %q is gone", phrase)
		}
	}
}

// THE POINTER IS THE OTHER HALF OF THE GESTURE. It has to name the exact path the prompt is written
// to — a pointer to a file that is not there sends a session looking and finding nothing — and it
// has to carry both markers, which is what makes `connect` re-runnable and `disconnect` possible.
//
// MUTATION: change WorkflowPath without the pointer text — this goes red.
func TestThePointerNamesTheFileItIsWrittenNextTo(t *testing.T) {
	pointer := prompt.Pointer()

	if !strings.HasPrefix(pointer, prompt.MarkerStart) {
		t.Errorf("the pointer does not open with %q: %.60q", prompt.MarkerStart, pointer)
	}
	if !strings.HasSuffix(pointer, prompt.MarkerEnd) {
		t.Errorf("the pointer does not close with %q: %.60q", prompt.MarkerEnd, pointer)
	}
	if !strings.Contains(pointer, prompt.WorkflowPath) {
		t.Errorf("the pointer does not name %s:\n%s", prompt.WorkflowPath, pointer)
	}
}

// THE PROMPT NEVER GOES INTO THE ENTRY FILE — that is the whole reason the pointer exists. A pointer
// that had grown into the prompt would put two hundred and fifty lines back into a file loaded on
// every session, which is the failure this design was chosen to avoid.
func TestThePointerStaysAPointer(t *testing.T) {
	pointer := prompt.Pointer()

	if lines := strings.Count(pointer, "\n") + 1; lines > 10 {
		t.Errorf("the pointer is %d lines long: it is turning into the prompt itself", lines)
	}
	if strings.Contains(pointer, "check_inbox") {
		t.Error("the pointer carries the prompt's content instead of pointing at it")
	}
}

// The prompt goes out with the rest of the product, in English.
func TestThePromptIsInEnglish(t *testing.T) {
	body := prompt.Workflow()
	if !strings.Contains(body, "check_inbox") {
		t.Fatal("the prompt lost its content; the check below would pass on anything")
	}

	for _, french := range []string{
		"Référencée", "Ne jamais", "Fini quand", "la session", "de la tâche", "Ce que Claude",
	} {
		if strings.Contains(body, french) {
			t.Fatalf("the prompt still carries French: %q", french)
		}
	}
}
