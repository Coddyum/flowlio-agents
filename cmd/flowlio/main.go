package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément   | Résumé                                                              | Ligne |
// |-----------|---------------------------------------------------------------------|-------|
// | main      | Point d'entrée de la CLI : dispatch et code de sortie                 | 29    |
// | run       | Route la commande demandée vers son implémentation                    | 37    |
// | usage     | Affiche l'aide                                                        | 72    |
// | newClient | Construit le client API à partir des identifiants locaux ou de l'env  | 111   |
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
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio: %v\n", err)
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
	fmt.Print(`flowlio — gestion de projets pour agents IA

Usage :
  flowlio init --team <slug> --project <KEY> [--team-name <nom>] [--project-name <nom>]
      Prépare une team, un projet et un token d'agent en une commande.

  flowlio whoami                       Identité du token courant
  flowlio team create <slug> <nom>     Crée une team
  flowlio team list                    Liste les teams
  flowlio project create <KEY> <nom>   Crée un projet dans la team
  flowlio project list                 Liste les projets de la team
  flowlio token create <KEY> <nom>     Émet un token d'agent pour un projet
  flowlio token list <KEY>             Liste les tokens d'un projet
  flowlio token revoke <id>            Révoque un token

  flowlio trust list                   Quelles paires de projets peuvent s'écrire
  flowlio trust allow <A> <B>          Ouvre le canal d'issues entre deux projets
  flowlio trust deny <A> <B>           Le referme (n'affecte pas les fils ouverts)

  flowlio task list [--status s]       Backlog du projet du token
  flowlio task show <CLÉ>              Une tâche et son fil de notes
  flowlio task create <titre>          Ouvre une tâche
  flowlio task status <CLÉ> <statut>   todo | in_progress | blocked | done
  flowlio task note <CLÉ> <texte>      Ajoute une note de progression
  flowlio task archive <CLÉ>           Sort la tâche du backlog actif

  flowlio mcp                          Serveur MCP sur stdio, pour un agent

Options communes :
  --team <slug>   Team visée (obligatoire avec un token admin)

Environnement :
  FLOWLIO_API_URL, FLOWLIO_TOKEN  Priment sur le fichier d'identifiants local
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
