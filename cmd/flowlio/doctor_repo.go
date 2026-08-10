package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                          | Ligne |
// |-----------------|-----------------------------------------------------------------|-------|
// | repoChecks      | Everything that only means something inside a connected repo      | 37    |
// | connectedNames  | The project and repo the .mcp.json entry names                    | 79    |
// | workflowCheck   | Whether the workflow prompt is there and current                  | 99    |
// | pointerCheck    | Whether at least one entry file sends a session to it             | 117   |
// | trustCheck      | Which repositories this one may raise an issue at                 | 146   |
//
// Fin du sommaire.
// =====================================================================
//
// The repository half of `flowlio doctor`. Split from doctor.go because the two halves answer
// different questions — "is the instance up" and "is THIS repository going to work" — and because
// the 300-line guard says so.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	"github.com/Coddyum/flowlio-agents/internal/pkg/prompt"
)

// repoChecks are everything that only means something inside a connected repository.
func repoChecks(ctx context.Context, dir string) []checkOutcome {
	project, repo, err := connectedNames(dir)
	if err != nil {
		return []checkOutcome{{label: "the " + mcpConfigName + " entry names a repository", cause: err.Error()}}
	}
	out := []checkOutcome{{
		label: fmt.Sprintf("%s declares %s in project %s", mcpConfigName, repo, project),
		ok:    true,
	}}

	// The workflow file and the pointer are local, so they are checked whether or not the instance
	// answers: a repository can be wrong about them while the API is perfectly fine.
	out = append(out, workflowCheck(dir), pointerCheck(dir), trustCheck(ctx, project, repo))

	f, err := credentials.LoadRepo(project, repo)
	if err != nil {
		cause := err.Error()
		if errors.Is(err, credentials.ErrNotFound) {
			cause = fmt.Sprintf("there is none on this host: run `flowlio connect %s`", repo)
		}
		return append(out,
			checkOutcome{label: "this repository's token is filed on this host", cause: cause},
			checkOutcome{label: "the token is accepted and belongs to " + repo, skipped: true,
				cause: "there is no token to try"})
	}
	out = append(out, checkOutcome{label: "this repository's token is filed on this host", ok: true})

	identity, err := checkIdentity(ctx, client.New(f.APIURL, f.Token))
	if err != nil {
		return append(out, checkOutcome{label: "the token is accepted and belongs to " + repo, cause: err.Error()})
	}
	if !strings.EqualFold(identity.ProjectKey, repo) || !strings.EqualFold(identity.TeamSlug, project) {
		return append(out, checkOutcome{
			label: "the token is accepted and belongs to " + repo,
			cause: fmt.Sprintf("it belongs to %s/%s instead", identity.TeamSlug, identity.ProjectKey),
		})
	}
	return append(out, checkOutcome{label: "the token is accepted and belongs to " + repo, ok: true})
}

// connectedNames reads the pair out of the .mcp.json entry — the same two names the MCP server will
// resolve its credentials from, so a mismatch here is exactly what an agent would hit.
func connectedNames(dir string) (project, repo string, err error) {
	_, servers, err := readMCPFile(filepath.Join(dir, mcpConfigName))
	if err != nil {
		return "", "", err
	}

	var entry serverEntry
	if err := json.Unmarshal(servers[mcpServerKey], &entry); err != nil {
		return "", "", fmt.Errorf("the %q entry is not readable: %w", mcpServerKey, err)
	}
	project, repo = entry.Env[envProjectVar], entry.Env[envRepoVar]
	if project == "" || repo == "" {
		return "", "", fmt.Errorf("the %q entry carries no %s/%s — it was written by an older "+
			"version: run `flowlio connect <REPO>` again", mcpServerKey, envProjectVar, envRepoVar)
	}
	return project, repo, nil
}

// workflowCheck says whether the workflow prompt is there and current. An older version is a
// FAILURE and not a remark: it is what an agent reads every session, and `connect` rewrites it.
func workflowCheck(dir string) checkOutcome {
	label := prompt.WorkflowPath + " is at version " + prompt.Version

	raw, err := os.ReadFile(filepath.Join(dir, prompt.WorkflowPath))
	if err != nil {
		return checkOutcome{label: label, cause: "it is not there: run `flowlio connect <REPO>`"}
	}
	if !strings.Contains(string(raw), "# Working with Flowlio — version "+prompt.Version) {
		return checkOutcome{label: label,
			cause: "it carries another version: run `flowlio connect <REPO>` to refresh it"}
	}
	return checkOutcome{label: label, ok: true}
}

// pointerCheck says whether at least one entry file sends a session to the workflow.
//
// ONE IS ENOUGH. A repository worked in with a single client has a single pointer, and demanding
// one per detected client would fail a repository that is perfectly fine.
func pointerCheck(dir string) checkOutcome {
	const label = "an agent is pointed at the workflow"

	entries := detectEntryFiles(dir)
	if len(entries) == 0 {
		return checkOutcome{label: label, skipped: true,
			cause: "no agent client is detected in this repository"}
	}

	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Path))
		if err == nil && strings.Contains(string(raw), prompt.MarkerStart) {
			return checkOutcome{label: label + " (" + entry.Path + ")", ok: true}
		}
	}
	return checkOutcome{label: label,
		cause: "no detected entry file carries the pointer: run `flowlio connect <REPO>`"}
}

// trustCheck says which repositories this one may raise an issue at.
//
// READ WITH THE ADMIN CREDENTIAL, AND SILENT WITHOUT IT. A project token is not allowed to read the
// graph, so on a host that holds no admin credential this check is SKIPPED rather than failed:
// asserting anything about a graph nobody could look at would be inventing a verdict.
//
// An empty graph is worth a red line even so. Since the edges are written by the creation of each
// repository, an empty one means either a creation that raced with another — the known,
// uncorrected hole recorded in sql/queries/projects.sql — or a `trust deny` somebody meant. Both
// are things a human has to look at, and both are invisible until an agent's create_issue fails.
func trustCheck(ctx context.Context, project, repo string) checkOutcome {
	const label = "this repository may raise issues at its siblings"

	admin, err := credentials.Load()
	if err != nil {
		return checkOutcome{label: label, skipped: true,
			cause: "reading the trust graph needs the admin credential, which is not on this host"}
	}

	var edges []service.TrustEdge
	c := client.New(admin.APIURL, admin.Token)
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/trust"+teamQuery(project), nil, &edges); err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusForbidden || apiErr.Status == http.StatusUnauthorized) {
			return checkOutcome{label: label, skipped: true,
				cause: "this credential is not allowed to read the trust graph"}
		}
		return checkOutcome{label: label, cause: err.Error()}
	}

	var outgoing []string
	for _, edge := range edges {
		if strings.EqualFold(edge.From, repo) {
			outgoing = append(outgoing, edge.To)
		}
	}
	if len(outgoing) == 0 {
		return checkOutcome{label: label,
			cause: "no outgoing trust edge — create_issue has nowhere to write. If this project " +
				"holds other repositories, `flowlio trust allow " + repo + " <OTHER>` opens one direction"}
	}
	return checkOutcome{label: label + " (" + strings.Join(outgoing, ", ") + ")", ok: true}
}
