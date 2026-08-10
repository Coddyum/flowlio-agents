package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                           | Ligne |
// |----------------|------------------------------------------------------------------|-------|
// | runRemove      | Deletes a repo, or a whole project, on the instance                | 45    |
// | removeRepo     | Resolves the key to an id and deletes that repo                    | 76    |
// | removeProject  | Deletes a project and everything under it, after a retyped slug    | 124   |
// | resolveRepoID  | Turns a repo key into the identifier the route needs               | 170   |
//
// Fin du sommaire.
// =====================================================================
//
// THE TWO DELETIONS A HUMAN IS ALLOWED TO MAKE, and the reason this command exists at all.
//
// Both routes were already there, admin-only, and no CLI command served them: the repo route takes
// an IDENTIFIER while `flowlio project list` only ever prints keys, so using it meant reading a
// UUID out of a JSON response by hand. Resolving that key is most of this file's value.
//
// A REFUSAL IS RELAYED VERBATIM. The server's 409 names every sibling still holding a thread with
// this repo, how many each holds, and what to do instead. Rewording it here would lose the counts
// and the advice, and would drift from the sentence the hosted product shows for the same refusal.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

const removeUsage = "usage: flowlio remove <REPO> [--project <slug>]   removes one repository\n" +
	"       flowlio remove --project <slug>            removes the project and everything in it"

// runRemove deletes a repo, or a whole project, on the instance.
func runRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	project := fs.String("project", "", "project slug")

	positional, err := splitFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return errors.New(removeUsage)
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	if len(positional) == 0 {
		if *project == "" {
			return errors.New(removeUsage)
		}
		return removeProject(ctx, os.Stdin, os.Stdout, c, *project)
	}
	return removeRepo(ctx, os.Stdout, c, *project, strings.ToUpper(strings.TrimSpace(positional[0])))
}

// removeRepo resolves the key to an identifier and deletes that repository.
//
// The local credential goes with it. A token that authenticates nothing is not harmless: it is a
// secret on the disk that nobody can account for, and the next `flowlio connect` on that key would
// find it and believe the repository still exists.
func removeRepo(ctx context.Context, out io.Writer, c *client.Client, project, repo string) error {
	if project == "" {
		stored, found, err := storedRepo(repo)
		if err != nil {
			return err
		}
		if found {
			project = stored.Project
		} else if project, err = resolveProjectSlug(ctx, c, repo); err != nil {
			return err
		}
	}

	id, err := resolveRepoID(ctx, c, project, repo)
	if err != nil {
		return err
	}

	path := workspaceAPI + "/projects/" + url.PathEscape(id) + teamQuery(project)
	if err := c.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		// The server's own sentence, unedited. It names the siblings, their thread counts and the way
		// out; anything we wrote here would say less.
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			_, _ = fmt.Fprintf(out, "%s was not removed.\n\n%s\n", repo, apiErr.Message)
			return &exitError{code: 1, err: errors.New("the instance refused the deletion")}
		}
		return err
	}
	_, _ = fmt.Fprintf(out, "repo %s removed from project %s.\n", repo, project)

	if removed, err := credentials.DeleteRepo(project, repo); err != nil {
		return err
	} else if removed {
		_, _ = fmt.Fprintln(out, "its token was deleted from this host too.")
	}

	_, _ = fmt.Fprintf(out, "\nThe repository's own %s is untouched: this command does not reach it.\n"+
		"Run `flowlio disconnect` from its root to take the configuration back out.\n", mcpConfigName)
	return nil
}

// removeProject deletes a project and everything under it.
//
// TOTAL CASCADE, IRREVERSIBLE: every repository, every task, every issue thread and every memory
// entry of the project. A yes/no question is not enough consent for that — a bare Enter answers it,
// and this is precisely the case where an answer given without reading is the worst outcome. So the
// slug has to be RETYPED, which cannot be done by accident.
func removeProject(ctx context.Context, in io.Reader, out io.Writer, c *client.Client, project string) error {
	_, _ = fmt.Fprintf(out, "This removes project %s and EVERYTHING in it: every repository, every "+
		"task,\nevery issue thread, every memory entry. Nothing is archived and nothing comes back.\n",
		project)

	if !isInteractive(in) {
		return errors.New("removing a whole project needs a human to confirm it, and there is no " +
			"terminal here")
	}

	p := newPrompter(in, out)
	typed, err := p.ask(fmt.Sprintf("Type %s to confirm:", project))
	if err != nil {
		return err
	}
	if typed != project {
		return errors.New("the slug was not retyped: nothing was removed")
	}

	if err := c.Do(ctx, http.MethodDelete, workspaceAPI+"/teams/"+url.PathEscape(project), nil, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "project %s removed.\n", project)

	// Every credential of the project follows: they open nothing now.
	stored, err := credentials.ListRepos()
	if err != nil {
		return err
	}
	for _, f := range stored {
		if !strings.EqualFold(f.Project, project) {
			continue
		}
		if _, err := credentials.DeleteRepo(f.Project, f.Repo); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "the token of %s was deleted from this host.\n", f.Repo)
	}

	_, _ = fmt.Fprintf(out, "\nThe repositories' own %s files are untouched. Run `flowlio disconnect`\n"+
		"from each root to take the configuration back out.\n", mcpConfigName)
	return nil
}

// resolveRepoID turns a repo key into the identifier the deletion route needs. This is the whole
// reason a user does not have to read a UUID out of a JSON response by hand.
func resolveRepoID(ctx context.Context, c *client.Client, project, repo string) (string, error) {
	var projects []service.Project
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(project), nil, &projects); err != nil {
		return "", err
	}

	for _, p := range projects {
		if strings.EqualFold(p.Key, repo) {
			return p.ID.String(), nil
		}
	}
	return "", fmt.Errorf("project %s holds no repo called %s", project, repo)
}
