package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                             | Ligne |
// |------------|--------------------------------------------------------------------|-------|
// | upMode     | Which half of the product this machine operates                     | 40    |
// | upRunners  | The injected actions a plan is made of, real or faked in a test      | 49    |
// | upStep     | One named action of a plan                                           | 59    |
// | upPlan     | Composes the ordered steps for a mode — the whole of the sequencing  | 70    |
// | runUp      | Detects the mode and runs the plan, one step at a time               | 85    |
// | detectMode | Reads whether this host operates the engine or only a waker          | 137   |
// | runProcess | Runs a command to completion, its output on stderr                  | 155   |
//
// Fin du sommaire.
// =====================================================================
//
// ONE INSTALL, ONE COMMAND (DESIGN-WAKE §4.1, §5, §6 — the packaging decision of §12). After
// `brew install flowlio`, a single `flowlio up` runs everything a mode needs, in one terminal:
//
//	self-host : a Postgres 18 container flowlio manages + the engine (which self-applies migrations
//	            locally, D32) + the waker — one process tree.
//	hosted    : the waker only; the engine runs on our infra (D25), reached over the network.
//
// The waker is NOT a second binary: it is a mode of this same program. The sequencing lives in
// upPlan as data, so it is testable without a Docker daemon or a live engine; only the runners touch
// the outside world.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"
)

// upMode is which half of the product this machine operates. The difference is who runs the engine,
// never what this repository knows how to do (D24).
type upMode int

const (
	modeSelfHost upMode = iota
	modeHosted
)

// upRunners are the outside-world actions a plan is composed of. They are injected so upPlan — the
// part that decides WHAT runs and in WHICH ORDER — is unit-tested without a container or a server.
type upRunners struct {
	// dbUp brings up the managed Postgres 18 container (loopback-bound, D38).
	dbUp func(context.Context) error
	// engine starts the API process; it self-applies migrations in local mode (D32) and blocks.
	engine func(context.Context) error
	// waker starts the waker; it blocks until interrupted.
	waker func(context.Context) error
}

// upStep is one named action of a plan.
type upStep struct {
	name   string
	detail string
	run    func(context.Context) error
}

// upPlan composes the ordered steps for a mode. This is the whole of the sequencing, and it is data:
// self-host brings up the database, then the engine, then the waker; hosted runs the waker alone.
//
// The engine is started in the BACKGROUND by its runner (it must not block the waker that follows),
// and the waker is the FOREGROUND step that holds the terminal until Ctrl-C.
func upPlan(m upMode, r upRunners) []upStep {
	if m == modeHosted {
		return []upStep{
			{"waker", "watch for answers and relaunch the agent (engine runs on our infra)", r.waker},
		}
	}
	return []upStep{
		{"database", "Postgres 18 in a managed container, data in a volume", r.dbUp},
		{"engine", "the API, self-applying migrations locally", r.engine},
		{"waker", "watch for answers and relaunch the agent", r.waker},
	}
}

// runUp detects the mode and runs its plan, one step at a time, stopping at the first that fails. The
// last step (the waker) blocks: `flowlio up` is meant to hold one terminal with everything running.
func runUp(ctx context.Context, _ []string) error {
	mode, err := detectMode()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	if mode == modeHosted {
		if cfg, cfgErr := loadHosted(); cfgErr == nil && cfg.APIURL != "" {
			fmt.Fprintf(os.Stderr, "flowlio up: hosted — engine at %s\n", cfg.APIURL)
		}
	}

	engineBin := os.Getenv("FLOWLIO_ENGINE_BIN")
	if engineBin == "" {
		engineBin = "flowlio-api"
	}
	runners := upRunners{
		dbUp: func(c context.Context) error {
			return runProcess(c, "docker", "compose", "up", "-d")
		},
		engine: func(c context.Context) error {
			// Background: launched and left running while the waker takes the foreground.
			cmd := exec.CommandContext(c, engineBin)
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("starting the engine (%s): %w", engineBin, err)
			}
			// Give it a moment to bind before the waker tries to register against it.
			time.Sleep(time.Second)
			return nil
		},
		waker: func(c context.Context) error { return runWaker(c, mode) },
	}

	for _, step := range upPlan(mode, runners) {
		fmt.Fprintf(os.Stderr, "flowlio up: %s — %s\n", step.name, step.detail)
		if err := step.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// detectMode reads whether this host operates the engine (self-host) or only a waker (hosted).
//
// Explicit wins: FLOWLIO_MODE=hosted|self-host settles it. Otherwise a filed hosted login makes it
// hosted — `flowlio login` is how a user opts in and it persists. Failing both, self-host is the
// default, because it is the mode that needs the container and the engine and would fail loudest if
// guessed wrong.
func detectMode() (upMode, error) {
	switch os.Getenv("FLOWLIO_MODE") {
	case "hosted":
		return modeHosted, nil
	case "self-host":
		return modeSelfHost, nil
	case "":
		if hostedLoggedIn() {
			return modeHosted, nil
		}
		return modeSelfHost, nil
	default:
		return modeSelfHost, fmt.Errorf("unknown FLOWLIO_MODE %q (expected self-host or hosted)", os.Getenv("FLOWLIO_MODE"))
	}
}

// runProcess runs a command to completion, its output on stderr. It exists so a plan step can shell
// out without each one re-plumbing stdio.
func runProcess(ctx context.Context, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not on PATH: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

