package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                        | Ligne |
// |-------------------|---------------------------------------------------------------|-------|
// | runDisconnect     | Takes the Flowlio configuration back out of this repository     | 50    |
// | disconnectRepo    | The same work on a named directory, so a test can play it       | 64    |
// | removeWorkflowFile| Removes .flowlio/workflow.md, and the directory when it empties | 115   |
//
// Fin du sommaire.
// =====================================================================
//
// THE EXACT INVERSE OF `connect`, AND NO NETWORK CALL AT ALL.
//
// Nothing here asks the instance anything: what is undone was written locally, and a repository has
// to be disconnectable when the instance is gone — which is one of the reasons somebody reaches for
// this command.
//
// It is the markers that make it possible. Without them, disconnecting would mean telling a human
// to open the repository's own doctrine file and delete the right lines by hand, which is how a
// half-deleted block ends up committed.
//
// THE TOKEN IS NOT TOUCHED. `disconnect` is repository-side; the credential is host-side and still
// opens a board this repository may be reconnected to in a minute. Deleting it is
// `flowlio remove`'s job, and it is a different decision.
//
// WHAT COMES BACK EXACTLY, AND WHAT COMES BACK NORMALISED. Every text file is restored byte for
// byte — `CLAUDE.md` is the repository's own doctrine, and a stray newline there is a diff somebody
// has to explain. The two JSON files are decoded, edited and re-encoded, so `connect` re-indents
// them once into encoding/json's shape; nothing is lost, and a second cycle changes nothing at all.
// Both properties are pinned in disconnect_test.go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// runDisconnect takes the Flowlio configuration back out of the repository it is run from.
//
// It takes no argument: everything it removes is identified by a constant — the entry key in the
// `.mcp.json`, the path of the workflow file, the markers, the hook's stamp prefix. Asking for the
// repo key would be asking the user for something we do not need.
func runDisconnect(_ context.Context, args []string) error {
	if len(args) > 0 {
		return errors.New("usage: flowlio disconnect   (run from the root of the repository)")
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory not found: %w", err)
	}
	return disconnectRepo(dir, os.Stdout)
}

// disconnectRepo does the work on a named directory, so the round trip with `connect` can be played
// in a test without moving the process into a temporary directory.
func disconnectRepo(dir string, out io.Writer) error {
	removed, err := removeMCPEntry(dir, mcpServerKey)
	if err != nil {
		return err
	}
	if removed {
		_, _ = fmt.Fprintf(out, "  %s — %q entry removed, the others left alone.\n", mcpConfigName, mcpServerKey)
	} else {
		_, _ = fmt.Fprintf(out, "  %s — no %q entry to remove.\n", mcpConfigName, mcpServerKey)
	}

	if action, err := removeWorkflowFile(dir); err != nil {
		return err
	} else {
		_, _ = fmt.Fprintf(out, "  %s %s.\n", prompt.WorkflowPath, action)
	}

	// The same detection `connect` used, so the blocks come out of exactly the files they went into.
	for _, entry := range detectEntryFiles(dir) {
		action, err := removeBlock(filepath.Join(dir, entry.Path), entry.Header)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "  %s %s.\n", entry.Path, action)
	}

	// The path printed is the repository-relative one, like every other line above: an absolute path
	// in the middle of a list of relative ones reads as a different kind of thing.
	_, action, err := removeInboxHook(dir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  %s %s.\n", hookSettingsPath, action)

	if _, sessionAction, err := removeSessionHook(dir); err != nil {
		return err
	} else if sessionAction != actionAbsent {
		_, _ = fmt.Fprintf(out, "  %s %s (session capture).\n", hookSettingsPath, sessionAction)
	}

	_, _ = fmt.Fprintln(out, "\nThis repository is disconnected. Its token is still filed on this host, so\n"+
		"`flowlio connect` puts everything back without issuing a new one.")
	return nil
}

// removeWorkflowFile removes the workflow prompt, and the directory with it when nothing else was
// put there.
//
// The directory is ours — `connect` created it — but only while it holds nothing else. Somebody may
// have filed their own notes next to the prompt, and taking a directory away because we made it is
// how a command deletes something it never wrote.
func removeWorkflowFile(dir string) (writeAction, error) {
	path := filepath.Join(dir, prompt.WorkflowPath)

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return actionAbsent, nil
		}
		return "", fmt.Errorf("removing %s: %w", path, err)
	}

	parent := filepath.Dir(path)
	entries, err := os.ReadDir(parent)
	if err == nil && len(entries) == 0 {
		if err := os.Remove(parent); err != nil {
			return "", fmt.Errorf("removing %s: %w", parent, err)
		}
	}
	return actionRemoved, nil
}
