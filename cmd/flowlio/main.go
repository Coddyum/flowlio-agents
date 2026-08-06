package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | main      | Entry point of the CLI: dispatch and exit code                        | 34    |
// | run       | Routes the requested command to its implementation                    | 47    |
// | usage     | Prints the help                                                       | 88    |
// | newClient | Builds the API client from the local credentials or from the env      | 139   |
//
// Fin du sommaire.
// =====================================================================
//
// The CLI is the face of the product locally: it must make a project operational in under two
// minutes, without reading any documentation.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// main delegates to run and translates the error into an exit code: one single place decides the
// status.
//
// The general case is 1. A command needing another status carries it in an `exitError`
// (`watch.go`) instead of calling os.Exit itself: os.Exit unwinds no defer, and the day a command
// registers one, a call buried in a sub-function would be impossible to find.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio: %v\n", err)

		var exit *exitError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		os.Exit(1)
	}
}

// run routes the command to its implementation. Every command lives in its own file.
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	ctx := context.Background()

	switch args[0] {
	case "init":
		return runInit(ctx, args[1:])
	case "whoami":
		return runWhoami(ctx, args[1:])
	case "team":
		return runTeam(ctx, args[1:])
	case "project":
		return runProject(ctx, args[1:])
	case "token":
		return runToken(ctx, args[1:])
	case "trust":
		return runTrust(ctx, args[1:])
	case "task":
		return runTask(ctx, args[1:])
	case "memory":
		return runMemory(ctx, args[1:])
	case "watch":
		return runWatch(ctx, args[1:])
	case "show":
		return runShow(ctx, args[1:])
	case "mcp":
		return runMCP(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

// usage prints the CLI help.
func usage() {
	fmt.Print(`flowlio — project management for AI agents

Usage:
  flowlio init --team <slug> --project <KEY> [--team-name <name>] [--project-name <name>]
      Sets up a team, a project and an agent token in one command.

  flowlio whoami                       Identity of the current token
  flowlio team create <slug> <name>    Creates a team
  flowlio team list                    Lists the teams
  flowlio project create <KEY> <name>  Creates a project inside the team
  flowlio project list                 Lists the team's projects
  flowlio token create <KEY> <name>    Issues an agent token for a project
  flowlio token list <KEY>             Lists a project's tokens
  flowlio token revoke <id>            Revokes a token

  flowlio trust list                   Which project pairs may write to each other
  flowlio trust allow <A> <B>          Opens the issue channel between two projects
  flowlio trust deny <A> <B>           Closes it again (open threads are untouched)

  flowlio task list [--status s]       Backlog of the token's own project
  flowlio task show <KEY>              One task and its note thread
  flowlio task create <title>          Opens a task
  flowlio task status <KEY> <status>   todo | in_progress | blocked | done
  flowlio task note <KEY> <text>       Appends a progress note
  flowlio task archive <KEY>           Drops the task out of the active backlog

  flowlio memory list [--kind k]       What this repository remembers, newest first
  flowlio memory search <words>        Full-text search over the same entries
  flowlio memory show <slug>           One entry, whole, with what it replaced
  flowlio memory write <slug> <kind> <title> <body> [--supersedes a,b]
      decision | learning | state. An entry is never edited: a newer one supersedes it.

  flowlio watch [--follow]             The team's debt queue — empty means all is well
  flowlio show <REF>                   Detail of one row of the queue (e.g. CORE-41)

  flowlio mcp                          MCP server over stdio, for an agent

Common options:
  --team <slug>   Target team (required with an admin token)

Exit status:
  0  the command succeeded    1  error    2  non-admin token on watch/show

Environment:
  FLOWLIO_API_URL, FLOWLIO_TOKEN  Take precedence over the local credentials file
`)
}

// newClient builds the API client. Without credentials, the message says what to do rather than
// exposing a file path without context.
func newClient() (*client.Client, error) {
	c, err := client.FromCredentials(os.Getenv("FLOWLIO_API_URL"), os.Getenv("FLOWLIO_TOKEN"))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, credentials.ErrNotFound) {
		return nil, err
	}

	// No credentials on the host, but the instance holds its own. Adopting them is silent and
	// prompts nothing: this path is shared by every command, including the ones an agent runs in a
	// session with no terminal attached.
	adopted, adoptErr := adoptCredentials(context.Background(), execDocker)
	if adoptErr != nil {
		if errors.Is(adoptErr, errNoInstance) {
			return nil, errors.New("no credentials found — run `flowlio init` from the repository you want to track, " +
				"or set FLOWLIO_API_URL and FLOWLIO_TOKEN")
		}
		return nil, adoptErr
	}
	return client.New(adopted.APIURL, adopted.Token), nil
}
