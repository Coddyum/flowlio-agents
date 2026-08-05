package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// fakeDocker records every command it is handed and answers from a scripted table.
//
// It records the FULL argument list, not a count: the guarantees below are about which docker
// commands are issued and which are not, and a counter cannot tell "started the stack" from
// "inspected it twice".
type fakeDocker struct {
	calls   []string
	answers map[string]string
	fails   map[string]error
}

func (f *fakeDocker) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err, ok := f.fails[key]; ok {
		return nil, err
	}
	return []byte(f.answers[key]), nil
}

const (
	inspectCmd = "inspect -f {{.State.Running}} " + apiContainer
	catCmd     = "exec " + apiContainer + " cat " + containerCredentialsPath
	upCmd      = "compose up -d"
)

// isolateConfigHome points credentials.Path() at a temporary directory, so a test never reads or
// overwrites the real ~/.config/flowlio/credentials.json.
func isolateConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "flowlio", "credentials.json")
}

// TestAdoptCredentialsWritesThemOnTheHost is the guarantee the card is about: the token crosses
// from the container to the user without anyone reading a log.
func TestAdoptCredentialsWritesThemOnTheHost(t *testing.T) {
	path := isolateConfigHome(t)
	d := &fakeDocker{answers: map[string]string{
		inspectCmd: "true\n",
		catCmd:     `{"api_url":"http://localhost:42058","token":"flw_abcdefghijkl_secret"}`,
	}}

	got, err := adoptCredentials(context.Background(), d.run)
	if err != nil {
		t.Fatalf("adoptCredentials: %v", err)
	}
	if got.APIURL != "http://localhost:42058" || got.Token != "flw_abcdefghijkl_secret" {
		t.Errorf("adopted %+v, want the container's credentials", got)
	}

	// The EXACT command list. Adoption must inspect and read, and do nothing else — in particular
	// it must never start a stack, because every CLI command goes through this path.
	want := []string{inspectCmd, catCmd}
	if strings.Join(d.calls, " | ") != strings.Join(want, " | ") {
		t.Errorf("docker commands = %v, want exactly %v", d.calls, want)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("credentials not written to %s: %v", path, err)
	}
	if !strings.Contains(string(raw), "flw_abcdefghijkl_secret") {
		t.Errorf("credentials file does not carry the token: %s", raw)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600 — it holds an admin token", perm)
	}
}

// TestAdoptCredentialsRefusesWhatItCannotUse: every shape that must NOT end up saved as if it were
// valid. A half-written credentials file adopted as good sends the user to a 401 with no clue why.
func TestAdoptCredentialsRefusesWhatItCannotUse(t *testing.T) {
	cases := []struct {
		name    string
		running string
		body    string
		wantErr error
	}{
		{name: "no container at all", running: "", wantErr: errNoInstance},
		{name: "container exists but is stopped", running: "false\n", wantErr: errNoInstance},
		{name: "empty credentials file", running: "true\n", body: `{}`},
		{name: "token missing", running: "true\n", body: `{"api_url":"http://localhost:42058"}`},
		{name: "url missing", running: "true\n", body: `{"token":"flw_abcdefghijkl_secret"}`},
		{name: "not json at all", running: "true\n", body: "cat: no such file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := isolateConfigHome(t)
			d := &fakeDocker{answers: map[string]string{inspectCmd: tc.running, catCmd: tc.body}}
			if tc.running == "" {
				d.fails = map[string]error{inspectCmd: errors.New("No such object")}
			}

			_, err := adoptCredentials(context.Background(), d.run)
			if err == nil {
				t.Fatal("adoptCredentials accepted it, want a refusal")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Error("a credentials file was written from an input that was refused")
			}
		})
	}
}

// TestOfferToStartStackRespectsNo pins that declining really declines: no container is started.
func TestOfferToStartStackRespectsNo(t *testing.T) {
	d := &fakeDocker{answers: map[string]string{}}
	var out strings.Builder

	err := offerToStartStack(context.Background(), d.run, strings.NewReader("n\n"), &out)
	if err == nil {
		t.Fatal("offerToStartStack returned nil after a refusal")
	}
	if len(d.calls) != 0 {
		t.Errorf("docker commands = %v, want none after a refusal", d.calls)
	}
	if !strings.Contains(out.String(), "docker compose up -d") {
		t.Errorf("the refusal message does not say what to run by hand: %q", out.String())
	}
}

// TestOfferToStartStackAnswers covers what counts as yes. An empty line is yes: the question is
// only asked at a point where starting is what the user came for.
func TestOfferToStartStackAnswers(t *testing.T) {
	cases := []struct {
		answer    string
		wantStart bool
	}{
		{answer: "\n", wantStart: true},
		{answer: "y\n", wantStart: true},
		{answer: "YES\n", wantStart: true},
		{answer: "o\n", wantStart: true},
		{answer: "n\n", wantStart: false},
		{answer: "no\n", wantStart: false},
		{answer: "later\n", wantStart: false},
		{answer: "", wantStart: true}, // EOF on a closed stdin
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.answer), func(t *testing.T) {
			d := &fakeDocker{answers: map[string]string{}}
			var out strings.Builder

			err := offerToStartStack(context.Background(), d.run, strings.NewReader(tc.answer), &out)
			started := len(d.calls) == 1 && d.calls[0] == upCmd

			if started != tc.wantStart {
				t.Errorf("answer %q started the stack = %v (commands %v), want %v",
					tc.answer, started, d.calls, tc.wantStart)
			}
			if tc.wantStart && err != nil {
				t.Errorf("offerToStartStack: %v", err)
			}
		})
	}
}

// TestWaitForCredentialsGivesUpOnDeadline: a stack that never becomes ready must return the reason,
// not hang. The message has to carry the underlying failure, or the user is left with "not ready"
// and nothing to act on.
func TestWaitForCredentialsGivesUpOnDeadline(t *testing.T) {
	d := &fakeDocker{
		answers: map[string]string{inspectCmd: "true\n"},
		fails:   map[string]error{catCmd: errors.New("No such file or directory")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := waitForCredentials(ctx, d.run, time.Millisecond)
	if err == nil {
		t.Fatal("waitForCredentials returned nil on a stack that never became ready")
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("error = %v, want it to carry the underlying docker failure", err)
	}
}

// TestWaitForCredentialsReturnsAsSoonAsTheyExist: the poll stops at the first success, it does not
// keep asking until the deadline.
func TestWaitForCredentialsReturnsAsSoonAsTheyExist(t *testing.T) {
	isolateConfigHome(t)
	d := &fakeDocker{answers: map[string]string{
		inspectCmd: "true\n",
		catCmd:     `{"api_url":"http://localhost:42058","token":"flw_abcdefghijkl_secret"}`,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	got, err := waitForCredentials(ctx, d.run, time.Hour) // an interval far beyond any first retry
	if err != nil {
		t.Fatalf("waitForCredentials: %v", err)
	}
	if got.Token != "flw_abcdefghijkl_secret" {
		t.Errorf("token = %q, want the container's", got.Token)
	}
	if len(d.calls) != 2 {
		t.Errorf("docker commands = %v, want the two of a single successful attempt", d.calls)
	}
}

// TestIsInteractiveRejectsAPipe is what keeps an agent's session from hanging on a prompt: a CLI
// run with no terminal must never ask a question.
func TestIsInteractiveRejectsAPipe(t *testing.T) {
	if isInteractive(strings.NewReader("y\n")) {
		t.Error("isInteractive said yes to a plain reader")
	}

	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if isInteractive(f) {
		t.Error("isInteractive said yes to a regular file — a redirected stdin would be prompted")
	}

	// /dev/null is a CHARACTER DEVICE, so the mode check alone let it through. `flowlio init
	// < /dev/null` was therefore prompted, and askYesNo reads the immediate EOF as yes: a question
	// guarding an overwrite got consent from nobody.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer func() { _ = null.Close() }()
	if isInteractive(null) {
		t.Errorf("isInteractive said yes to %s — an EOF answer would count as consent", os.DevNull)
	}
}

// TestCredentialsRoundTripThroughAdoption pins the JSON contract between the two sides. The API
// writes this file with credentials.Save; the CLI reads it back out of the container with
// encoding/json. A renamed field would break adoption silently, and only in Docker.
func TestCredentialsRoundTripThroughAdoption(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := credentials.File{APIURL: "http://localhost:42058", Token: "flw_abcdefghijkl_secret"}
	path, err := credentials.Save(want)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	// Second temp home: adoption must produce the file, not find the one Save just wrote.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := &fakeDocker{answers: map[string]string{
		inspectCmd: "true\n",
		catCmd:     string(written),
	}}

	got, err := adoptCredentials(context.Background(), d.run)
	if err != nil {
		t.Fatalf("adoptCredentials on what the API wrote: %v", err)
	}
	if got != want {
		t.Errorf("adopted %+v, want %+v — the JSON field names have drifted apart", got, want)
	}
}
