package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | verifyConnection | Plays the four checks and fails on the first that does not pass  | 46    |
// | checkIdentity    | Asks the API who the freshly filed token is                      | 85    |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THE SELF-TEST IS THE TAIL OF `connect` AND NOT A COMMAND OF ITS OWN.
//
// A separate `flowlio verify` is a command nobody runs, and the moment verification is worth
// anything is the moment the files have just been written — before the user closes the terminal and
// opens an agent that then fails for a reason they will attribute to the agent.
//
// NOTHING GREEN IS ANNOUNCED WITHOUT HAVING BEEN OBSERVED. Each line below either performed its
// check or says which step failed, and the command exits non-zero. `flowlio doctor` replays the
// same ground later, from a repository that is already connected.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// expectedToolCount is the size of the surface an agent is offered. Written down so the check is a
// check: comparing tools() to itself would pass on an empty list, which is exactly the failure that
// matters — an agent that connects and is offered nothing.
const expectedToolCount = 12

// verifyConnection plays the four checks in the order a session would hit them, and stops at the
// first that fails.
//
// The order is deliberate: an unreachable instance makes every later failure meaningless, and a
// token that does not resolve makes the identity comparison meaningless in turn. Reporting the
// first real cause is worth more than four lines of red.
func verifyConnection(ctx context.Context, out io.Writer, f credentials.RepoFile) error {
	api := client.New(f.APIURL, f.Token)

	_, _ = fmt.Fprintln(out, "\nChecking the connection:")

	if dead := unreachableAPI(ctx, api); dead != nil {
		_, _ = fmt.Fprintf(out, "  fail  the instance answers at %s\n", f.APIURL)
		return fmt.Errorf("the instance does not answer: %w", dead)
	}
	_, _ = fmt.Fprintf(out, "  ok    the instance answers at %s\n", f.APIURL)

	identity, err := checkIdentity(ctx, api)
	if err != nil {
		_, _ = fmt.Fprintln(out, "  fail  this repository's token is accepted")
		return fmt.Errorf("the token was refused: %w", err)
	}
	_, _ = fmt.Fprintln(out, "  ok    this repository's token is accepted")

	// The comparison is the only thing that catches a credential filed under the wrong pair — the
	// two writes above would both succeed and the agent would work on somebody else's board.
	if !strings.EqualFold(identity.ProjectKey, f.Repo) || !strings.EqualFold(identity.TeamSlug, f.Project) {
		_, _ = fmt.Fprintf(out, "  fail  the token belongs to %s/%s\n", f.Project, f.Repo)
		return fmt.Errorf("the token belongs to %s/%s, but %s/%s was written into %s",
			identity.TeamSlug, identity.ProjectKey, f.Project, f.Repo, mcpConfigName)
	}
	_, _ = fmt.Fprintf(out, "  ok    the token belongs to %s/%s\n", f.Project, f.Repo)

	if got := len(tools()); got != expectedToolCount {
		_, _ = fmt.Fprintf(out, "  fail  %d tools offered to an agent\n", got)
		return fmt.Errorf("this binary offers %d tools, expected %d", got, expectedToolCount)
	}
	_, _ = fmt.Fprintf(out, "  ok    %d tools offered to an agent\n", expectedToolCount)

	return nil
}

// checkIdentity asks the API who the token is. A project token that resolves to no project is not a
// working connection: it is an admin token filed as a repository's, and every tool call would fail
// on the scope rather than on the credential.
func checkIdentity(ctx context.Context, api *client.Client) (service.Identity, error) {
	var identity struct {
		Scope string `json:"scope"`
		service.Identity
	}
	if err := api.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &identity); err != nil {
		return service.Identity{}, err
	}
	if identity.ProjectKey == "" {
		return service.Identity{}, fmt.Errorf("this token is scoped to no project")
	}
	return identity.Identity, nil
}
