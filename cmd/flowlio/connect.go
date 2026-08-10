package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                          | Ligne |
// |-----------------|-----------------------------------------------------------------|-------|
// | connectPlan     | What this run is about to do, decided before anything is written  | 49    |
// | runConnect      | Makes the current repository operational, then checks that it is  | 64    |
// | planConnect     | Reads the repository and works out what there is to write         | 109   |
// | announcePlan    | Prints the plan, and asks the one question there is to ask        | 139   |
// | printPlan       | Describes the run in the order the writes happen                  | 148   |
// | writeOurs       | Writes the two files that are ours, without asking                | 180   |
// | writeTheirs     | Writes into the user's files, or prints what it would have        | 211   |
// | indent          | Prefixes every line, so a quoted block is not read as output      | 237   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio connect <REPO>` makes the repository it is run from operational: an agent opened in it
// afterwards finds its board, its workflow and its inbox with nothing else to do.
//
// FOUR TARGETS, TWO OWNERS. `.mcp.json` and `.flowlio/workflow.md` are ours and are written without
// asking. The pointer in the repository's entry file and the hook in `.claude/settings.json` are
// the user's files, so they cost ONE question — one, not two, because a user asked twice stops
// reading the second.
//
// ONE CODE PATH, FOUR WAYS IN: a yes, a no, `--yes`, and a non-interactive session. Only the value
// of one boolean differs; what is not written is PRINTED, so the fallback is always a correct set
// of instructions rather than a silence.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// connectPlan is what a run is about to do, worked out before a single byte is written.
//
// Decided up front so the consent block and the writes cannot disagree: a question that lists
// something the writes then skip is worse than no question.
type connectPlan struct {
	// dir is the repository root — the working directory, because that is what the user means by
	// "this repository".
	dir string
	// creds is the credential the repository works under, already filed on this host.
	creds credentials.RepoFile
	// entries are the agent clients this repository shows signs of. Empty is a normal outcome.
	entries []entryFile
	// hook says whether `.claude/` is there. We never create it: that would presume a client.
	hook bool
	// legacy says whether an entry we wrote under our old name is still in the .mcp.json.
	legacy bool
}

// runConnect makes the current repository operational, then checks that it is.
func runConnect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	project := fs.String("project", "", "project slug, when the repo key alone does not name one")
	yes := fs.Bool("yes", false, "write into this repository's own files without asking")

	positional, err := splitFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: flowlio connect <REPO> [--project <slug>] [--yes]")
	}
	repo := strings.ToUpper(strings.TrimSpace(positional[0]))

	plan, err := planConnect(ctx, *project, repo)
	if err != nil {
		return err
	}

	// The consent decision is taken BEFORE any write, and it is the only thing the four ways in
	// disagree about.
	touchTheirs := *yes
	if !*yes && isInteractive(os.Stdin) {
		touchTheirs, err = announcePlan(os.Stdin, os.Stdout, plan)
		if err != nil {
			return err
		}
	} else {
		printPlan(os.Stdout, plan)
	}

	if err := offerLegacyRemoval(os.Stdin, os.Stdout, plan, *yes); err != nil {
		return err
	}
	if err := writeOurs(os.Stdout, plan); err != nil {
		return err
	}
	if err := writeTheirs(os.Stdout, plan, touchTheirs); err != nil {
		return err
	}

	return verifyConnection(ctx, os.Stdout, plan.creds)
}

// planConnect reads the repository and works out what there is to write.
func planConnect(ctx context.Context, project, repo string) (connectPlan, error) {
	dir, err := os.Getwd()
	if err != nil {
		return connectPlan{}, fmt.Errorf("current directory not found: %w", err)
	}

	creds, err := repoCredentials(ctx, project, repo)
	if err != nil {
		return connectPlan{}, err
	}

	legacy, err := mcpLegacyEntry(dir)
	if err != nil {
		return connectPlan{}, err
	}

	return connectPlan{
		dir:     dir,
		creds:   creds,
		entries: detectEntryFiles(dir),
		hook:    dirExists(filepath.Join(dir, ".claude")),
		legacy:  legacy,
	}, nil
}

// announcePlan prints the plan and asks the one question there is to ask.
//
// The default is YES: the user typed `flowlio connect`, the blocks are bounded, and `flowlio
// disconnect` takes them back out. A refusal is not a dead end either — it prints what it would
// have written.
func announcePlan(in io.Reader, out io.Writer, plan connectPlan) (bool, error) {
	printPlan(out, plan)
	if len(plan.entries) == 0 && !plan.hook {
		return false, nil
	}
	return askYesNo(in, out, "Continue?", true)
}

// printPlan describes the run in the order the writes happen.
func printPlan(out io.Writer, plan connectPlan) {
	_, _ = fmt.Fprintf(out, "\nConnecting %s to project %s.\n", plan.creds.Repo, plan.creds.Project)

	_, _ = fmt.Fprintln(out, "\n  Will write without asking:")
	_, _ = fmt.Fprintf(out, "    %-24s (merged, existing entries preserved)\n", mcpConfigName)
	_, _ = fmt.Fprintf(out, "    %-24s (workflow prompt, version %s)\n", prompt.WorkflowPath, prompt.Version)

	if len(plan.entries) == 0 && !plan.hook {
		_, _ = fmt.Fprintln(out, "\n  No agent client detected in this repository, so no pointer is written.")
		_, _ = fmt.Fprintf(out, "  Whatever your agent reads at the start of a session, add this to it:\n\n%s\n",
			indent(prompt.Pointer(), "    "))
		return
	}

	_, _ = fmt.Fprintln(out, "\n  Will add a bounded block to:")
	for _, entry := range plan.entries {
		_, _ = fmt.Fprintf(out, "    %-24s (%s)\n", entry.Path, entry.Client)
	}
	if plan.hook {
		_, _ = fmt.Fprintf(out, "    %-24s (inbox reminder, every %ds at most)\n",
			hookSettingsPath, hookIntervalSeconds)
	}

	_, _ = fmt.Fprintln(out)
}

// offerLegacyRemoval deals with the entry we wrote under our old name, and NEVER in silence.
//
// Two entries pointing at the same board is not a cosmetic problem: an agent merges its MCP servers
// by name, so both are launched, both expose `create_task`, and a write lands on whichever one the
// client happened to pick. But the old entry may have been adjusted by hand — an absolute path, a
// different port — so it is removed with a yes and not on our own authority.
func offerLegacyRemoval(in io.Reader, out io.Writer, plan connectPlan, yes bool) error {
	if !plan.legacy {
		return nil
	}

	_, _ = fmt.Fprintf(out, "\n  This repository declares a %q MCP server — the name we used before %q.\n",
		mcpLegacyServerKey, mcpServerKey)
	_, _ = fmt.Fprintln(out, "  Left in place, both are launched and a write lands on whichever the agent picked.")

	remove := yes
	if !yes && isInteractive(in) {
		answer, err := askYesNo(in, out, fmt.Sprintf("  Remove the %q entry?", mcpLegacyServerKey), true)
		if err != nil {
			return err
		}
		remove = answer
	}
	if !remove {
		_, _ = fmt.Fprintf(out, "  Left alone. Remove the %q entry from %s by hand when you have checked it.\n",
			mcpLegacyServerKey, mcpConfigName)
		return nil
	}

	if _, err := removeMCPEntry(plan.dir, mcpLegacyServerKey); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  %q entry removed.\n", mcpLegacyServerKey)
	return nil
}

// writeOurs writes the two files that are ours, without asking.
func writeOurs(out io.Writer, plan connectPlan) error {
	// Every path printed here is repository-relative, like the plan the user just read: an absolute
	// path in the middle of a list of relative ones reads as a different kind of thing.
	_, written, err := writeMCPConfig(plan.dir, plan.creds.Project, plan.creds.Repo)
	if err != nil {
		return err
	}
	if written {
		_, _ = fmt.Fprintf(out, "  %s written — committable as is, it holds no secret.\n", mcpConfigName)
	} else {
		_, _ = fmt.Fprintf(out, "  %s already carries a %q entry, left alone.\n", mcpConfigName, mcpServerKey)
	}

	_, action, err := writeWorkflowFile(plan.dir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  %s %s (version %s).\n", prompt.WorkflowPath, action, prompt.Version)
	return nil
}

// writeTheirs writes into the user's files, or prints exactly what it would have written.
//
// The printed form is not a consolation prize: it is the same block, and pasting it by hand gives
// the same result. That is what makes a refusal, and a session with no terminal, correct rather
// than merely handled.
func writeTheirs(out io.Writer, plan connectPlan, allowed bool) error {
	if len(plan.entries) == 0 && !plan.hook {
		return nil
	}

	if !allowed {
		_, _ = fmt.Fprintln(out, "\n  Not writing into this repository's own files. Add this block to "+
			"whatever your agent\n  reads at the start of a session:")
		_, _ = fmt.Fprintf(out, "\n%s\n", indent(prompt.Pointer(), "    "))
		return nil
	}

	for _, entry := range plan.entries {
		action, err := writeBlock(filepath.Join(plan.dir, entry.Path), entry.Header, prompt.Pointer())
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "  %s %s — pointer to %s.\n", entry.Path, action, prompt.WorkflowPath)
	}

	if plan.hook {
		_, action, err := writeInboxHook(plan.dir, plan.creds.Repo)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "  %s %s — inbox reminder.\n", hookSettingsPath, action)
	}
	return nil
}
