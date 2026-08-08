package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | runTrust           | Sub-commands that edit the trust graph                      | 56    |
// | trustList          | Prints the graph with its directions, or what to type        | 106   |
// | trustAllow         | Opens one directed edge and confirms it                     | 146   |
// | trustDeny          | Cuts one directed edge, naming the real circuit breaker      | 172   |
// | trustPath          | Builds a trust route's path with its team                   | 197   |
// | explainAdminToken  | Turns a 403 into advice about the right token               | 211   |
// | possibleEdges      | Number of possible directed edges in a team of n projects    | 226   |
// | joinKeys           | Lists project keys, comma-separated                         | 234   |
// | teamOption         | Renders the --team flag to copy into the suggested command  | 244   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio trust` is the ONLY surface where the truth of the graph is readable, and the first
// command typed by a human whose agent has just been handed `not found` on a create_issue. It is
// therefore written to be read by someone who does not yet know what is happening to them.
//
// THE EDGE IS DIRECTED (migration 000013), AND EVERY LINE PRINTED HERE SAYS SO. `WEB → CORE` means
// WEB may open a question at CORE, and nothing about the other way round. Rendering the graph as a
// list of pairs after that migration would have been a screen that quietly stopped being true —
// no test failing, no build breaking, no lint complaining — while the customer read a symmetry the
// database no longer holds. That is the FLWL-78 failure mode, and it is why the arrow is asserted
// in trust_test.go rather than left to a re-reading.
//
// None of these three commands exists on the MCP side, and that is the decision: an agent is
// SUBJECT to the graph. Letting it read the graph would hand it the map of what it can reach;
// letting it write the graph would let it sign its own authorisation.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runTrust routes the three sub-commands of the trust graph.
//
// `deny` and not `revoke`: `token revoke` already exists and cuts EVERYTHING, immediately. Two
// identical verbs for two gestures, one of which contains an incident and the other does not, would
// be confused on the day of an incident — that is, the only day it matters.
//
// The two positional arguments are <from> and <to>, in that order, on both write verbs. They are
// never sorted: `allow WEB CORE` and `allow CORE WEB` are two different declarations.
func runTrust(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio trust list | allow <from> <to> | deny <from> <to> [--team slug]")
	}

	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
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
	case "list":
		return explainAdminToken(trustList(ctx, c, *team))

	case "allow":
		if len(positional) < 2 {
			return errors.New("usage: flowlio trust allow <from> <to> [--team slug]")
		}
		return explainAdminToken(trustAllow(ctx, c, *team, positional[0], positional[1]))

	case "deny":
		if len(positional) < 2 {
			return errors.New("usage: flowlio trust deny <from> <to> [--team slug]")
		}
		return explainAdminToken(trustDeny(ctx, c, *team, positional[0], positional[1]))

	default:
		return fmt.Errorf("unknown trust sub-command: %s", sub)
	}
}

// trustList prints a team's graph, ONE LINE PER DIRECTION.
//
// The arrow is the content of the line, not decoration: two repos that may question each other show
// as two lines, and a repo that may question without being questionable shows as one. Printing a
// pair would make those two graphs look identical on screen while the database tells them apart.
//
// An EMPTY graph stays the most important case of this whole command: it is the state a human lands
// in after an agent told them "not found". The output does not merely print nothing — it names the
// projects and gives the exact command to type. No oracle here: this route is admin, and an admin
// can already enumerate every project of every team.
func trustList(ctx context.Context, c *client.Client, team string) error {
	var edges []service.TrustEdge
	if err := c.Do(ctx, http.MethodGet, trustPath(team, ""), nil, &edges); err != nil {
		return err
	}

	var projects []service.Project
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(team), nil, &projects); err != nil {
		return err
	}

	if len(edges) == 0 {
		fmt.Println("\n  no trust declared — the cross-project channel is closed for this team.")
		if len(projects) < 2 {
			fmt.Println("  this team holds fewer than two projects: there is no possible edge.")
			return nil
		}
		fmt.Printf("  projects: %s\n", joinKeys(projects))
		fmt.Printf("  open one direction:  flowlio trust allow %s %s%s\n\n",
			projects[0].Key, projects[1].Key, teamOption(team))
		return nil
	}

	fmt.Println()
	fmt.Println("  <from> may open a question at <to>. Each direction is declared on its own.")
	for _, e := range edges {
		fmt.Printf("  %s → %-12s since %s\n", e.From, e.To, e.CreatedAt.Format("2006-01-02"))
	}

	total := possibleEdges(len(projects))
	fmt.Printf("\n  %d directed edge(s) out of %d possible.\n\n", len(edges), total)
	return nil
}

// trustAllow opens ONE edge. Idempotent, and says so: a replay is not an error, but letting the
// human believe they just changed something would be one.
//
// The confirmation names the direction it just opened AND the one it did not. Without that second
// sentence, somebody who ran `trust allow WEB CORE` would reasonably read "the two repos can talk"
// and only discover the truth when CORE's agent gets a `not found` it cannot explain.
func trustAllow(ctx context.Context, c *client.Client, team, from, to string) error {
	var decision service.TrustDecision
	in := service.TrustPairInput{From: from, To: to}
	if err := c.Do(ctx, http.MethodPost, trustPath(team, ""), in, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s → %s: already allowed, nothing to do.\n", decision.From, decision.To)
		return nil
	}
	fmt.Printf("%s → %s: %s can now raise issues at %s.\n",
		decision.From, decision.To, decision.From, decision.To)
	fmt.Printf("The other way round is a separate declaration: flowlio trust allow %s %s%s\n",
		decision.To, decision.From, teamOption(team))
	return nil
}

// trustDeny cuts ONE edge.
//
// THE LAST THREE LINES OF THIS OUTPUT ARE THE MOST IMPORTANT OF THE COMMAND. They say that
// `trust deny` is NOT a containment tool, and they name the one that is. Without them, a human who
// has just discovered that a repo is compromised would believe they had cut it off, while every
// thread already open stays answerable, with no time bound. The first of the three now also says
// that the opposite direction is still standing — under a directed graph, "I cut CORE off" is one
// command short of true.
func trustDeny(ctx context.Context, c *client.Client, team, from, to string) error {
	path := trustPath(team, url.PathEscape(from)+"/"+url.PathEscape(to))

	var decision service.TrustDecision
	if err := c.Do(ctx, http.MethodDelete, path, nil, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s → %s: no trust declared, nothing to withdraw.\n", decision.From, decision.To)
		return nil
	}

	fmt.Printf("%s → %s: trust withdrawn. %s can no longer open an issue at %s.\n",
		decision.From, decision.To, decision.From, decision.To)
	fmt.Printf("The other direction is untouched: cut it with flowlio trust deny %s %s%s\n",
		decision.To, decision.From, teamOption(team))
	fmt.Println("Threads already open stay readable and answerable.")
	fmt.Println("To cut a compromised repo off immediately: flowlio token revoke <id>.")
	return nil
}

// trustPath builds a trust route's path. suffix carries the DELETE's two keys IN ORDER, from then
// to, and is empty elsewhere — keys validate against ^[A-Z][A-Z0-9]{1,9}$, so they are safe as a
// URL segment.
func trustPath(team, suffix string) string {
	path := workspaceAPI + "/trust"
	if suffix != "" {
		path += "/" + suffix
	}
	return path + teamQuery(team)
}

// explainAdminToken turns a bare 403 into advice.
//
// Without it, the human who has just followed `flowlio init` and exported FLOWLIO_TOKEN gets
// `flowlio: api: Forbidden`, one command after being told to export the wrong token. This is the
// only place in the product where a 403 is expected ON A HANDLING MISTAKE rather than on an
// attempt: the message therefore says what to do, not what is forbidden.
func explainAdminToken(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		return err
	}
	return fmt.Errorf(`%w
        this command wants the ADMIN token, not the agent token that "flowlio init"
        prints. It lives in ~/.config/flowlio/credentials.json: run again with
        FLOWLIO_TOKEN unset in the environment`, err)
}

// possibleEdges returns n(n−1), the number of DIRECTED edges in a team of n projects — twice the
// number of pairs, because each pair is two independent declarations. The figure is there to say
// "2 out of 6" rather than "2", which is the only way to see at a glance that something is still
// left to open.
func possibleEdges(n int) int {
	if n < 2 {
		return 0
	}
	return n * (n - 1)
}

// joinKeys lists project keys, comma-separated.
func joinKeys(projects []service.Project) string {
	keys := make([]string, 0, len(projects))
	for _, p := range projects {
		keys = append(keys, p.Key)
	}
	return strings.Join(keys, ", ")
}

// teamOption renders the --team flag to copy as is into the suggested command, empty when the team
// was not given — that is, when the token already carries one.
func teamOption(team string) string {
	if team == "" {
		return ""
	}
	return " --team " + team
}
