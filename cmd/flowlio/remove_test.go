package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
	"github.com/google/uuid"
)

// THE SERVER'S REFUSAL IS RELAYED WORD FOR WORD. Its 409 names every sibling still holding a thread
// with this repo, how many each holds, and what to do instead. Rewording it here would drop the
// counts and the advice, and would drift from the sentence the hosted product shows for the same
// refusal.
//
// MUTATION: replace the message with one of our own — this goes red.
func TestRemoveRelaysTheRefusalVerbatim(t *testing.T) {
	const refusal = "repo API still has open threads with WEB (3) and CORE (1); revoke its tokens " +
		"and deny its trust edges instead"
	id := uuid.New()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == workspaceAPI+"/projects":
			writeJSON(t, w, http.StatusOK, []service.Project{{ID: id, Key: "API", Name: "acme-api"}})
		case r.Method == http.MethodDelete && r.URL.Path == workspaceAPI+"/projects/"+id.String():
			writeJSON(t, w, http.StatusConflict, map[string]string{"error": refusal})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	var out strings.Builder
	err := removeRepo(context.Background(), &out, client.New(ts.URL, "flw_admin"), "acme", "API")
	if err == nil {
		t.Fatal("a refused deletion exited clean")
	}

	if !strings.Contains(out.String(), refusal) {
		t.Errorf("the server's sentence was not relayed:\n%s", out.String())
	}
	// The status is what a script reads, and a refusal is not a success.
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Errorf("exit status = %v, expected 1", err)
	}
}

// The whole value of the command: `project list` prints keys, the deletion route takes an id, and
// resolving one to the other is what saves a human reading a UUID out of a JSON response.
func TestRemoveResolvesTheKeyToAnIdentifier(t *testing.T) {
	id := uuid.New()
	deleted := ""

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == workspaceAPI+"/projects":
			if got := r.URL.Query().Get("team"); got != "acme" {
				t.Errorf("listed the repos of team %q, expected acme", got)
			}
			writeJSON(t, w, http.StatusOK, []service.Project{
				{ID: uuid.New(), Key: "WEB", Name: "acme-web"},
				{ID: id, Key: "API", Name: "acme-api"},
			})
		case r.Method == http.MethodDelete:
			deleted = strings.TrimPrefix(r.URL.Path, workspaceAPI+"/projects/")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	defer ts.Close()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out strings.Builder
	if err := removeRepo(context.Background(), &out, client.New(ts.URL, "flw_admin"), "acme", "API"); err != nil {
		t.Fatalf("removeRepo: %v", err)
	}
	if deleted != id.String() {
		t.Errorf("deleted %q, expected the id of API (%s)", deleted, id)
	}

	// A repository deleted server-side leaves nothing behind that this command can reach, and it has
	// to say so: `remove` never touches the repository's own files.
	if !strings.Contains(out.String(), "flowlio disconnect") {
		t.Errorf("the output does not say the repository's own files are untouched:\n%s", out.String())
	}
}

// A key the project does not hold is named as such, rather than being sent to the deletion route as
// something that cannot be an identifier.
func TestRemoveNamesAKeyThatDoesNotExist(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			t.Error("a deletion was attempted for a key that does not exist")
		}
		writeJSON(t, w, http.StatusOK, []service.Project{{ID: uuid.New(), Key: "WEB"}})
	}))
	defer ts.Close()

	var out strings.Builder
	err := removeRepo(context.Background(), &out, client.New(ts.URL, "flw_admin"), "acme", "API")
	if err == nil || !strings.Contains(err.Error(), "API") {
		t.Errorf("error = %v, expected one naming API", err)
	}
}

// writeJSON answers with a status and a body, the way the API does.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encoding the response: %v", err)
	}
}
