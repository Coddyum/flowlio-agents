package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | main      | Entry point of the CLI: dispatch and exit code                        | 34    |
// | run       | Routes the requested command to its implementation                    | 51    |
// | usage     | Prints the help                                                       | 115   |
// | newClient | Builds the API client from the local credentials or from the env      | 186   |
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
//
// BARE `flowlio` RUNS EVERYTHING (DESIGN-WAKE §4.1): self-host brings up the database, the engine and
// the waker; hosted brings up the waker alone. The help is one word away — `flowlio help` — because
// the common gesture after `brew install flowlio` is to run it, not to read it.
func run(args []string) error {
	if len(args) == 0 {
		return runUp(context.Background(), nil)
	}

	ctx := context.Background()

	switch args[0] {
	case "setup":
		return runSetup(ctx, args[1:])
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
	case "connect":
		return runConnect(ctx, args[1:])
	case "remove":
		return runRemove(ctx, args[1:])
	case "disconnect":
		return runDisconnect(ctx, args[1:])
	case "doctor":
		return runDoctor(ctx, args[1:])
	case "mcp":
		return runMCP(ctx, args[1:])
	case "waker":
		mode, err := detectMode()
		if err != nil {
			return err
		}
		return runWaker(ctx, mode)
	case "agent":
		return runAgent(ctx, args[1:])
	case "session-start":
		return runSessionStart(ctx, args[1:])
	case "login":
		return runLogin(ctx, args[1:])
	case "version", "--version", "-v":
		return runVersion(args[1:])
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

Running everything (after brew install flowlio):
  flowlio                              Runs everything: self-host = DB container + engine + waker;
                                       hosted = the waker only
  flowlio login <prod-url> [token]     Link this machine to a hosted account; flowlio then runs hosted
  flowlio agent set <name>             Which agent the waker launches: claude | codex | opencode

Setting up:
  flowlio setup                        Creates a project, its repos and one token each
  flowlio connect <REPO>               Makes the current repository operational (self-host)
  flowlio connect <REPO> --id <id>     Hosted: links this dir to a flowlio.me repository for the waker
  flowlio doctor                       Checks that it really is, and says what is not
  flowlio disconnect                   Takes the configuration back out of this repository
  flowlio remove <REPO>                Deletes a repo on the instance
  flowlio remove --project <slug>      Deletes a project and everything in it

A project holds repos, and a repo is one git repository with one agent. setup asks for
both and prints one connect line per repo; run each from that repository's root.

  flowlio whoami                       Identity of the current token
  flowlio team create <slug> <name>    Creates a team (a project, in this CLI's words)
  flowlio team list                    Lists the teams
  flowlio project create <KEY> <name>  Creates a project (a repo, in this CLI's words)
  flowlio project list                 Lists them
  flowlio token create <KEY> <name>    Issues an agent token, and prints it once
  flowlio token list <KEY>             Lists a repo's tokens
  flowlio token revoke <id>            Revokes a token

  flowlio trust list                   Which project may raise issues AT which
  flowlio trust allow <from> <to>      Lets <from> raise issues at <to>, that way only
  flowlio trust deny <from> <to>       Cuts that one direction (open threads untouched)

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
  flowlio waker                        Watches for cross-repo answers and relaunches the agent
  flowlio version                      Which release this binary is, for a bug report

  flowlio init                         Gone — see setup and connect above

Common options:
  --team <slug>   Target team (required with an admin token)

Exit status:
  0  the command succeeded    1  error    2  non-admin token on watch/show

Environment:
  FLOWLIO_API_URL, FLOWLIO_TOKEN     An explicit address and token, ahead of everything else
  FLOWLIO_PROJECT, FLOWLIO_REPO      What connect writes into a repository's .mcp.json;
                                     the token is read from the configuration directory
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
			return nil, errors.New("no credentials found — run `flowlio setup` to create a project " +
				"and its repositories, or set FLOWLIO_API_URL and FLOWLIO_TOKEN")
		}
		return nil, adoptErr
	}
	return client.New(adopted.APIURL, adopted.Token), nil
}
