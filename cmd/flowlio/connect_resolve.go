package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                       | Ligne |
// |--------------------|--------------------------------------------------------------|-------|
// | repoCredentials    | The token this repository works under, minting one if needed  | 47    |
// | storedRepo         | Finds an already-issued credential from the repo key alone    | 86    |
// | mintRepoToken      | Issues a project token and files it under the project's slug  | 120   |
// | resolveProjectSlug | Which project holds this repo key, asked of the instance      | 148   |
//
// Fin du sommaire.
// =====================================================================
//
// WHAT `connect` NEEDS, AND ITS LIMIT.
//
// The normal path reads a credential `flowlio setup` already filed. When there is none — a
// repository set up on its own, or one whose credential was deleted — this file mints one, and
// minting a project token requires the ADMIN credential, which only exists on the machine that owns
// the instance.
//
// So `flowlio connect` works for the operator and not for a teammate who cloned the repository
// somewhere else. That is acceptable for a single-operator self-hosted deployment, and it is why
// `setup` says it in its output rather than leaving it to be discovered.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// connectTokenName is the name the token issued by `connect` carries. It shows in `flowlio token
// list`, and it says which command created it — which is the only question anybody asks of that
// list.
const connectTokenName = "agent"

// repoCredentials yields the credential this repository works under.
//
// Three ways in, in this order: the slug was given, the slug can be deduced from what is already
// filed on this host, or nothing is filed and a token has to be issued.
func repoCredentials(ctx context.Context, projectFlag, repo string) (credentials.RepoFile, error) {
	if projectFlag != "" {
		f, err := credentials.LoadRepo(projectFlag, repo)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, credentials.ErrNotFound) {
			return credentials.RepoFile{}, err
		}
		return mintRepoToken(ctx, projectFlag, repo)
	}

	f, found, err := storedRepo(repo)
	if err != nil {
		return credentials.RepoFile{}, err
	}
	if found {
		return f, nil
	}

	// Nothing filed under that key: ask the instance which project holds it. This is what makes
	// `flowlio connect API` enough on a host with one project — the slug is derivable, so asking the
	// user for it would be asking them to repeat what we can read.
	admin, err := newClient()
	if err != nil {
		return credentials.RepoFile{}, fmt.Errorf("%w — `flowlio connect` issues this repository's "+
			"token, which needs the admin credential of the instance", err)
	}
	slug, err := resolveProjectSlug(ctx, admin, repo)
	if err != nil {
		return credentials.RepoFile{}, err
	}
	return mintRepoToken(ctx, slug, repo)
}

// storedRepo finds an already-issued credential from the repo key alone.
//
// A key present in two projects is REFUSED rather than guessed. Picking one would connect the
// repository to somebody else's board and every symptom of that lands later, somewhere else.
func storedRepo(repo string) (f credentials.RepoFile, found bool, err error) {
	stored, err := credentials.ListRepos()
	if err != nil {
		return credentials.RepoFile{}, false, err
	}

	var matches []credentials.RepoFile
	for _, candidate := range stored {
		if strings.EqualFold(candidate.Repo, repo) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return credentials.RepoFile{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		slugs := make([]string, 0, len(matches))
		for _, m := range matches {
			slugs = append(slugs, m.Project)
		}
		return credentials.RepoFile{}, false, fmt.Errorf(
			"repo %s exists in %d projects (%s) — say which one with --project <slug>",
			repo, len(matches), strings.Join(slugs, ", "))
	}
}

// mintRepoToken issues a project token and files it under the project's slug.
//
// THE SECRET IS NEVER PRINTED. It goes straight from the response into a 0600 file, which is the
// whole point of moving it out of the environment: a token nobody ever sees is a token nobody
// pastes into a repository.
func mintRepoToken(ctx context.Context, project, repo string) (credentials.RepoFile, error) {
	admin, err := newClient()
	if err != nil {
		return credentials.RepoFile{}, fmt.Errorf("%w — `flowlio connect` issues this repository's "+
			"token, which needs the admin credential of the instance", err)
	}

	var created service.CreatedToken
	in := service.CreateTokenInput{ProjectKey: repo, Name: connectTokenName}
	if err := admin.Do(ctx, http.MethodPost, workspaceAPI+"/tokens"+teamQuery(project), in, &created); err != nil {
		return credentials.RepoFile{}, fmt.Errorf("issuing a token for %s: %w", repo, err)
	}

	f := credentials.RepoFile{
		APIURL:  admin.BaseURL(),
		Project: project,
		Repo:    repo,
		Token:   created.Secret,
	}
	if _, err := credentials.SaveRepo(f); err != nil {
		return credentials.RepoFile{}, err
	}
	// Read back rather than return what was sent: SaveRepo normalises the names, and everything
	// downstream — the .mcp.json, the hook, the self-test — has to use the spelling that was filed.
	return credentials.LoadRepo(project, repo)
}

// resolveProjectSlug asks the instance which project holds this repo key.
func resolveProjectSlug(ctx context.Context, c *client.Client, repo string) (string, error) {
	var teams []service.Team
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/teams", nil, &teams); err != nil {
		return "", err
	}

	var matches []string
	for _, team := range teams {
		var projects []service.Project
		if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(team.Slug), nil, &projects); err != nil {
			continue
		}
		for _, p := range projects {
			if strings.EqualFold(p.Key, repo) {
				matches = append(matches, team.Slug)
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no project holds a repo called %s — create it with `flowlio setup`, "+
			"or name the project with --project <slug>", repo)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("repo %s exists in %d projects (%s) — say which one with --project <slug>",
			repo, len(matches), strings.Join(matches, ", "))
	}
}
