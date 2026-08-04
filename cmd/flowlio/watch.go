package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                         | Ligne |
// |-------------------|----------------------------------------------------------------|-------|
// | exitError         | Error carrying its own exit status up to main                    | 60    |
// | exitError.Error   | Renders the wrapped error's message                              | 67    |
// | exitError.Unwrap  | Exposes the wrapped error to errors.Is / errors.As               | 72    |
// | runWatch          | Screen 1: the debt queue, with an optional --follow               | 86    |
// | runShow           | Screen 2: the detail of one reference                             | 137   |
// | resolveTeam       | Resolves the target team: --team, then whoami, then the only one   | 186   |
// | refusalExit       | Turns an authorisation refusal into exit status 2                 | 221   |
// | parseRef          | Splits a KEY-number reference                                     | 236   |
//
// Fin du sommaire.
// =====================================================================
//
// THE TWO HUMAN SUPERVISION SCREENS.
//
// These are the only CLI commands that read a WHOLE team rather than the token's own project. They
// hit the `overview` module, mounted behind `AdminOnly`: a project token is refused there, and that
// refusal exits with STATUS 2 — distinct from the 1 of every other error, so a script can tell
// "wrong token" apart from "server unreachable".
//
// Phase 1: TEXT, NOT A TUI. No dependency added, no repaint, no control sequence emitted.
// `--follow` never clears the screen: the output stays greppable, redirectable, and readable in a
// `tmux` pane with no interactive terminal.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	overviewservice "github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	workspaceservice "github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// overviewAPI is the route prefix of the overview module, mounted by the engine under /api/<key>/.
const overviewAPI = "/api/overview"

// followInterval is the polling period of --follow.
//
// Ten seconds: a debt is measured in hours, responsiveness has no value here, and a twenty-repo
// team must not trigger a database sweep every second.
const followInterval = 10 * time.Second

// forbiddenExitCode is the exit status of an authorisation refusal.
const forbiddenExitCode = 2

// exitError carries its own exit status. main is the ONLY place that reads it, and the only one
// calling os.Exit: a command states the intent, it does not carry it out.
type exitError struct {
	code int
	err  error
}

// Error renders the wrapped error's message, without mentioning the status: a human reads a
// message, a script reads a status.
func (e *exitError) Error() string {
	return e.err.Error()
}

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *exitError) Unwrap() error {
	return e.err
}

// runWatch prints the team's debt queue.
//
// WITHOUT --follow, A HEALTHY TEAM PRODUCES NO OUTPUT AND EXITS 0. That is the criterion of the
// task, and it is also what makes the command usable from `cron`: it only speaks when there is
// something to say.
//
// WITH --follow, it only writes on CHANGE, measured on the state's signature rather than on its
// rendering — ages move every turn without anything having moved. The first turn always writes,
// including on a healthy team: a `--follow` that starts mute is indistinguishable from a dead
// client.
func runWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	team := teamFlag(fs)
	follow := fs.Bool("follow", false, "poll every 10s and only write when something changes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	slug, err := resolveTeam(ctx, c, *team)
	if err != nil {
		return err
	}

	previous := ""
	for {
		var state overviewservice.TeamState
		if err := c.Do(ctx, http.MethodGet, overviewAPI+"/"+teamQuery(slug), nil, &state); err != nil {
			return refusalExit(err)
		}

		if !*follow {
			fmt.Print(renderWatch(state))
			return nil
		}

		if sig := watchSignature(state); sig != previous {
			previous = sig
			if out := renderWatch(state); out != "" {
				fmt.Print(out)
			} else {
				fmt.Printf("%s — no debt left.\n", state.GeneratedAt.Format(time.RFC3339))
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(followInterval):
		}
	}
}

// runShow prints the detail of one reference of the queue.
//
// Its argument is the string read in the REF column of `watch`, retyped as is. The CLI handles no
// opaque identifier: that is the same constraint the service imposes on itself.
func runShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	team := teamFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: flowlio show [--team <slug>] <REF>   (e.g. flowlio show CORE-41)")
	}

	projectKey, number, err := parseRef(fs.Arg(0))
	if err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	slug, err := resolveTeam(ctx, c, *team)
	if err != nil {
		return err
	}

	path := overviewAPI + "/refs/" + url.PathEscape(projectKey) + "/" +
		strconv.FormatInt(number, 10) + teamQuery(slug)

	var detail overviewservice.RefDetail
	if err := c.Do(ctx, http.MethodGet, path, nil, &detail); err != nil {
		return refusalExit(err)
	}

	fmt.Print(renderShow(detail, time.Now()))
	return nil
}

// resolveTeam resolves the team both screens target.
//
// The overview module REQUIRES a `?team=<slug>`: there is no server-side default, by design —
// tenancy depends on a server resolution of the slug, never on an identifier supplied by the
// client. The convenience is therefore built here, in this order:
//
//  1. `--team`, which wins over everything;
//  2. the token's own team, when it carries one — the team-scoped admin case;
//  3. the instance's only team, when there is exactly one — the development machine case.
//
// Beyond one, the choice is REFUSED rather than guessed: an "I'll take the first" would show one
// team's state to another team's supervisor, without saying a word.
func resolveTeam(ctx context.Context, c *client.Client, flagSlug string) (string, error) {
	if flagSlug != "" {
		return flagSlug, nil
	}

	var me struct {
		Scope string `json:"scope"`
		workspaceservice.Identity
	}
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &me); err != nil {
		return "", err
	}
	if me.TeamSlug != "" {
		return me.TeamSlug, nil
	}

	var teams []workspaceservice.Team
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/teams", nil, &teams); err != nil {
		return "", err
	}
	switch len(teams) {
	case 0:
		return "", errors.New("no team on this instance — create one with `flowlio team create`")
	case 1:
		return teams[0].Slug, nil
	default:
		return "", fmt.Errorf("%d teams on this instance: pass --team <slug>", len(teams))
	}
}

// refusalExit turns an authorisation refusal into exit status 2.
//
// The 403 comes from `AdminOnly`, and its message is deliberately mute ("forbidden"): it does not
// tell a project token what it should have been. Here, on the side of the human who typed the
// command, the explanation is owed.
func refusalExit(err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
		return &exitError{
			code: forbiddenExitCode,
			err: errors.New("non-admin token: these screens read a whole team, " +
				"they require an admin token"),
		}
	}
	return err
}

// parseRef splits a human-readable `KEY-number` reference.
//
// The split happens on the LAST dash: a project key may contain one, the number may not.
func parseRef(ref string) (string, int64, error) {
	i := strings.LastIndex(ref, "-")
	if i <= 0 || i == len(ref)-1 {
		return "", 0, fmt.Errorf("invalid reference: %q (expected KEY-number, e.g. CORE-41)", ref)
	}

	number, err := strconv.ParseInt(ref[i+1:], 10, 64)
	if err != nil || number <= 0 {
		return "", 0, fmt.Errorf("invalid reference: %q (expected a number after the last dash)", ref)
	}
	return ref[:i], number, nil
}
