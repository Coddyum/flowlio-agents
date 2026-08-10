package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | checkOutcome     | One line of the report: what was checked and what came of it     | 41    |
// | runDoctor        | Plays every check that applies here and reports on all of them   | 58    |
// | connected        | Whether this directory is one connect has been run in            | 80    |
// | hostChecks       | The two checks that hold anywhere: instance and admin credential | 91    |
// | reportChecks     | Prints one line per check and decides the exit status            | 119   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio doctor` answers one question: is this repository actually going to work when an agent
// opens it? It replays the ground `connect` covered, later, when something has drifted — a moved
// instance, a revoked token, a workflow file left at an older version.
//
// EVERY CHECK RUNS. Unlike `connect`'s self-test, which stops at the first failure because the ones
// after it would be meaningless, this command is a report: somebody runs it precisely because they
// do not know what is wrong, and three red lines locate a problem that one red line does not.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// checkOutcome is one line of the report.
//
// The cause is carried as a STRING and not an error: half of these lines have no error to wrap —
// "the workflow file is at version 1" is a finding, not a failure of anything — and a report that
// mixed the two would have to explain which is which.
type checkOutcome struct {
	// label says what was checked, in the words somebody would use to describe the problem.
	label string
	// ok is the verdict.
	ok bool
	// cause is what went wrong, empty when nothing did.
	cause string
	// skipped marks a check that could not be played rather than one that failed. A red line for
	// something nobody could look at teaches the reader to distrust the whole report.
	skipped bool
}

// runDoctor plays every check that applies where it is run.
//
// Outside a connected repository only the host checks run: `doctor` has to be usable from anywhere
// to answer "is the instance up and am I who I think I am", and refusing to run outside a
// repository would make that impossible.
func runDoctor(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return errors.New("usage: flowlio doctor   (run from a connected repository, or anywhere)")
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory not found: %w", err)
	}

	outcomes := hostChecks(ctx)
	if connected(dir) {
		outcomes = append(outcomes, repoChecks(ctx, dir)...)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "No %s with a %q entry here: checking the host only.\n\n",
			mcpConfigName, mcpServerKey)
	}

	return reportChecks(os.Stdout, outcomes)
}

// connected says whether this directory is a repository `connect` has been run in.
func connected(dir string) bool {
	_, servers, err := readMCPFile(filepath.Join(dir, mcpConfigName))
	if err != nil {
		return false
	}
	_, found := servers[mcpServerKey]
	return found
}

// hostChecks are the two that hold anywhere: the instance answers, and this host holds the admin
// credential.
func hostChecks(ctx context.Context) []checkOutcome {
	admin, err := credentials.Load()
	if err != nil {
		cause := err.Error()
		if errors.Is(err, credentials.ErrNotFound) {
			cause = "there is none on this host: run `flowlio setup`"
		}
		// Without an address there is nothing to reach, so the first check cannot be played rather
		// than being failed on a guess.
		return []checkOutcome{
			{label: "the admin credential is readable", cause: cause},
			{label: "the instance answers", skipped: true, cause: "no address to try"},
		}
	}

	out := []checkOutcome{{label: "the admin credential is readable", ok: true}}
	if dead := unreachableAPI(ctx, client.New(admin.APIURL, admin.Token)); dead != nil {
		out = append(out, checkOutcome{
			label: "the instance answers at " + admin.APIURL,
			cause: dead.Error(),
		})
		return out
	}
	return append(out, checkOutcome{label: "the instance answers at " + admin.APIURL, ok: true})
}

// reportChecks prints one line per check and decides the status. A skipped check never fails the
// command: nothing was observed, so nothing is asserted.
func reportChecks(out io.Writer, outcomes []checkOutcome) error {
	failed := 0
	for _, o := range outcomes {
		switch {
		case o.skipped:
			_, _ = fmt.Fprintf(out, "  skip  %s — %s\n", o.label, o.cause)
		case o.ok:
			_, _ = fmt.Fprintf(out, "  ok    %s\n", o.label)
		default:
			failed++
			_, _ = fmt.Fprintf(out, "  fail  %s — %s\n", o.label, o.cause)
		}
	}

	if failed == 0 {
		return nil
	}
	return &exitError{code: 1, err: fmt.Errorf("%d of %d checks failed", failed, len(outcomes))}
}
