package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | runAgent    | Sets or shows which agent the waker launches for this repository    | 34    |
// | repoForCwd  | Finds the connected repo whose filed path is the working directory  | 88    |
//
// Fin du sommaire.
// =====================================================================
//
// WHICH AGENT THE WAKER LAUNCHES (DESIGN-WAKE §4.2). A per-repository choice, set from inside the
// repository so there is nothing to name: the working directory identifies the repo, exactly as
// `connect` captured it. The presets carry the exact headless invocation, so a user types a name,
// not a command line — `set-custom` is the escape hatch for a tool no preset covers.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	"github.com/Coddyum/flowlio-agents/internal/pkg/waker"
)

// runAgent sets or shows the agent this repository's waker launches.
//
//	flowlio agent                     shows the current choice
//	flowlio agent set <name>          claude | codex | opencode
//	flowlio agent set-custom <cmd>    an arbitrary template, {prompt} injected
func runAgent(_ context.Context, args []string) error {
	rf, err := repoForCwd()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		name := rf.Agent
		if rf.AgentCommand != "" {
			name = "custom: " + rf.AgentCommand
		}
		if name == "" {
			name = "claude (default)"
		}
		fmt.Printf("%s launches: %s\n", rf.Repo, name)
		return nil
	}

	switch args[0] {
	case "set":
		if len(args) < 2 {
			return errors.New("usage: flowlio agent set <claude|codex|opencode>")
		}
		if _, ok := waker.Preset(args[1]); !ok {
			return fmt.Errorf("unknown agent %q — known presets: claude, codex, opencode "+
				"(for anything else, flowlio agent set-custom \"<command> {prompt}\")", args[1])
		}
		rf.Agent = strings.ToLower(args[1])
		rf.AgentCommand = ""

	case "set-custom":
		if len(args) < 2 {
			return errors.New(`usage: flowlio agent set-custom "<command> {prompt}"`)
		}
		template := strings.Join(args[1:], " ")
		if _, ok := waker.Custom(template); !ok {
			return fmt.Errorf("unusable command template %q", template)
		}
		rf.Agent = ""
		rf.AgentCommand = template

	default:
		return fmt.Errorf("unknown agent subcommand: %s", args[0])
	}

	if _, err := credentials.SaveRepo(rf); err != nil {
		return err
	}
	fmt.Printf("%s will launch %s on a wake\n", rf.Repo, args[len(args)-1])
	return nil
}

// repoForCwd finds the connected repository whose filed path is the current working directory. It is
// how every per-repo command here avoids asking the user to name what the directory already says.
func repoForCwd() (credentials.RepoFile, error) {
	wd, err := os.Getwd()
	if err != nil {
		return credentials.RepoFile{}, fmt.Errorf("reading the working directory: %w", err)
	}
	rf, err := repoForDir(wd)
	if err != nil {
		return credentials.RepoFile{}, errors.New("this directory is not a connected repository — " +
			"run `flowlio connect <REPO>` from its root first")
	}
	return rf, nil
}
