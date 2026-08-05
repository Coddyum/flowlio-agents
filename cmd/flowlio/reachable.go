package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                          | Ligne |
// |-------------------|-----------------------------------------------------------------|-------|
// | unreachableAPI    | Says whether anything answered, and at which address it did not | 45    |
// | repointAtInstance | Follows the running instance when the local address is dead     | 64    |
//
// Fin du sommaire.
// =====================================================================
//
// WHY A DEAD ADDRESS NEEDS ITS OWN RECOVERY.
//
// `~/.config/flowlio/credentials.json` outlives the instance that wrote it. When the compose file
// moved the API off its old port, every machine that had run `flowlio init` before kept pointing at
// the port that no longer listens — and the file was still perfectly readable, so newClient
// succeeded and the first request left for nowhere. Everything written to rescue a MISSING file
// (adoption from the container, offering to start the stack) lives behind `err != nil` and was
// therefore never reached.
//
// Two things are needed, and they are not the same thing. Every command must SAY where the dead
// address came from — that is client.TransportError, and it costs nothing. Only `flowlio init` may
// then look for a live instance and repoint: reading docker is a side effect on a failure path, and
// overwriting the file is a decision that belongs to whoever pointed it there.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// unreachableAPI reports the transport failure when nothing answered at the client's address, and
// nil when something did.
//
// ANY answer counts, including a refusal: a 401 or a 403 proves an API is listening, and this probe
// is about the address, not about what the token may do. Reading the teams is the cheapest request
// that exists on every deployment.
func unreachableAPI(ctx context.Context, c *client.Client) *client.TransportError {
	err := c.Do(ctx, http.MethodGet, workspaceAPI+"/teams", nil, nil)

	var transport *client.TransportError
	if errors.As(err, &transport) {
		return transport
	}
	return nil
}

// repointAtInstance offers to follow the running instance after dead showed the configured address
// answers nothing, and returns a client pointed at it on a yes.
//
// mayAsk is decided by the CALLER, because os.Stdin is the only thing that can tell whether a human
// is there — and because the branch that must not prompt is the one an agent runs, so it has to be
// drivable from a test with no terminal.
//
// Returns dead unchanged whenever nothing better is known: that error already names the address and
// the file it came from, which is the whole point of this path.
func repointAtInstance(ctx context.Context, dead *client.TransportError, run dockerRunner, in io.Reader, out io.Writer, mayAsk bool) (*client.Client, error) {
	live, err := instanceCredentials(ctx, run)
	if err != nil {
		return nil, dead
	}
	if strings.TrimRight(live.APIURL, "/") == dead.URL {
		// The instance believes in the very address that just went unanswered. Repointing would
		// change nothing, and claiming a fix is available would be worse than the plain error.
		return nil, dead
	}

	if !mayAsk {
		// The command names the way out instead of taking it. An agent's session has nobody to
		// consent, and a file pointing elsewhere may be pointing there on purpose.
		return nil, fmt.Errorf("%w — the running instance answers at %s instead: export FLOWLIO_API_URL=%s, "+
			"or delete the file above so the instance's credentials get adopted", dead, live.APIURL, live.APIURL)
	}

	_, _ = fmt.Fprintf(out, "%v\n", dead)
	// Default NO: this question overwrites a file that may be pointing where someone put it, and a
	// stdin that ends without a word must not count as consent.
	ok, err := askYesNo(in, out, fmt.Sprintf(
		"The running instance answers at %s — follow it and overwrite the local credentials?", live.APIURL), false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, dead
	}

	path, err := credentials.Save(live)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(out, "%s now points at %s.\n", path, live.APIURL)
	return client.New(live.APIURL, live.Token), nil
}
