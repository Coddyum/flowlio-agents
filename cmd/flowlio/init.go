package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                           | Ligne |
// |------------|------------------------------------------------------------------|-------|
// | runInit    | Prépare team, projet et token d'agent en une seule commande        | 33    |
// | announceMCPConfig | Écrit la configuration MCP du dépôt et dit ce qui s'est passé | 96    |
// | ensure     | Exécute une création en tolérant l'existence préalable             | 117   |
// | splitFlags | Sépare options et arguments positionnels, dans n'importe quel ordre| 134   |
// | printToken | Affiche un token fraîchement émis, avec l'avertissement qui va avec| 154   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runInit prépare tout ce qu'il faut à un repo pour être suivi : la team si elle manque, le
// projet si il manque, puis un token d'agent.
//
// La commande est réexécutable : une team ou un projet déjà présents ne sont pas une erreur.
// Seul le token est systématiquement neuf — un secret ne se relit pas, il se réémet.
func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	team := fs.String("team", "", "slug de la team (ex: omiros)")
	teamName := fs.String("team-name", "", "nom lisible de la team (défaut: le slug)")
	project := fs.String("project", "", "clé du projet (ex: CORE)")
	projectName := fs.String("project-name", "", "nom lisible du projet (défaut: la clé)")
	tokenName := fs.String("token-name", "agent", "nom du token émis")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *team == "" || *project == "" {
		return errors.New("usage: flowlio init --team <slug> --project <KEY> [--team-name <nom>] [--project-name <nom>]")
	}
	if *teamName == "" {
		*teamName = *team
	}
	if *projectName == "" {
		*projectName = *project
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	if err := ensure(func() error {
		in := service.CreateTeamInput{Slug: *team, Name: *teamName}
		return c.Do(ctx, http.MethodPost, workspaceAPI+"/teams", in, nil)
	}, "team "+*team); err != nil {
		return err
	}

	if err := ensure(func() error {
		in := service.CreateProjectInput{Key: *project, Name: *projectName}
		return c.Do(ctx, http.MethodPost, workspaceAPI+"/projects"+teamQuery(*team), in, nil)
	}, "projet "+*project); err != nil {
		return err
	}

	var created service.CreatedToken
	in := service.CreateTokenInput{ProjectKey: *project, Name: *tokenName}
	if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/tokens"+teamQuery(*team), in, &created); err != nil {
		return err
	}

	fmt.Printf("team %s et projet %s prêts.\n", *team, *project)

	// Le .mcp.json est écrit AVANT l'affichage du token : c'est lui qui rend l'agent
	// opérationnel, et le token affiché n'a de sens qu'une fois qu'on sait où il sera lu.
	if err := announceMCPConfig(c.BaseURL()); err != nil {
		return err
	}

	printToken(created)
	return nil
}

// announceMCPConfig écrit la configuration MCP du dépôt courant et dit ce qui s'est passé.
//
// Un échec d'écriture N'ANNULE PAS l'init : la team, le projet et le token existent déjà côté
// serveur, et le token est sur le point d'être affiché pour la seule et unique fois. Avorter ici
// le perdrait. Le défaut est donc signalé, et la commande continue.
func announceMCPConfig(apiURL string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("répertoire courant introuvable: %w", err)
	}

	path, written, err := writeMCPConfig(dir, apiURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowlio: configuration MCP non écrite: %v\n", err)
		return nil
	}
	if written {
		fmt.Printf("%s écrit — commitable tel quel, il ne contient aucun secret.\n", path)
	} else {
		fmt.Printf("%s porte déjà une entrée %q, conservée.\n", path, mcpServerKey)
	}
	return nil
}

// ensure exécute une création et tolère un conflit : la ressource existait déjà, ce qui est le
// résultat voulu. Toute autre erreur remonte.
func ensure(create func() error, label string) error {
	err := create()
	if err == nil {
		fmt.Printf("%s créé.\n", label)
		return nil
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		fmt.Printf("%s existe déjà, conservé.\n", label)
		return nil
	}
	return err
}

// splitFlags sépare les options des arguments positionnels, quel que soit leur ordre : un agent
// ou un humain pressé ne doit pas avoir à deviner que --team se met avant la clé.
func splitFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args

	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	return positional, nil
}

// printToken affiche un token émis. C'est la seule occasion de le lire : le serveur n'en garde
// qu'un hash.
func printToken(created service.CreatedToken) {
	// La ligne est donnée prête à coller, parce que c'est exactement ce que l'utilisateur va en
	// faire : le .mcp.json référence ${FLOWLIO_TOKEN}, il faut donc que la variable existe.
	fmt.Printf("\ntoken %q pour le projet %s — affiché une seule fois, à coller tel quel :\n\n    export FLOWLIO_TOKEN=%s\n\n",
		created.Name, created.ProjectKey, created.Secret)
	fmt.Println("Jamais dans le dépôt : le .mcp.json ne porte que la référence à cette variable.")
}
