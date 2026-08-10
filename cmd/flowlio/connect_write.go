package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                       | Ligne |
// |--------------------|--------------------------------------------------------------|-------|
// | writeAction        | What one write did, in the words the output uses              | 40    |
// | writeWorkflowFile  | Writes .flowlio/workflow.md, rewriting an older version       | 55    |
// | writeBlock         | Puts the bounded block into a file that belongs to the user   | 91    |
// | removeBlock        | Takes that block back out, and the file with it if we made it | 134   |
// | blockBounds        | The byte range of our block, or none when it is not whole     | 178   |
//
// Fin du sommaire.
// =====================================================================
//
// EVERY WRITE IN THIS FILE IS IDEMPOTENT, and that is not a nicety. `connect` is re-run by anybody
// who is not sure it worked the first time, and by `setup` printing the same line twice. A write
// that stacked a second copy of its block would punish exactly that.
//
// Two of the four targets are ours — `.mcp.json` and `.flowlio/workflow.md` — and are written
// whole. The other two belong to the user, and are only ever touched BETWEEN MARKERS.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

const (
	filesPerm = 0o644
	dirsPerm  = 0o755
)

// writeAction says what one write did, in the words the output uses. A command that claims to have
// written what was already there teaches its reader to stop believing it.
type writeAction string

const (
	actionWritten   writeAction = "written"
	actionUpdated   writeAction = "updated"
	actionUnchanged writeAction = "already up to date"
	actionRemoved   writeAction = "removed"
	actionAbsent    writeAction = "nothing to remove"
)

// writeWorkflowFile writes the workflow prompt into the repository.
//
// A file already carrying the CURRENT version is left alone; one carrying anything else is
// rewritten and the caller says so. The version is what makes that decision possible, which is why
// prompt.Version is tested against the heading of the markdown itself.
func writeWorkflowFile(dir string) (path string, action writeAction, err error) {
	path = filepath.Join(dir, prompt.WorkflowPath)
	body := prompt.Workflow() + "\n"

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if string(existing) == body {
			return path, actionUnchanged, nil
		}
		action = actionUpdated
	case errors.Is(err, os.ErrNotExist):
		action = actionWritten
	default:
		return path, "", fmt.Errorf("reading %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirsPerm); err != nil {
		return path, "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), filesPerm); err != nil {
		return path, "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, action, nil
}

// writeBlock puts block between the markers of the file at path, creating the file — with header
// ahead of the block — when it is not there.
//
// THE MARKERS ARE THE WHOLE MECHANISM. A block already present is REPLACED in place, so re-running
// `connect` never stacks a second copy, and `disconnect` can lift it back out without a human
// editing the repository's own doctrine file by hand.
//
// What is guaranteed on the way back is a file that ends with the content it had and one newline.
// A file that ended with several blank lines comes back with one — recorded here rather than
// pretended away, because the alternative is remembering the user's trailing whitespace forever.
func writeBlock(path, header, block string) (action writeAction, err error) {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(existing)

	var updated string
	switch start, end := blockBounds(content); {
	case start >= 0:
		updated = content[:start] + block + content[end:]
		if updated == content {
			return actionUnchanged, nil
		}
		action = actionUpdated
	case content == "":
		updated = header + block + "\n"
		action = actionWritten
	default:
		separator := "\n"
		if !strings.HasSuffix(content, "\n") {
			separator = "\n\n"
		}
		updated = content + separator + block + "\n"
		action = actionUpdated
	}

	if err := os.MkdirAll(filepath.Dir(path), dirsPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(updated), filesPerm); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return action, nil
}

// removeBlock takes our block back out of path, and removes the file entirely when the block — and
// the header we wrote ahead of it — were all it ever held.
//
// A `.cursor/rules/flowlio.mdc` we created is ours to delete; a `CLAUDE.md` that existed before is
// not. The header has to be passed in for that judgement to be possible: what is left of a file we
// created is not empty, it is the front-matter Cursor requires, and a file holding nothing but our
// own front-matter is still a file nobody asked for.
func removeBlock(path, header string) (action writeAction, err error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return actionAbsent, nil
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(existing)
	start, end := blockBounds(content)
	if start < 0 {
		return actionAbsent, nil
	}

	before := strings.TrimRight(content[:start], "\n")
	after := strings.TrimLeft(content[end:], "\n")

	// Nothing of the user's left: the file is one we created, and leaving it behind would make
	// `git status` dirty for no reason at all.
	ours := strings.TrimSpace(header)
	if remainder := strings.TrimSpace(before + "\n" + after); remainder == "" || remainder == ours {
		if err := os.Remove(path); err != nil {
			return "", fmt.Errorf("removing %s: %w", path, err)
		}
		return actionRemoved, nil
	}

	updated := before + "\n"
	if after != "" {
		updated += after
	}
	if err := os.WriteFile(path, []byte(updated), filesPerm); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return actionRemoved, nil
}

// blockBounds yields the byte range of our block in content, or -1 when there is none.
//
// An opening marker with no closing one is NOT a block. Treating it as one would mean rewriting
// everything from the truncation to the end of the file — that is, deleting whatever the user
// wrote after the point where our block was mangled. A whole block is appended instead, and the
// half-written one stays where it is for a human to look at.
func blockBounds(content string) (start, end int) {
	start = strings.Index(content, prompt.MarkerStart)
	if start < 0 {
		return -1, -1
	}
	end = strings.Index(content[start:], prompt.MarkerEnd)
	if end < 0 {
		return -1, -1
	}
	return start, start + end + len(prompt.MarkerEnd)
}
