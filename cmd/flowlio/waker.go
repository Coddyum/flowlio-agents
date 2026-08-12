package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                          | Ligne |
// |---------------|-----------------------------------------------------------------|-------|
// | runWaker      | Runs the waker in the mode's transport: push (self-host) or poll   | 64    |
// | launchFor     | Builds one repo's launch closure, shared by both transports        | 124   |
// | serveRepo     | Starts one repo's loopback listener and registers it              | 191   |
// | registerLoop  | Registers with the engine and refreshes before the lease lapses   | 220   |
// | resolveAgent  | Turns a repo's stored config into a launch recipe                 | 246   |
// | plural        | The one-letter tail that keeps a count line grammatical           | 264   |
//
// Fin du sommaire.
// =====================================================================
//
// THE WAKER (DESIGN-WAKE §5, §8). A mode of this same `flowlio` program — not a second binary — that
// runs on the user's machine, next to where the agents' code and credentials already live. For every
// repository connected on this host it registers a loopback callback with the engine, and when an
// event drops the engine POSTs to it; the waker launches the configured agent in that repo's
// directory, under a relaunch cap, and returns to waiting.
//
// v1 is one machine (DESIGN-WAKE §10): every repo with a filed path is served. The engine push is
// the transport; the hosted escalation ladder (a laptop behind a NAT the engine cannot reach) is the
// next lot, and is noted where it belongs rather than half-built here.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	effortpkg "github.com/Coddyum/flowlio-agents/internal/pkg/effort"
	"github.com/Coddyum/flowlio-agents/internal/pkg/waker"
)

const (
	// capLimit / capWindow bound relaunches per repo: a pair of repos answering each other cannot
	// burn a session in mutual wake-ups (DESIGN-WAKE §9).
	capLimit  = 5
	capWindow = 10 * time.Minute
	// leaseRefresh re-registers well inside the engine's 15-minute lease, so a lost refresh does not
	// leave the repo unreachable for a full window.
	leaseRefresh = 5 * time.Minute
	// sessionLimitPause is how long a repo is held after its agent hit the account session limit.
	// Retrying before then is certain to fail; a limit resets on its own, and a held repo tries again
	// after this and re-holds if it is still limited — polling the wall every half hour, not every
	// minute (FLWL-85).
	sessionLimitPause = 30 * time.Minute
)

// runWaker starts the waker for every repository connected on this host, in the transport the mode
// dictates: self-host is PUSHED to (a loopback listener + registration); hosted, behind a NAT the
// engine cannot reach, POLLS the escalation ladder (DESIGN-WAKE §5, §6).
//
// It blocks until interrupted. A repo with no filed path is skipped with a line to stderr rather
// than guessed at: launching an agent in the wrong directory is worse than not launching it.
func runWaker(ctx context.Context, mode upMode) error {
	repos, err := credentials.ListRepos()
	if err != nil {
		return fmt.Errorf("listing connected repositories: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// Hosted needs the account link before anything: the waker polls flowlio-core's relay with it,
	// not the engine. Loading it once here fails fast with the command that fixes it.
	var hosted hostedConfig
	if mode == modeHosted {
		var err error
		if hosted, err = loadHosted(); err != nil {
			return fmt.Errorf("hosted mode but not logged in — run `flowlio login <prod-url>`: %w", err)
		}
	}

	cap := waker.NewCap(capLimit, capWindow)
	ceiling := wakeCeiling()
	served := 0
	for _, rf := range repos {
		if rf.Path == "" {
			fmt.Fprintf(os.Stderr, "flowlio waker: %s/%s has no filed path — run `flowlio connect %s` "+
				"from its root; skipped\n", rf.Project, rf.Repo, rf.Repo)
			continue
		}
		var startErr error
		if mode == modeHosted {
			startErr = pollRepo(ctx, rf, cap, hosted, ceiling)
		} else {
			startErr = serveRepo(ctx, rf, cap, ceiling)
		}
		if startErr != nil {
			fmt.Fprintf(os.Stderr, "flowlio waker: %s/%s not served: %v\n", rf.Project, rf.Repo, startErr)
			continue
		}
		served++
	}

	if served == 0 {
		return errors.New("no repository could be served — connect at least one with `flowlio connect <REPO>`")
	}
	transport := "waiting for local wakes"
	if mode == modeHosted {
		transport = "polling the escalation ladder"
	}
	fmt.Fprintf(os.Stderr, "flowlio waker: serving %d repositor%s; %s (Ctrl-C to stop)\n",
		served, plural(served), transport)

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "flowlio waker: stopping")
	return nil
}

// launchFor builds the launch closure for one repository: it resolves the agent, reads any known
// Claude session (for resume), and returns a function that runs the agent under the shared cap. It
// is the one place the two transports — push and poll — share, so a wake means the same thing on
// both.
func launchFor(ctx context.Context, rf credentials.RepoFile, cap *waker.Cap, hosted hostedConfig, ceiling string) (func(effort string), error) {
	agent, err := resolveAgent(rf)
	if err != nil {
		return nil, err
	}
	repo := waker.Repo{
		Project: rf.Project,
		Key:     rf.Repo,
		Path:    rf.Path,
		Agent:   agent,
	}

	// Hosted Claude authenticates the engine's MCP with the account token, not an OAuth flow a
	// `claude -p` cannot run: write the launch-time config and point Claude at it with
	// `--strict-mcp-config`. Only Claude has a resume/MCP recipe here; codex/opencode and self-host
	// keep the repo's own resolution.
	if rf.RepoID != "" && agent.Name == "claude" {
		cfg, err := writeHostedMCPConfig(rf, hosted.APIURL, hosted.AccountToken)
		if err != nil {
			return nil, fmt.Errorf("preparing %s launch config: %w", rf.Repo, err)
		}
		repo.ExtraArgs = []string{"--mcp-config", cfg, "--strict-mcp-config"}
	}

	logPath, err := agentLogPath(rf)
	if err != nil {
		return nil, err
	}
	launcher := newAgentLauncher(logPath)

	return func(effort string) {
		// Read the session id AT LAUNCH, not once at startup: a resume points at a specific Claude
		// session, and between two wakes that session can be replaced by a newer one (the SessionStart
		// hook refiled it) or cleared entirely. A stale id baked in at startup meant the waker retried a
		// dead session on every wake until it was restarted.
		repo.SessionID = loadSession(rf)
		// Clamp the tier the transport carried to this daemon's ceiling before it selects a model: the
		// sender proposed, the receiver disposes (internal/pkg/effort). An empty tier folds to standard,
		// so every wake picks a model deliberately rather than inheriting the human's interactive default.
		repo.Effort = effortpkg.Clamp(effort, ceiling)
		wakeLog(rf.Repo, "wake — launching %s", agent.Name)
		start := time.Now()
		launched, err := waker.Launch(ctx, cap, launcher, repo, start)
		// Feed the outcome back into the breaker so a run of failures backs a repo off instead of
		// retrying every cadence into a wall (FLWL-85). A session limit is a hard stop retrying cannot
		// clear before it resets, so it earns a longer, explicit pause rather than the failure ramp.
		switch {
		case err != nil && sessionLimited(logPath):
			cap.Block(rf.Repo, start, sessionLimitPause)
			wakeLog(rf.Repo, "paused — account session limit reached, holding %s", sessionLimitPause)
		case err != nil:
			cap.RecordOutcome(rf.Repo, start, false)
			wakeLog(rf.Repo, "failed — %v (log: %s)", err, logPath)
		case !launched:
			wakeLog(rf.Repo, "wake held — rate cap or failure backoff")
		default:
			cap.RecordOutcome(rf.Repo, start, true)
			wakeLog(rf.Repo, "done — %s", time.Since(start).Round(time.Second))
		}
	}, nil
}

// serveRepo starts one repository's loopback listener and registers its callback with the engine.
//
// The listener binds 127.0.0.1:0 — the kernel picks a free port, which then composes the callback,
// so two repos never fight over one number. The launch runs the configured agent in the repo's
// directory, under the shared cap.
func serveRepo(ctx context.Context, rf credentials.RepoFile, cap *waker.Cap, ceiling string) error {
	// Self-host: no account link, and the repo's own `.mcp.json` resolves to a local token, so there
	// is no launch-time MCP config to write. An empty hostedConfig says exactly that.
	launch, err := launchFor(ctx, rf, cap, hostedConfig{}, ceiling)
	if err != nil {
		return err
	}
	secret, err := waker.NewSecret()
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("binding a loopback port: %w", err)
	}
	srv := &http.Server{Handler: waker.NewListener(secret, launch), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() { <-ctx.Done(); _ = srv.Close() }()

	callback := fmt.Sprintf("http://%s/wake", ln.Addr().String())
	api := client.New(rf.APIURL, rf.Token)
	go registerLoop(ctx, api, rf.Repo, callback, secret)
	return nil
}

// registerLoop registers with the engine and refreshes before the lease lapses. The first
// registration is synchronous-ish (its failure is logged, not fatal): a transient engine outage must
// not take the whole waker down, and the next tick retries.
func registerLoop(ctx context.Context, api *client.Client, repo, callback, secret string) {
	body := map[string]string{"callback": callback, "secret": secret}
	register := func() {
		var out struct {
			LeaseSeconds int `json:"lease_seconds"`
		}
		if err := api.Do(ctx, http.MethodPost, "/api/wake/register", body, &out); err != nil {
			fmt.Fprintf(os.Stderr, "flowlio waker: %s registration failed: %v\n", repo, err)
		}
	}

	register()
	ticker := time.NewTicker(leaseRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			register()
		}
	}
}

// resolveAgent turns a repo's stored config into a launch recipe: a custom template if one was set,
// otherwise the named preset, defaulting to Claude — the one agent whose resume path FLWL-8 verified.
func resolveAgent(rf credentials.RepoFile) (waker.Agent, error) {
	if rf.AgentCommand != "" {
		if agent, ok := waker.Custom(rf.AgentCommand); ok {
			return agent, nil
		}
		return waker.Agent{}, fmt.Errorf("unusable custom agent command %q", rf.AgentCommand)
	}
	name := rf.Agent
	if name == "" {
		name = "claude"
	}
	if agent, ok := waker.Preset(name); ok {
		return agent, nil
	}
	return waker.Agent{}, fmt.Errorf("unknown agent %q (known: claude, codex, opencode, or a custom command)", name)
}

// plural is the one-letter tail that keeps a count line grammatical.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
