package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

const (
	// liveInstanceURL is where the running instance answers in these tests — the port the compose
	// file settled on, which is precisely the one the stale files on real machines do NOT carry.
	liveInstanceURL   = "http://localhost:42058"
	liveInstanceToken = "flw_liveliveliv_admin"
	staleToken        = "flw_stalestalest_admin"
)

// deadAddress returns an address nothing listens on: a real server is started so the port is one the
// operating system handed out, then closed. Hardcoding a port risks hitting something.
func deadAddress(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()
	return url
}

// staleCredentials reproduces the situation this file exists for: a perfectly READABLE credentials
// file whose address answers nothing. Returns the file's path, that address, and the transport
// failure a first request produces.
func staleCredentials(t *testing.T) (string, string, *client.TransportError) {
	t.Helper()

	deadURL := deadAddress(t)
	path := isolateConfigHome(t)
	if _, err := credentials.Save(credentials.File{APIURL: deadURL, Token: staleToken}); err != nil {
		t.Fatalf("writing the stale credentials: %v", err)
	}

	c, err := client.FromCredentials("", "")
	if err != nil {
		t.Fatalf("FromCredentials on a readable file: %v", err)
	}
	dead := unreachableAPI(context.Background(), c)
	if dead == nil {
		t.Fatalf("something answered at %s, which was closed on purpose", deadURL)
	}
	return path, deadURL, dead
}

// liveDocker answers as a running instance whose own credentials point at apiURL.
func liveDocker(apiURL string) *fakeDocker {
	return &fakeDocker{answers: map[string]string{
		inspectCmd: "true\n",
		catCmd:     fmt.Sprintf(`{"api_url":%q,"token":%q}`, apiURL, liveInstanceToken),
	}}
}

// TestUnreachableAPINamesTheCredentialsFile is the defect this card is about: the old message spoke
// of `dial tcp` and a port, and never said the address came from a file — let alone which one.
func TestUnreachableAPINamesTheCredentialsFile(t *testing.T) {
	path, deadURL, dead := staleCredentials(t)

	if !strings.Contains(dead.Error(), path) {
		t.Errorf("error = %q, want it to name %s: an address printed alone sends the reader hunting "+
			"for a setting they never knowingly made", dead, path)
	}
	if !strings.Contains(dead.Error(), deadURL) {
		t.Errorf("error = %q, want it to name the address %s", dead, deadURL)
	}
}

// TestUnreachableAPIAcceptsARefusalAsAnAnswer: the probe is about the ADDRESS. A 403 proves an API
// is listening, and treating it as unreachable would send init looking for another instance every
// time someone ran it with a project token.
func TestUnreachableAPIAcceptsARefusalAsAnAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	if got := unreachableAPI(context.Background(), client.New(ts.URL, "flw_test_secret")); got != nil {
		t.Errorf("unreachableAPI = %v on a server that answered 403", got)
	}
}

// TestRepointAtInstanceOnlyNamesTheWayOutWhenNobodyCanAnswer is the agent's case: no terminal, so
// nothing may be overwritten. The error has to carry both halves — the dead file and the live
// address — or the reader is told there is a problem and not what to do.
func TestRepointAtInstanceOnlyNamesTheWayOutWhenNobodyCanAnswer(t *testing.T) {
	path, deadURL, dead := staleCredentials(t)
	d := liveDocker(liveInstanceURL)
	var out strings.Builder

	got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader("y\n"), &out, false)
	if got != nil {
		t.Error("repointAtInstance repointed with no human to consent")
	}
	if err == nil {
		t.Fatal("repointAtInstance returned nil on an address that answers nothing")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name the file %s", err, path)
	}
	if !strings.Contains(err.Error(), liveInstanceURL) {
		t.Errorf("error = %q, want it to name the instance answering at %s", err, liveInstanceURL)
	}
	if out.String() != "" {
		t.Errorf("printed %q on a path where nobody is reading — the error carries everything", out.String())
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back %s: %v", path, readErr)
	}
	if !strings.Contains(string(raw), deadURL) || strings.Contains(string(raw), liveInstanceToken) {
		t.Errorf("the credentials file was rewritten without consent: %s", raw)
	}
}

// TestRepointAtInstanceFollowsTheInstanceOnYes: the repair the card asks for, and it only happens
// once a human has said so.
func TestRepointAtInstanceFollowsTheInstanceOnYes(t *testing.T) {
	path, deadURL, dead := staleCredentials(t)
	d := liveDocker(liveInstanceURL)
	var out strings.Builder

	got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader("y\n"), &out, true)
	if err != nil {
		t.Fatalf("repointAtInstance: %v", err)
	}
	if got.BaseURL() != liveInstanceURL {
		t.Errorf("client points at %s, want %s", got.BaseURL(), liveInstanceURL)
	}
	// The question is asked AFTER the reason is shown: a prompt with no explanation gets answered
	// without being read.
	if !strings.Contains(out.String(), deadURL) {
		t.Errorf("output %q does not say why the question is being asked", out.String())
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back %s: %v", path, readErr)
	}
	if !strings.Contains(string(raw), liveInstanceToken) {
		t.Errorf("%s still does not carry the instance's credentials: %s", path, raw)
	}
}

// TestRepointAtInstanceLeavesTheFileAloneOnNo: a no is a no. The file may be pointing elsewhere on
// purpose, and the original failure is what the caller must still see.
func TestRepointAtInstanceLeavesTheFileAloneOnNo(t *testing.T) {
	path, deadURL, dead := staleCredentials(t)
	d := liveDocker(liveInstanceURL)
	var out strings.Builder

	got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader("n\n"), &out, true)
	if got != nil {
		t.Error("repointAtInstance repointed after a refusal")
	}
	if !errors.Is(err, dead) {
		t.Errorf("error = %v, want the transport failure that started this", err)
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back %s: %v", path, readErr)
	}
	if !strings.Contains(string(raw), deadURL) {
		t.Errorf("%s was rewritten after a refusal: %s", path, raw)
	}
}

// TestRepointAtInstanceTakesSilenceForARefusal is the trap this path fell into on a real terminal:
// stdin ended before an answer arrived, an empty line counted as yes, and the credentials file was
// overwritten with nobody having said so. A bare Enter or a Ctrl-D must leave the file alone.
func TestRepointAtInstanceTakesSilenceForARefusal(t *testing.T) {
	for _, answer := range []string{"", "\n"} {
		t.Run(fmt.Sprintf("%q", answer), func(t *testing.T) {
			path, deadURL, dead := staleCredentials(t)
			d := liveDocker(liveInstanceURL)
			var out strings.Builder

			got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader(answer), &out, true)
			if got != nil {
				t.Error("repointAtInstance repointed on an answer nobody gave")
			}
			if !errors.Is(err, dead) {
				t.Errorf("error = %v, want the transport failure unchanged", err)
			}
			if !strings.Contains(out.String(), "[y/N]") {
				t.Errorf("prompt %q does not show that the default is no", out.String())
			}

			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read back %s: %v", path, readErr)
			}
			if !strings.Contains(string(raw), deadURL) {
				t.Errorf("%s was overwritten without consent: %s", path, raw)
			}
		})
	}
}

// TestRepointAtInstanceDoesNotOfferTheDeadAddressBack: when the instance believes in the very
// address that went unanswered, there is no repair to offer. Asking anyway would promise a fix that
// changes nothing.
func TestRepointAtInstanceDoesNotOfferTheDeadAddressBack(t *testing.T) {
	_, deadURL, dead := staleCredentials(t)
	d := liveDocker(deadURL)
	var out strings.Builder

	got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader("y\n"), &out, true)
	if got != nil {
		t.Error("repointAtInstance repointed at the address that answers nothing")
	}
	if !errors.Is(err, dead) {
		t.Errorf("error = %v, want the transport failure unchanged", err)
	}
	if out.String() != "" {
		t.Errorf("asked something %q when there was nothing to offer", out.String())
	}
}

// TestRepointAtInstanceStaysQuietWithoutDocker: no daemon, no container, no repair — and no second
// error burying the first. The transport failure is the useful one.
func TestRepointAtInstanceStaysQuietWithoutDocker(t *testing.T) {
	_, _, dead := staleCredentials(t)
	d := &fakeDocker{fails: map[string]error{inspectCmd: errors.New("Cannot connect to the Docker daemon")}}
	var out strings.Builder

	got, err := repointAtInstance(context.Background(), dead, d.run, strings.NewReader("y\n"), &out, true)
	if got != nil {
		t.Error("repointAtInstance repointed with no instance to follow")
	}
	if !errors.Is(err, dead) {
		t.Errorf("error = %v, want the transport failure rather than a docker diagnostic", err)
	}
}
