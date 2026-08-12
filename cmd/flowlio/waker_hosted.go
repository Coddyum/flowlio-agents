package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                            | Ligne |
// |------------|-------------------------------------------------------------------|-------|
// | pollRepo   | Starts the hosted poll loop for one repository, against core        | 45    |
// | pollLoop   | Probes on the server-dictated cadence until interrupted             | 63    |
// | probeOnce  | One probe of core's relay: launch on work, return the next delay    | 80    |
//
// Fin du sommaire.
// =====================================================================
//
// THE HOSTED TRANSPORT (DESIGN-WAKE §6). A laptop behind a NAT is not reachable from prod, so the
// engine cannot push to it — the waker POLLS. And in hosted it does NOT talk to the engine directly:
// the project token lives inside flowlio-core, walled from this machine. It polls flowlio-core's wake
// RELAY (`/api/v2/agents/wake?repo=<id>`) with the ACCOUNT bearer `flowlio login` filed, and core
// forwards the probe to the engine as the repository (see flowlio-core's mcp/wake.go).
//
// It never chooses its own cadence: the relay carries the engine's next_probe_after, and a probe too
// soon takes a 429. The client honours whichever the server sends, so a misconfigured daemon cannot
// cost the day (§3). The zero-SQL probe makes an idle repo nearly free to poll; the piggyback frees
// active agents from polling at all.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	"github.com/Coddyum/flowlio-agents/internal/pkg/waker"
)

// pollRepo starts the hosted poll loop for one repository, against flowlio-core's relay. It returns
// at once: the loop runs in its own goroutine, so one waker covers every connected repo in parallel.
//
// A repo with no core id cannot be polled — it was connected in self-host, or without `--id`. That
// is a configuration gap named to stderr, not a launch in the dark.
func pollRepo(ctx context.Context, rf credentials.RepoFile, cap *waker.Cap, hosted hostedConfig, ceiling string) error {
	if rf.RepoID == "" {
		return errors.New("no core repository id — run `flowlio connect " + rf.Repo + " --id <id>` (from flowlio.me)")
	}
	launch, err := launchFor(ctx, rf, cap, hosted, ceiling)
	if err != nil {
		return err
	}
	// The account bearer, not a project token: in hosted this machine holds no project token, and
	// the relay is what turns the account identity into a scoped engine call.
	api := client.New(hosted.APIURL, hosted.AccountToken)
	relay := "/api/v2/agents/wake?repo=" + url.QueryEscape(rf.RepoID)
	go pollLoop(ctx, api, rf.Repo, relay, launch)
	return nil
}

// pollLoop probes on the server-dictated cadence until the context is cancelled. The delay between
// probes is never the client's own: it is what probeOnce read from the last reply.
func pollLoop(ctx context.Context, api *client.Client, repo, relay string, launch func(effort string)) {
	for {
		delay := probeOnce(ctx, api, repo, relay, launch)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// probeOnce runs one probe of core's relay, launches the agent when there is work, and returns how
// long to wait before the next one — always the value the SERVER dictated.
//
// A 429 means the client came back too soon: the wait is the Retry-After it carries. A transport or
// upstream failure is a plain back-off — core or the engine may be briefly unreachable, which must
// not become a busy loop.
func probeOnce(ctx context.Context, api *client.Client, repo, relay string, launch func(effort string)) time.Duration {
	var res struct {
		HasWork        bool   `json:"has_work"`
		NextProbeAfter int    `json:"next_probe_after"`
		SuggestedEffort string `json:"suggested_effort"`
	}
	err := api.Do(ctx, http.MethodGet, relay, nil, &res)
	if err == nil {
		if res.HasWork {
			// The engine's suggested tier reaches here verbatim through flowlio-core's relay; the daemon
			// clamps it to its own ceiling inside launch. Absent (an engine that predates the field, or no
			// pending work with a tier) launches at standard.
			launch(res.SuggestedEffort)
		}
		return waker.ProbeDelay(res.NextProbeAfter)
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests {
		if ra := api.LastResponseHeader("Retry-After"); ra != "" {
			if n, convErr := strconv.Atoi(ra); convErr == nil {
				return waker.ProbeDelay(n)
			}
		}
		return waker.ProbeDelay(0)
	}

	fmt.Fprintf(os.Stderr, "flowlio waker: %s probe failed: %v\n", repo, err)
	return waker.ProbeDelay(0)
}
