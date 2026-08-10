package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | setupPlan        | The project and the repositories a run is going to create        | 52    |
// | runSetup         | Creates a project, its repositories and one token each           | 63    |
// | askSetupPlan     | Holds the conversation that composes that plan                   | 109   |
// | setupClient      | The admin client, starting the stack when there is none          | 173   |
// | createWorkspace  | Creates the project then its repositories, one after the other   | 212   |
// | fileRepoTokens   | Issues one token per repository and files it, printing none      | 232   |
// | ensure           | Runs a creation, tolerating that it already exists               | 256   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio setup` is the first command of the product and the only one that asks questions. It ends
// on a list of `flowlio connect <REPO>` lines, one per repository, and nothing else is left to do.
//
// NO SECRET IS EVER PRINTED. The tokens go straight into `~/.config/flowlio/repos/`, in 0600. That
// is the whole difference with `flowlio init`, which ended on an `export FLOWLIO_TOKEN=` line the
// user then had to place somewhere — and which two repositories on one machine could not share.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// setupTokenName is the name every token issued here carries. Same as `connect`'s: which command
// created a token is the only thing anybody asks of `flowlio token list`.
const setupTokenName = connectTokenName

// setupUsage is printed whenever the flags do not add up, and it is the WHOLE non-interactive form
// — a usage line that omits an option is how a user learns the option does not exist.
const setupUsage = "usage: flowlio setup   (interactive)\n" +
	"       flowlio setup --project <slug> --repo <KEY>[:<name>] [--repo …] " +
	"[--project-name <name>] [--yes]\n" +
	"       flowlio setup --list"

// setupPlan is the project and the repositories a run is going to create.
type setupPlan struct {
	// slug is the project as everything else names it: the configuration directory, `--project`, the
	// `.mcp.json` of every repository.
	slug string
	// name is what a human calls the project.
	name string
	// repos are its repositories, in the order they were given.
	repos []repoSpec
}

// runSetup creates a project, its repositories and one token each, then says what to run where.
func runSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	slug := fs.String("project", "", "project slug (e.g. acme)")
	name := fs.String("project-name", "", "human-readable project name (default: the slug)")
	list := fs.Bool("list", false, "reprint the connect lines of what is already set up")
	var repos repoFlags
	fs.Var(&repos, "repo", "repository as KEY[:name], repeatable (e.g. API:acme-api)")
	// Accepted and unused, on purpose. `--yes` is what `connect` takes, the documented
	// non-interactive form of this command carries it, and a flag that is documented but rejected
	// fails a copied command line with `flag provided but not defined` — which reads as a bug.
	// Giving the flags means asking nothing, so there is nothing left for it to skip.
	_ = fs.Bool("yes", false, "implied: giving --project and --repo already asks no questions")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *list {
		return listSetupRepos(os.Stdout)
	}

	plan, err := askSetupPlan(os.Stdin, os.Stdout, *slug, *name, repos)
	if err != nil {
		return err
	}

	c, err := setupClient(ctx)
	if err != nil {
		return err
	}
	if err := createWorkspace(ctx, os.Stdout, c, plan); err != nil {
		return err
	}
	if err := fileRepoTokens(ctx, os.Stdout, c, plan); err != nil {
		return err
	}

	announceConnect(os.Stdout, plan)
	return nil
}

// askSetupPlan composes the plan, from the flags when they are there and from a conversation when
// they are not.
//
// NON-INTERACTIVE WITHOUT FLAGS IS REFUSED RATHER THAN WAITED ON. An agent runs this with no
// terminal; a question asked there is a session that hangs until somebody kills it. The refusal
// names the flag form, so the way out is in the error itself.
func askSetupPlan(in io.Reader, out io.Writer, slug, name string, repos repoFlags) (setupPlan, error) {
	if slug != "" || len(repos) > 0 {
		if slug == "" || len(repos) == 0 {
			return setupPlan{}, errors.New(setupUsage)
		}
		if err := validateProjectSlug(slug); err != nil {
			return setupPlan{}, err
		}
		if name == "" {
			name = slug
		}
		return setupPlan{slug: slug, name: name, repos: repos}, nil
	}

	if !isInteractive(in) {
		return setupPlan{}, errors.New("nothing to read from and no flags given.\n" + setupUsage)
	}

	p := newPrompter(in, out)
	_, _ = fmt.Fprint(out, "Setting up a Flowlio project and its repositories.\n\n")

	projectName, err := p.askUntil("Project name?", "", func(string) error { return nil })
	if err != nil {
		return setupPlan{}, err
	}
	projectSlug, err := p.askUntil(
		fmt.Sprintf("Project slug? [%s]", slugify(projectName)), slugify(projectName), validateProjectSlug)
	if err != nil {
		return setupPlan{}, err
	}

	var specs repoFlags
	for {
		key, err := p.askUntil("Repo key?", "", validateRepoKey)
		if err != nil {
			return setupPlan{}, err
		}
		repoName, err := p.askUntil("Repo name? ["+key+"]", key, func(string) error { return nil })
		if err != nil {
			return setupPlan{}, err
		}
		if err := specs.Set(strings.ToUpper(key) + ":" + repoName); err != nil {
			_, _ = fmt.Fprintf(out, "  %v\n", err)
			continue
		}

		more, err := p.confirm("Add another repo?", false)
		if err != nil {
			return setupPlan{}, err
		}
		if !more {
			break
		}
	}

	return setupPlan{slug: projectSlug, name: projectName, repos: specs}, nil
}

// setupClient yields the admin client, offering to start the stack when there is none.
//
// THE PRIVILEGE OF OFFERING TO START THE STACK LIVES HERE NOW. It used to belong to `flowlio init`,
// which was the first command a user reached for; `setup` is that command today. Every other
// command still fails with an explanation instead, because an agent runs them with no terminal to
// answer from.
func setupClient(ctx context.Context) (*client.Client, error) {
	c, err := newClient()
	if err != nil {
		// An instance that IS running and still left newClient failing is a different problem —
		// unreadable credentials, a broken daemon — and starting a second stack would not fix it.
		if !isInteractive(os.Stdin) || instanceIsRunning(ctx, execDocker) {
			return nil, err
		}
		if err := offerToStartStack(ctx, execDocker, os.Stdin, os.Stdout); err != nil {
			return nil, err
		}

		waitCtx, cancel := context.WithTimeout(ctx, instanceReadyTimeout)
		defer cancel()
		adopted, waitErr := waitForCredentials(waitCtx, execDocker, credentialsPollInterval)
		if waitErr != nil {
			return nil, waitErr
		}
		fmt.Println("Instance ready. Credentials saved locally — nothing to copy from the logs.")
		return client.New(adopted.APIURL, adopted.Token), nil
	}

	// A credentials file that outlived its instance is READABLE, so newClient succeeded and every
	// request below would leave for a port nothing listens on. Probing here rather than reacting to
	// the first creation keeps the recovery in one place — see reachable.go for why only this
	// command may repoint.
	if dead := unreachableAPI(ctx, c); dead != nil {
		return repointAtInstance(ctx, dead, execDocker, os.Stdin, os.Stdout, isInteractive(os.Stdin))
	}
	return c, nil
}

// createWorkspace creates the project, then its repositories, ONE AFTER THE OTHER.
//
// The sequence is not a matter of style. Two repositories created CONCURRENTLY do not get linked to
// each other: the trust edges are written by the creation of each repository against the ones that
// already exist, and two creations in flight cannot see one another. The hole is known and
// uncorrected, written down in `sql/queries/projects.sql`. A loop avoids it by construction, and
// that is one of the reasons this command exists at all.
func createWorkspace(ctx context.Context, out io.Writer, c *client.Client, plan setupPlan) error {
	if err := ensure(out, func() error {
		in := service.CreateTeamInput{Slug: plan.slug, Name: plan.name}
		return c.Do(ctx, http.MethodPost, workspaceAPI+"/teams", in, nil)
	}, "project "+plan.slug); err != nil {
		return err
	}

	for _, repo := range plan.repos {
		if err := ensure(out, func() error {
			in := service.CreateProjectInput{Key: repo.Key, Name: repo.Name}
			return c.Do(ctx, http.MethodPost, workspaceAPI+"/projects"+teamQuery(plan.slug), in, nil)
		}, "repo "+repo.Key); err != nil {
			return err
		}
	}
	return nil
}

// fileRepoTokens issues one token per repository and files it. NOTHING IS PRINTED but the path.
func fileRepoTokens(ctx context.Context, out io.Writer, c *client.Client, plan setupPlan) error {
	for _, repo := range plan.repos {
		var created service.CreatedToken
		in := service.CreateTokenInput{ProjectKey: repo.Key, Name: setupTokenName}
		if err := c.Do(ctx, http.MethodPost, workspaceAPI+"/tokens"+teamQuery(plan.slug), in, &created); err != nil {
			return fmt.Errorf("issuing a token for %s: %w", repo.Key, err)
		}

		path, err := credentials.SaveRepo(credentials.RepoFile{
			APIURL:  c.BaseURL(),
			Project: plan.slug,
			Repo:    repo.Key,
			Token:   created.Secret,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "token for %s filed in %s.\n", repo.Key, path)
	}
	return nil
}

// ensure runs a creation and tolerates a conflict: the resource already existed, which is the
// intended outcome. Any other error propagates.
func ensure(out io.Writer, create func() error, label string) error {
	err := create()
	if err == nil {
		_, _ = fmt.Fprintf(out, "%s created.\n", label)
		return nil
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		_, _ = fmt.Fprintf(out, "%s already exists, left alone.\n", label)
		return nil
	}
	return err
}
