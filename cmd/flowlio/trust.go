package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | runTrust           | Sub-commands that edit the trust graph                      | 46    |
// | trustList          | Prints the graph, or says what to type when it is empty     | 93    |
// | trustAllow         | Opens a pair and confirms it                                | 128   |
// | trustDeny          | Closes a pair, naming the real circuit breaker              | 150   |
// | trustPath          | Builds a trust route's path with its team                   | 172   |
// | explainAdminToken  | Turns a 403 into advice about the right token               | 186   |
// | possiblePairs      | Number of possible pairs in a team of n projects            | 200   |
// | joinKeys           | Lists project keys, comma-separated                         | 208   |
// | teamOption         | Renders the --team flag to copy into the suggested command  | 218   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio trust` is the ONLY surface where the truth of the graph is readable, and the first
// command typed by a human whose agent has just been handed `not found` on a create_issue. It is
// therefore written to be read by someone who does not yet know what is happening to them.
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
func runTrust(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio trust list | allow <A> <B> | deny <A> <B> [--team slug]")
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
			return errors.New("usage: flowlio trust allow <A> <B> [--team slug]")
		}
		return explainAdminToken(trustAllow(ctx, c, *team, positional[0], positional[1]))

	case "deny":
		if len(positional) < 2 {
			return errors.New("usage: flowlio trust deny <A> <B> [--team slug]")
		}
		return explainAdminToken(trustDeny(ctx, c, *team, positional[0], positional[1]))

	default:
		return fmt.Errorf("unknown trust sub-command: %s", sub)
	}
}

// trustList prints a team's graph.
//
// An EMPTY graph is the most important case of this whole command: it is every team's default state
// after the migration, so it is the state a human arrives in after an agent told them "not found".
// The output does not merely print nothing — it names the projects and gives the exact command to
// type. No oracle here: this route is admin, and an admin can already enumerate every project of
// every team.
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
			fmt.Println("  this team holds fewer than two projects: there is no possible pair.")
			return nil
		}
		fmt.Printf("  projects: %s\n", joinKeys(projects))
		fmt.Printf("  open a pair:  flowlio trust allow %s %s%s\n\n",
			projects[0].Key, projects[1].Key, teamOption(team))
		return nil
	}

	fmt.Println()
	for _, e := range edges {
		fmt.Printf("  %s ↔ %-12s since %s\n", e.First, e.Second, e.CreatedAt.Format("2006-01-02"))
	}

	total := possiblePairs(len(projects))
	fmt.Printf("\n  %d pair(s) out of %d possible.\n\n", len(edges), total)
	return nil
}

// trustAllow opens a pair. Idempotent, and says so: a replay is not an error, but letting the human
// believe they just changed something would be one.
func trustAllow(ctx context.Context, c *client.Client, team, first, second string) error {
	var decision service.TrustDecision
	in := service.TrustPairInput{First: first, Second: second}
	if err := c.Do(ctx, http.MethodPost, trustPath(team, ""), in, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s ↔ %s: already allowed, nothing to do.\n", decision.First, decision.Second)
		return nil
	}
	fmt.Printf("%s ↔ %s: the two projects can now raise issues to each other.\n",
		decision.First, decision.Second)
	return nil
}

// trustDeny closes a pair.
//
// THE LAST THREE LINES OF THIS OUTPUT ARE THE MOST IMPORTANT OF THE COMMAND. They say that
// `trust deny` is NOT a containment tool, and they name the one that is. Without them, a human who
// has just discovered that a repo is compromised would believe they had cut it off, while every
// thread already open stays answerable, with no time bound.
func trustDeny(ctx context.Context, c *client.Client, team, first, second string) error {
	path := trustPath(team, url.PathEscape(first)+"/"+url.PathEscape(second))

	var decision service.TrustDecision
	if err := c.Do(ctx, http.MethodDelete, path, nil, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s ↔ %s: no trust declared, nothing to withdraw.\n", decision.First, decision.Second)
		return nil
	}

	fmt.Printf("%s ↔ %s: trust withdrawn. No new issue between these two projects.\n",
		decision.First, decision.Second)
	fmt.Println("Threads already open stay readable and answerable.")
	fmt.Println("To cut a compromised repo off immediately: flowlio token revoke <id>.")
	return nil
}

// trustPath builds a trust route's path. suffix carries the DELETE's two keys, empty elsewhere —
// keys validate against ^[A-Z][A-Z0-9]{1,9}$, so they are safe as a URL segment.
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

// possiblePairs returns n(n−1)/2, the number of pairs in a team of n projects. The figure is there
// to say "2 out of 3" rather than "2", which is the only way to see at a glance that something is
// still left to open.
func possiblePairs(n int) int {
	if n < 2 {
		return 0
	}
	return n * (n - 1) / 2
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
