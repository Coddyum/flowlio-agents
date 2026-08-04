package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | main      | Point d'entrée de la CLI : dispatch et code de sortie                 | 33    |
// | run       | Route la commande demandée vers son implémentation                    | 46    |
// | usage     | Affiche l'aide                                                        | 85    |
// | newClient | Construit le client API à partir des identifiants locaux ou de l'env  | 130   |
//
// Fin du sommaire.
// =====================================================================
//
// La CLI est le visage du produit en local : elle doit rendre un projet opérationnel en moins
// de deux minutes, sans lire de documentation.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// main délègue à run et traduit l'erreur en code de sortie : un seul endroit décide du statut.
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

// run route la commande vers son implémentation. Chaque commande vit dans son propre fichier.
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
		return fmt.Errorf("commande inconnue: %s", args[0])
	}
}

// usage affiche l'aide de la CLI.
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

// newClient construit le client API. Sans identifiants, le message dit quoi faire plutôt que
// d'exposer un chemin de fichier sans contexte.
func newClient() (*client.Client, error) {
	c, err := client.FromCredentials(os.Getenv("FLOWLIO_API_URL"), os.Getenv("FLOWLIO_TOKEN"))
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, errors.New("aucun identifiant trouvé — démarrer le serveur une première fois, " +
				"ou renseigner FLOWLIO_API_URL et FLOWLIO_TOKEN")
		}
		return nil, err
	}
	return c, nil
}
