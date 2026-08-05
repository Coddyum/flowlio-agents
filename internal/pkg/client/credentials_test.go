package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// recorder is an API that answers nothing and remembers who called it: which server received the
// request answers "which address won", and the bearer it carried answers "which token won".
type recorder struct {
	*httptest.Server
	hits   int
	bearer string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits++
		r.bearer = strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(r.Close)
	return r
}

// TestFromCredentialsFallsBackHalfByHalf is the guarantee that keeps an exported token from being
// silently swapped for the file's.
//
// The assertion is on the REQUEST that actually leaves: a client can carry the wrong identity and
// still look correct from the outside, then come back with a bare `forbidden` pointing at nothing.
// Reading the address and the bearer off the wire is the only place both are observable.
func TestFromCredentialsFallsBackHalfByHalf(t *testing.T) {
	const (
		fileToken = "flw_filefilefile_admin"
		envToken  = "flw_envenvenven_agent"
	)

	cases := []struct {
		name       string
		exportURL  bool
		exportTok  bool
		wantServer string // "env" or "file"
		wantToken  string
	}{
		{name: "nothing exported: the file answers for both", wantServer: "file", wantToken: fileToken},
		{name: "both exported: the environment answers for both", exportURL: true, exportTok: true, wantServer: "env", wantToken: envToken},
		{name: "only the token exported: it is KEPT, the file supplies the address", exportTok: true, wantServer: "file", wantToken: envToken},
		{name: "only the address exported: it is kept, the file supplies the token", exportURL: true, wantServer: "env", wantToken: fileToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileAPI, envAPI := newRecorder(t), newRecorder(t)

			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if _, err := credentials.Save(credentials.File{APIURL: fileAPI.URL, Token: fileToken}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			var url, token string
			if tc.exportURL {
				url = envAPI.URL
			}
			if tc.exportTok {
				token = envToken
			}

			c, err := client.FromCredentials(url, token)
			if err != nil {
				t.Fatalf("FromCredentials: %v", err)
			}
			if err := c.Do(context.Background(), http.MethodGet, "/whoami", nil, nil); err != nil {
				t.Fatalf("Do: %v", err)
			}

			got, other := fileAPI, envAPI
			if tc.wantServer == "env" {
				got, other = envAPI, fileAPI
			}
			if got.hits != 1 {
				t.Fatalf("the %s address received %d requests, want 1", tc.wantServer, got.hits)
			}
			if other.hits != 0 {
				t.Errorf("the other address received %d requests, want none", other.hits)
			}
			if got.bearer != tc.wantToken {
				t.Errorf("token sent = %q, want %q", got.bearer, tc.wantToken)
			}
		})
	}
}

// TestFromCredentialsNamesTheMissingHalf: a token exported with no address and no file must say
// which half is missing, not report a file the user never created.
func TestFromCredentialsNamesTheMissingHalf(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := client.FromCredentials("", "flw_envenvenven_agent")
	if err == nil {
		t.Fatal("FromCredentials accepted a token with no address")
	}
	if !errors.Is(err, credentials.ErrNotFound) {
		t.Errorf("error = %v, want it to wrap credentials.ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "FLOWLIO_API_URL") {
		t.Errorf("error = %q, want it to name FLOWLIO_API_URL as the missing half", err)
	}
}
