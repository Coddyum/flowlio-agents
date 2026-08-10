package main

// WHERE AN MCP SESSION GETS ITS CREDENTIALS.
//
// Split out of mcp.go, which is at the size limit, and it is the right seam anyway: this file is
// the only thing that knows how a repository is identified, and the day the engine serves MCP over
// HTTP it is the only one that changes.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// mcpClient resolves the API client of an MCP session. FOUR SOURCES, AND THE FIRST THAT ANSWERS
// WINS — the order is the compatibility contract, not a preference:
//
//  1. FLOWLIO_API_URL + FLOWLIO_TOKEN. Every `.mcp.json` written before the rename carries them, and
//     an explicit export still has to beat everything else.
//  2. FLOWLIO_PROJECT + FLOWLIO_REPO. What `flowlio connect` writes: two names, resolved here into
//     an address and a secret that never left this host.
//  3. The admin credentials. An operator running `flowlio mcp` by hand from the machine that owns
//     the instance gets a session rather than a lecture.
//  4. Nothing — and then the error names the one command that fixes it.
//
// The ADDRESS now travels with the token (2), where it used to be frozen in the committed
// `.mcp.json`. A repository initialised against the Docker stack kept calling :42058 forever, even
// once the API had moved; the file that carries the token is rewritten by `connect`, so it cannot
// drift the same way.
func mcpClient() (*client.Client, error) {
	if envURL, envToken := os.Getenv("FLOWLIO_API_URL"), os.Getenv("FLOWLIO_TOKEN"); envURL != "" && envToken != "" {
		return client.New(envURL, envToken), nil
	}

	project, repo := os.Getenv(envProjectVar), os.Getenv(envRepoVar)
	if project != "" && repo != "" {
		f, err := credentials.LoadRepo(project, repo)
		if err == nil {
			return client.New(f.APIURL, f.Token), nil
		}
		if !errors.Is(err, credentials.ErrNotFound) {
			return nil, err
		}
		// The names are there and the credentials are not: this repository was set up on ANOTHER
		// machine and its `.mcp.json` was cloned. Naming the command is the whole answer.
		return nil, fmt.Errorf("repo %s of project %s has no credentials on this host — "+
			"run `flowlio connect %s` from the root of this repository",
			strings.ToUpper(repo), strings.ToLower(project), strings.ToUpper(repo))
	}

	api, err := newClient()
	if err != nil {
		return nil, fmt.Errorf("%w — an agent's session reads %s and %s, which `flowlio connect <REPO>` "+
			"writes into this repository's %s", err, envProjectVar, envRepoVar, mcpConfigName)
	}
	return api, nil
}
