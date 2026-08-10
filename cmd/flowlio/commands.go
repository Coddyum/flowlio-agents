package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                          | Ligne |
// |-------------|-----------------------------------------------------------------|-------|
// | teamFlag    | Adds the --team option shared by the admin commands               | 34    |
// | teamQuery   | Builds the ?team=<slug> parameter when one is given               | 39    |
// | runWhoami   | Prints the identity of the current token                          | 47    |
// | runTeam     | Subcommands for managing teams                                    | 73    |
// | runProject  | Subcommands for managing projects                                 | 112   |
// | runToken    | Subcommands for managing agent tokens                             | 161   |
// | splitFlags  | Separates flags from positional arguments, in any order           | 237   |
// | printToken  | Prints a freshly issued token, with the warning it deserves       | 261   |
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

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

const workspaceAPI = "/api/workspace"

// teamFlag declares the --team option, mandatory with an admin token, ignored with a project
// token, which is locked into its team anyway.
func teamFlag(fs *flag.FlagSet) *string {
	return fs.String("team", "", "target team slug (required with an admin token)")
}

// teamQuery builds the matching query parameter.
func teamQuery(slug string) string {
	if slug == "" {
		return ""
	}
	return "?team=" + url.QueryEscape(slug)
}

// runWhoami prints the identity of the current token: the first thing an agent asks for.
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
		fmt.Printf("scope:   %s (no team)\n", out.Scope)
		return nil
	}
	fmt.Printf("scope:   %s\nteam:    %s (%s)\n", out.Scope, out.TeamSlug, out.TeamName)
	if out.ProjectKey != "" {
		fmt.Printf("project: %s (%s)\n", out.ProjectKey, out.ProjectName)
	}
	return nil
}

// runTeam handles creating and listing teams.
func runTeam(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio team create <slug> <name> | flowlio team list")
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch args[0] {
	case "create":
		if len(args) < 3 {
			return errors.New("usage: flowlio team create <slug> <name>")
		}
		var team service.Team
		in := service.CreateTeamInput{Slug: args[1], Name: args[2]}
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/teams", in, &team); err != nil {
			return err
		}
		fmt.Printf("team created: %s (%s)\n", team.Slug, team.Name)
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
		return fmt.Errorf("unknown team subcommand: %s", args[0])
	}
}

// runProject handles creating and listing the projects of a team.
func runProject(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio project create <KEY> <name> | flowlio project list")
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
			return errors.New("usage: flowlio project create <KEY> <name> [--team slug]")
		}
		var project service.Project
		in := service.CreateProjectInput{Key: positional[0], Name: positional[1]}
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/projects"+teamQuery(*team), in, &project); err != nil {
			return err
		}
		fmt.Printf("project created: %s (%s)\n", project.Key, project.Name)
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
		return fmt.Errorf("unknown project subcommand: %s", sub)
	}
}

// runToken handles issuing, listing and revoking agent tokens.
func runToken(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio token create <KEY> <name> | list <KEY> | revoke <id>")
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
			return errors.New("usage: flowlio token create <KEY> <name> [--team slug]")
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
			state := "active"
			if t.Revoked {
				state = "revoked"
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
		fmt.Println("token revoked")
		return nil

	default:
		return fmt.Errorf("unknown token subcommand: %s", sub)
	}
}

// splitFlags separates flags from positional arguments, whatever their order: neither an agent nor
// a human in a hurry should have to guess that --team goes before the key.
//
// It lived in init.go until that command became a shim. It belongs here: every command in this file
// leans on it.
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

// printToken prints an issued token. This is the only chance to read it: the server keeps nothing
// but a hash.
//
// `flowlio token create` is now the ONLY command that shows a secret, and it is explicitly a
// request for one. `setup` and `connect` file theirs in 0600 and print a path — a token nobody sees
// is a token nobody pastes into a repository.
func printToken(created service.CreatedToken) {
	fmt.Printf("\ntoken %q for repo %s — shown once:\n\n    %s\n\n",
		created.Name, created.ProjectKey, created.Secret)
	fmt.Printf("A repository set up with `flowlio connect` needs none of this: its token is filed in\n" +
		"the configuration directory, and the .mcp.json carries two names and no secret.\n")
}
