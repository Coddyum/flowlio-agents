package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                       | Ligne |
// |-----------------------|--------------------------------------------------------------|-------|
// | runInit               | Prepares team, project and agent token in a single command     | 35    |
// | announceTrustIsClosed | Warns that no trust is declared, on the 2nd project            | 143   |
// | announceMCPConfig     | Writes the repo's MCP config and says what happened            | 196   |
// | ensure                | Runs a creation, tolerating that it already exists             | 217   |
// | splitFlags            | Separates flags from positional arguments, in any order        | 234   |
// | printToken            | Prints a freshly issued token, with the warning it deserves    | 254   |
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
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runInit prepares everything a repo needs in order to be tracked: the team if it is missing, the
// project if it is missing, then an agent token.
//
// The command is re-runnable: a team or a project that already exists is not an error. Only the
// token is always new — a secret is not read back, it is reissued.
func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	team := fs.String("team", "", "team slug (e.g. omiros)")
	teamName := fs.String("team-name", "", "human-readable team name (default: the slug)")
	project := fs.String("project", "", "project key (e.g. CORE)")
	projectName := fs.String("project-name", "", "human-readable project name (default: the key)")
	tokenName := fs.String("token-name", "agent", "name of the issued token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *team == "" || *project == "" {
		return errors.New("usage: flowlio init --team <slug> --project <KEY> [--team-name <name>] [--project-name <name>]")
	}
	if *teamName == "" {
		*teamName = *team
	}
	if *projectName == "" {
		*projectName = *project
	}

	c, err := newClient()
	if err != nil {
		// `flowlio init` is the command a user reaches for before anything exists, and the only
		// interactive one. It is therefore the ONLY place allowed to offer to start the stack:
		// every other command fails with an explanation instead, because an agent runs them with
		// no terminal to answer from.
		//
		// An instance that IS running and still left newClient failing is a different problem —
		// unreadable credentials, a broken daemon — and starting a second stack would not fix it.
		if !isInteractive(os.Stdin) || instanceIsRunning(ctx, execDocker) {
			return err
		}
		if err := offerToStartStack(ctx, execDocker, os.Stdin, os.Stdout); err != nil {
			return err
		}

		waitCtx, cancel := context.WithTimeout(ctx, instanceReadyTimeout)
		defer cancel()
		adopted, waitErr := waitForCredentials(waitCtx, execDocker, credentialsPollInterval)
		if waitErr != nil {
			return waitErr
		}
		c = client.New(adopted.APIURL, adopted.Token)
		fmt.Println("Instance ready. Credentials saved locally — nothing to copy from the logs.")
	}

	// A credentials file that outlived its instance is READABLE, so newClient succeeded and every
	// request above would leave for a port nothing listens on. Probing here rather than reacting to
	// the first creation keeps the recovery in one place, and costs one GET on the one command that
	// is already the slowest — see reachable.go for why only this command may repoint.
	if dead := unreachableAPI(ctx, c); dead != nil {
		c, err = repointAtInstance(ctx, dead, execDocker, os.Stdin, os.Stdout, isInteractive(os.Stdin))
		if err != nil {
			return err
		}
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
	}, "project "+*project); err != nil {
		return err
	}

	var created service.CreatedToken
	in := service.CreateTokenInput{ProjectKey: *project, Name: *tokenName}
	if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/tokens"+teamQuery(*team), in, &created); err != nil {
		return err
	}

	fmt.Printf("team %s and project %s are ready.\n", *team, *project)

	// Placed HERE rather than after printToken: whatever follows the display of a secret is what
	// gets read the least. Same reason as announceMCPConfig.
	announceTrustIsClosed(ctx, c, *team, *project)

	// The .mcp.json is written BEFORE the token is printed: it is what makes the agent operational,
	// and the printed token only means something once you know where it will be read from.
	if err := announceMCPConfig(c.BaseURL()); err != nil {
		return err
	}

	printToken(created)
	return nil
}

// announceTrustIsClosed warns that no trust is declared, AT THE EXACT MOMENT it starts to matter:
// when the team holds a second project.
//
// The graph is born empty, so two sibling repos cannot raise issues to each other until a human
// says so. Without this block, the human learns it from a `not found` their agent reports hours
// later, with no way to tell a policy apart from a bug.
//
// Nothing is printed on a team's FIRST project: a one-project team has no possible pair, and the
// graph is structurally invisible there. A warning that always shows is a warning people stop
// reading.
//
// A failure never interrupts init: the team, the project and the token already exist server-side,
// and the token is about to be shown for its one and only time. Aborting here would lose it — same
// reason as announceMCPConfig, and that is also why this function returns no error.
func announceTrustIsClosed(ctx context.Context, c *client.Client, team, project string) {
	// The key is normalised BEFORE any comparison. `flowlio init --project frnt` is legal — the
	// server upper-cases the key — so comparing the raw flag against what the API returns would
	// make the project we just created look like a sibling, and the suggested command would be a
	// SELF-PAIR that `trust allow` refuses. Help that does not work is worse than no help.
	project = strings.ToUpper(strings.TrimSpace(project))

	var projects []service.Project
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(team), nil, &projects); err != nil {
		return
	}
	if len(projects) < 2 {
		return
	}

	// The graph is READ before claiming that it is empty. Without that read, a second `init` on an
	// already-wired team announced "no trust is declared" when it was, and sent the human off to
	// retype a command they had already run.
	var edges []service.TrustEdge
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/trust"+teamQuery(team), nil, &edges); err != nil {
		// A project token is not allowed to read the graph: in that case we stay quiet rather than
		// assert anything. Silence is the only honest way out.
		return
	}
	if len(edges) > 0 {
		return
	}

	// Naming a concrete sibling rather than "your other projects": the command we give is then
	// copyable as is, which is the only kind of help that survives being skim-read.
	sibling := ""
	for _, p := range projects {
		if p.Key != project {
			sibling = p.Key
			break
		}
	}
	if sibling == "" {
		return
	}

	fmt.Printf("\n  Team %s now holds %d projects, and no trust is declared:\n",
		team, len(projects))
	fmt.Printf("  %s and %s cannot raise issues to each other. With the admin token:\n\n",
		project, sibling)
	fmt.Printf("      flowlio trust allow %s %s --team %s\n\n", sibling, project, team)
}

// announceMCPConfig writes the current repo's MCP configuration and says what happened.
//
// A write failure DOES NOT CANCEL the init: the team, the project and the token already exist
// server-side, and the token is about to be shown for its one and only time. Aborting here would
// lose it. The fault is therefore reported, and the command carries on.
func announceMCPConfig(apiURL string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory not found: %w", err)
	}

	path, written, err := writeMCPConfig(dir, apiURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowlio: MCP configuration not written: %v\n", err)
		return nil
	}
	if written {
		fmt.Printf("%s written — committable as is, it holds no secret.\n", path)
	} else {
		fmt.Printf("%s already carries a %q entry, left alone.\n", path, mcpServerKey)
	}
	return nil
}

// ensure runs a creation and tolerates a conflict: the resource already existed, which is the
// intended outcome. Any other error propagates.
func ensure(create func() error, label string) error {
	err := create()
	if err == nil {
		fmt.Printf("%s created.\n", label)
		return nil
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		fmt.Printf("%s already exists, left alone.\n", label)
		return nil
	}
	return err
}

// splitFlags separates flags from positional arguments, whatever their order: neither an agent nor
// a human in a hurry should have to guess that --team goes before the key.
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
func printToken(created service.CreatedToken) {
	// The line is given ready to paste, because that is exactly what the user is about to do with
	// it: the .mcp.json references ${FLOWLIO_TOKEN}, so the variable has to exist.
	fmt.Printf("\ntoken %q for project %s — shown once, paste it as is:\n\n    export FLOWLIO_TOKEN=%s\n\n",
		created.Name, created.ProjectKey, created.Secret)
	fmt.Println("Never in the repository: the .mcp.json carries nothing but a reference to that variable.")
}
