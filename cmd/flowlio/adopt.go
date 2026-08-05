package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                        | Ligne |
// |--------------------|---------------------------------------------------------------|-------|
// | dockerRunner       | Runs one docker subcommand — injected so this file is testable  | 84    |
// | execDocker         | The real runner, backed by the docker binary on PATH            | 88    |
// | adoptCredentials   | Copies the instance's credentials out of the container, silently| 112   |
// | instanceCredentials| Reads the instance's credentials without writing on the host    | 129   |
// | instanceIsRunning  | Answers whether the API container is up right now               | 162   |
// | offerToStartStack  | Asks once, then brings the stack up with docker compose         | 177   |
// | waitForCredentials | Polls until the fresh instance has written its credentials      | 208   |
// | isInteractive      | Answers whether a human can be prompted on this input           | 232   |
// | askYesNo           | Reads one yes/no answer, defaulting to yes on an empty line     | 262   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THE CLI TALKS TO DOCKER.
//
// Until now the admin token reached its owner through `docker compose logs api`: a secret
// recovered by grepping a log, then pasted into two exports. The API does write a credentials file
// — inside the container, where nothing on the host ever reads it.
//
// A bind mount of ~/.config into the container is the obvious fix and it does not survive Linux:
// the container runs as uid 10001, a bind-mounted host directory keeps the host's ownership
// (usually uid 1000), and the write fails. Docker Desktop hides this on macOS by virtualising
// ownership, which is exactly what would let the bug ship. So the file stays in a NAMED volume,
// whose ownership Docker initialises from the image — no uid to reconcile — and the CLI copies it
// out on the host side, running as the host user.
//
// Shelling out to docker is not a new dependency: Docker is already the single prerequisite of the
// quickstart, and this card's own third step has the CLI start the stack. If docker is absent, or
// the container is not named as expected, every path below degrades to a message telling the user
// what to do by hand.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

const (
	// apiContainer is the name docker-compose.yml pins on the API service. Pinned rather than
	// discovered through `docker compose ps`, because the command that needs it runs from the user's
	// OWN repository, where no compose file exists.
	apiContainer = "flowlio-api"
	// containerCredentialsPath is where the API writes its credentials file inside the image. The
	// image sets HOME to /home/flowlio, and a named volume is mounted on .config so the file
	// survives `docker compose up --build`.
	containerCredentialsPath = "/home/flowlio/.config/flowlio/credentials.json"
	// dockerTimeout bounds every docker call. A daemon that is starting, or not there at all, must
	// not leave the CLI hanging in an agent's non-interactive session.
	dockerTimeout = 30 * time.Second
	// stackTimeout bounds `docker compose up -d`, which may have to build the image.
	stackTimeout = 10 * time.Minute
	// instanceReadyTimeout bounds the wait between a started stack and its first credentials: the
	// API only writes them once Postgres is healthy and the migrations have run.
	instanceReadyTimeout = 2 * time.Minute
	// credentialsPollInterval is how often that wait retries.
	credentialsPollInterval = time.Second
)

// errNoInstance reports that no running API container was found. It is the signal that separates
// "adoption failed" from "there is nothing to adopt from yet".
var errNoInstance = errors.New("no running flowlio instance")

// dockerRunner runs one docker subcommand and returns its standard output.
//
// Injected rather than called directly so that every branch below is testable without a daemon:
// the tests pin the EXACT list of commands issued, which is what proves that refusing to start the
// stack really issues no `compose up`.
type dockerRunner func(ctx context.Context, args ...string) ([]byte, error)

// execDocker is the real runner. Standard error is folded into the returned error rather than the
// output, so a docker diagnostic never gets parsed as a credentials file.
func execDocker(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("docker %s: %s", strings.Join(args, " "), msg)
		}
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// adoptCredentials copies the running instance's credentials file onto the host and returns it.
//
// SILENT AND WITHOUT SIDE EFFECTS beyond that one write: every command goes through this path, and
// an agent running `flowlio task list` in a non-interactive session must never be prompted, nor
// have containers started under it. Starting the stack is offerToStartStack's job, and only
// `flowlio init` calls it.
//
// Returns errNoInstance when there is nothing to adopt from, so the caller can tell a missing
// instance from a broken one.
func adoptCredentials(ctx context.Context, run dockerRunner) (credentials.File, error) {
	f, err := instanceCredentials(ctx, run)
	if err != nil {
		return credentials.File{}, err
	}

	if _, err := credentials.Save(f); err != nil {
		return credentials.File{}, err
	}
	return f, nil
}

// instanceCredentials reads the running instance's credentials and WRITES NOTHING on the host.
//
// Split out of adoptCredentials because a local file that outlived its instance has to be compared
// with the instance's before being overwritten: the comparison is the only thing that tells a
// leftover apart from an address someone pointed elsewhere on purpose.
func instanceCredentials(ctx context.Context, run dockerRunner) (credentials.File, error) {
	if !instanceIsRunning(ctx, run) {
		return credentials.File{}, errNoInstance
	}

	raw, err := run(ctx, "exec", apiContainer, "cat", containerCredentialsPath)
	if err != nil {
		// Named explicitly, because this is what every instance created before this version looks
		// like: it is running, it works, and its credentials file was written to a container layer
		// that has since been thrown away. The server keeps only a hash of the token, so there is
		// nothing left to read — the way out is a new admin token, not a retry.
		return credentials.File{}, fmt.Errorf(
			"container %s is running but has no credentials at %s — an instance bootstrapped before "+
				"this version left none: issue a token from a session that still has the admin one, "+
				"or recreate the instance from empty: %w",
			apiContainer, containerCredentialsPath, err)
	}

	var f credentials.File
	if err := json.Unmarshal(raw, &f); err != nil {
		return credentials.File{}, fmt.Errorf("credentials of container %s unreadable: %w", apiContainer, err)
	}
	if f.APIURL == "" || f.Token == "" {
		return credentials.File{}, fmt.Errorf("credentials of container %s are incomplete", apiContainer)
	}
	return f, nil
}

// instanceIsRunning answers whether the API container is up right now.
//
// `docker inspect` on the state, not `docker ps` filtered by name: a container that exists but is
// stopped must read as "not running", and a substring match on a name list would call
// flowlio-api-old a running instance.
func instanceIsRunning(ctx context.Context, run dockerRunner) bool {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	out, err := run(ctx, "inspect", "-f", "{{.State.Running}}", apiContainer)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// offerToStartStack asks once whether to bring the stack up, and does it on a yes.
//
// One question, not three: starting containers is the only step here with a side effect the user
// might not want, and a prompt they answer without reading is worth less than no prompt at all.
func offerToStartStack(ctx context.Context, run dockerRunner, in io.Reader, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "No flowlio instance is running (container %s).\n", apiContainer)

	// The question names the directory: `docker compose up -d` reads the compose file of the CURRENT
	// one, so this only works from a flowlio-agents checkout. Asking without saying so would get a
	// yes from a user standing in their own repository, and hand them a compose error instead of an
	// instance.
	ok, err := askYesNo(in, out, "Start one here with `docker compose up -d` (needs this to be the flowlio-agents repository)?", true)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no instance started — run `docker compose up -d` from the flowlio-agents repository, then try again")
	}

	_, _ = fmt.Fprintln(out, "Starting the stack — the first run builds the image, which takes a moment.")

	startCtx, cancel := context.WithTimeout(ctx, stackTimeout)
	defer cancel()
	if _, err := run(startCtx, "compose", "up", "-d"); err != nil {
		return fmt.Errorf("%w — is the current directory the flowlio-agents repository?", err)
	}
	return nil
}

// waitForCredentials polls until the instance has written its credentials, or the deadline passes.
//
// `docker compose up -d` returns as soon as the containers are created, which is before Postgres is
// healthy and well before the API has bootstrapped its admin token. Reading once and giving up
// would send a user who did everything right back to the logs — the exact outcome this card
// removes.
func waitForCredentials(ctx context.Context, run dockerRunner, every time.Duration) (credentials.File, error) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	var last error
	for {
		f, err := adoptCredentials(ctx, run)
		if err == nil {
			return f, nil
		}
		last = err

		select {
		case <-ctx.Done():
			return credentials.File{}, fmt.Errorf("instance still not ready: %w", last)
		case <-ticker.C:
		}
	}
}

// isInteractive answers whether r is a terminal a human can answer from.
//
// Nothing may prompt when it is not: an agent runs this CLI with no terminal attached, and a
// question asked there is a session that hangs until it is killed.
func isInteractive(r io.Reader) bool {
	f, ok := r.(interface{ Stat() (fs.FileInfo, error) })
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&fs.ModeCharDevice == 0 {
		return false
	}

	// /dev/null IS a character device, and `flowlio init < /dev/null` is exactly how an agent runs a
	// command it has no intention of answering. Counted as a terminal, it got prompted, and askYesNo
	// reads the immediate EOF as an empty line — that is, as yes. A question guarding an overwrite
	// was therefore answered by nobody. Observed on macOS while reproducing FLWL-69.
	if null, statErr := os.Stat(os.DevNull); statErr == nil && os.SameFile(null, info) {
		return false
	}
	return true
}

// askYesNo reads one answer, and defaultYes decides what an empty line — or an immediate EOF —
// means.
//
// THE DEFAULT IS PER QUESTION, not per CLI. Starting a stack is additive and it is what the user
// came for, so a bare Enter is a yes there. Overwriting the credentials file is neither: it may be
// pointing where someone put it, and a stdin that ends without a word is not consent. Ctrl-D is the
// same keystroke in both cases, which is exactly why the caller has to say which risk it is taking.
func askYesNo(in io.Reader, out io.Writer, question string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	_, _ = fmt.Fprintf(out, "%s %s ", question, suffix)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes, nil
	case "y", "yes", "o", "oui":
		return true, nil
	default:
		return false, nil
	}
}
