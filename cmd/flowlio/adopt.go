package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                        | Ligne |
// |--------------------|---------------------------------------------------------------|-------|
// | dockerRunner       | Runs one docker subcommand — injected so this file is testable  | 82    |
// | execDocker         | The real runner, backed by the docker binary on PATH            | 86    |
// | adoptCredentials   | Copies the instance's credentials out of the container, silently| 110   |
// | instanceIsRunning  | Answers whether the API container is up right now               | 147   |
// | offerToStartStack  | Asks once, then brings the stack up with docker compose         | 162   |
// | waitForCredentials | Polls until the fresh instance has written its credentials      | 193   |
// | isInteractive      | Answers whether a human can be prompted on this input           | 217   |
// | askYesNo           | Reads one yes/no answer, defaulting to yes on an empty line     | 231   |
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

	if _, err := credentials.Save(f); err != nil {
		return credentials.File{}, err
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
	ok, err := askYesNo(in, out, "Start one here with `docker compose up -d` (needs this to be the flowlio-agents repository)?")
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
	return info.Mode()&fs.ModeCharDevice != 0
}

// askYesNo reads one answer. An empty line means yes: the question is only ever asked at a point
// where continuing is what the user came for.
func askYesNo(in io.Reader, out io.Writer, question string) (bool, error) {
	_, _ = fmt.Fprintf(out, "%s [Y/n] ", question)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes", "o", "oui":
		return true, nil
	default:
		return false, nil
	}
}
