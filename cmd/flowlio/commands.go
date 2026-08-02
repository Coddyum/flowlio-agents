package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                          | Ligne |
// |-------------|-----------------------------------------------------------------|-------|
// | teamFlag    | Ajoute l'option --team commune aux commandes admin                | 32    |
// | teamQuery   | Construit le paramètre ?team=<slug> quand il est renseigné        | 37    |
// | runWhoami   | Affiche l'identité du token courant                               | 45    |
// | runTeam     | Sous-commandes de gestion des teams                               | 71    |
// | runProject  | Sous-commandes de gestion des projets                             | 110   |
// | runToken    | Sous-commandes de gestion des tokens d'agent                      | 159   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
)

const workspaceAPI = "/api/workspace"

// teamFlag déclare l'option --team, obligatoire avec un token admin, ignorée avec un token de
// projet qui est de toute façon enfermé dans sa team.
func teamFlag(fs *flag.FlagSet) *string {
	return fs.String("team", "", "slug de la team (obligatoire avec un token admin)")
}

// teamQuery construit le paramètre de requête correspondant.
func teamQuery(slug string) string {
	if slug == "" {
		return ""
	}
	return "?team=" + url.QueryEscape(slug)
}

// runWhoami affiche l'identité du token courant : la première chose qu'un agent demande.
func runWhoami(ctx context.Context, _ []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}

	var out struct {
		Scope string `json:"scope"`
		service.Identity
	}
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &out); err != nil {
		return err
	}

	if out.TeamSlug == "" {
		fmt.Printf("portée : %s (aucune team)\n", out.Scope)
		return nil
	}
	fmt.Printf("portée : %s\nteam   : %s (%s)\n", out.Scope, out.TeamSlug, out.TeamName)
	if out.ProjectKey != "" {
		fmt.Printf("projet : %s (%s)\n", out.ProjectKey, out.ProjectName)
	}
	return nil
}

// runTeam gère la création et le listing des teams.
func runTeam(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio team create <slug> <nom> | flowlio team list")
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch args[0] {
	case "create":
		if len(args) < 3 {
			return errors.New("usage: flowlio team create <slug> <nom>")
		}
		var team service.Team
		in := service.CreateTeamInput{Slug: args[1], Name: args[2]}
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/teams", in, &team); err != nil {
			return err
		}
		fmt.Printf("team créée : %s (%s)\n", team.Slug, team.Name)
		return nil

	case "list":
		var teams []service.Team
		if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/teams", nil, &teams); err != nil {
			return err
		}
		for _, t := range teams {
			fmt.Printf("%-20s %s\n", t.Slug, t.Name)
		}
		return nil

	default:
		return fmt.Errorf("sous-commande team inconnue: %s", args[0])
	}
}

// runProject gère la création et le listing des projets d'une team.
func runProject(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio project create <KEY> <nom> | flowlio project list")
	}

	fs := flag.NewFlagSet("project", flag.ContinueOnError)
	team := teamFlag(fs)

	sub := args[0]
	rest := args[1:]
	positional, err := splitFlags(fs, rest)
	if err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch sub {
	case "create":
		if len(positional) < 2 {
			return errors.New("usage: flowlio project create <KEY> <nom> [--team slug]")
		}
		var project service.Project
		in := service.CreateProjectInput{Key: positional[0], Name: positional[1]}
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/projects"+teamQuery(*team), in, &project); err != nil {
			return err
		}
		fmt.Printf("projet créé : %s (%s)\n", project.Key, project.Name)
		return nil

	case "list":
		var projects []service.Project
		if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(*team), nil, &projects); err != nil {
			return err
		}
		for _, p := range projects {
			fmt.Printf("%-12s %s\n", p.Key, p.Name)
		}
		return nil

	default:
		return fmt.Errorf("sous-commande project inconnue: %s", sub)
	}
}

// runToken gère l'émission, le listing et la révocation des tokens d'agent.
func runToken(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio token create <KEY> <nom> | list <KEY> | revoke <id>")
	}

	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	team := teamFlag(fs)

	sub := args[0]
	positional, err := splitFlags(fs, args[1:])
	if err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch sub {
	case "create":
		if len(positional) < 2 {
			return errors.New("usage: flowlio token create <KEY> <nom> [--team slug]")
		}
		in := service.CreateTokenInput{ProjectKey: positional[0], Name: positional[1]}
		var created service.CreatedToken
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/tokens"+teamQuery(*team), in, &created); err != nil {
			return err
		}
		printToken(created)
		return nil

	case "list":
		if len(positional) < 1 {
			return errors.New("usage: flowlio token list <KEY> [--team slug]")
		}
		query := teamQuery(*team)
		separator := "?"
		if query != "" {
			separator = "&"
		}
		var tokens []service.TokenInfo
		path := workspaceAPI + "/tokens" + query + separator + "project=" + url.QueryEscape(positional[0])
		if err := c.Do(ctx, http.MethodGet, path, nil, &tokens); err != nil {
			return err
		}
		for _, t := range tokens {
			state := "actif"
			if t.Revoked {
				state = "révoqué"
			}
			fmt.Printf("%s  %-20s %-8s %s\n", t.ID, t.Name, state, t.Prefix)
		}
		return nil

	case "revoke":
		if len(positional) < 1 {
			return errors.New("usage: flowlio token revoke <id> [--team slug]")
		}
		path := workspaceAPI + "/tokens/" + url.PathEscape(positional[0]) + teamQuery(*team)
		if err := c.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
			return err
		}
		fmt.Println("token révoqué")
		return nil

	default:
		return fmt.Errorf("sous-commande token inconnue: %s", sub)
	}
}
