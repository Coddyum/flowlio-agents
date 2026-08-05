package main

// What this file locks down: what the MCP layer and the CLI **send**, not what they yield.
//
// Until now, no test in the repo read the body of an outgoing request — the API double of
// mcp_test.go answered the same payload while ignoring the request (`func(w, _ *http.Request)`).
// The whole MCP surface was therefore checked on its return, never on what it sent: a tool could
// omit a field silently, and three mutations proved it by staying green across the whole suite,
// `golangci-lint` included.
//
// The double now records method, path and body. `newAPIServer` goes through it: there is no longer,
// in this package, a way to mount a fake API that ignores what it is told.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// recordedRequest is what a caller actually put on the wire.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// apiRecorder collects the requests received by the API double.
//
// The mutex is not decorative: httptest serves every request in its own goroutine, and the race
// detector fails the suite without it.
type apiRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *apiRecorder) record(req recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

// all yields a copy of the requests received, in arrival order.
func (r *apiRecorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

// only requires that exactly one request was emitted, and yields it. The count is part of the
// assertion: a tool sending two requests where one suffices is a round trip paid on every turn.
func (r *apiRecorder) only(t *testing.T) recordedRequest {
	t.Helper()

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("%d requests emitted, expected 1: %+v", len(got), got)
	}
	return got[0]
}

// fields decodes the JSON body of a request into a map, to assert field by field.
func (req recordedRequest) fields(t *testing.T) map[string]any {
	t.Helper()

	if req.Body == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(req.Body), &out); err != nil {
		t.Fatalf("unreadable body %q: %v", req.Body, err)
	}
	return out
}

// newRecordingAPI mounts a fake API that always answers the same payload AND records what it
// receives.
func newRecordingAPI(t *testing.T, reply string) (*client.Client, *apiRecorder) {
	t.Helper()

	rec := &apiRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the received body: %v", err)
		}
		rec.record(recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Body:   string(body),
		})

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(ts.Close)

	return client.New(ts.URL, "flw_test"), rec
}

// newRecordingServer mounts an MCP server plugged into the recording API.
func newRecordingServer(t *testing.T, reply string) (*mcpServer, *apiRecorder) {
	t.Helper()

	api, rec := newRecordingAPI(t, reply)
	return &mcpServer{
		out:        &bytes.Buffer{},
		api:        api,
		projectKey: "CORE",
		teamSlug:   "omiros",
	}, rec
}

// taskAPIReply is the payload the fake API yields for a task: the return is not the subject of
// this file, it only has to be decodable.
const taskAPIReply = `{"number":34,"title":"x","status":"todo","priority":"normal",` +
	`"created_at":"2026-08-02T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}`

// update_task sends the note INSIDE the patch — that is the whole guarantee of FLWL-15: "status
// changed, reason lost" is not a reachable state because the two travel together.
//
// MUTATION: removing `Note: in.Note,` from the updateTask payload makes this test fall over. Before
// it, that mutation crossed the whole suite green: the tool threw the note away and yielded the
// task all the same, so nothing saw it.
func TestUpdateTaskSendsTheNoteInsideThePatch(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","status":"done","note":"shipped"}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("request = %s %s, expected PATCH /api/task/34", req.Method, req.Path)
	}

	fields := req.fields(t)
	if fields["note"] != "shipped" {
		t.Errorf("note sent = %v, expected \"shipped\" — the note does not reach the API", fields["note"])
	}
	if fields["status"] != "done" {
		t.Errorf("status sent = %v, expected \"done\"", fields["status"])
	}
}

// A note ALONE is a valid call: it is the direct replacement of add_task_note, removed by FLWL-15,
// and the least tested path of that commit.
//
// MUTATION: removing `|| in.Note != nil` from the guard makes this test fall over — the tool then
// yields "no change requested" and emits NO request at all, on a call the agent believes
// succeeded.
func TestUpdateTaskWithOnlyANoteReachesTheAPI(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","note":"making progress"}`)); err != nil {
		t.Fatalf("update_task with the note alone: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("request = %s %s, expected PATCH /api/task/34", req.Method, req.Path)
	}
	if got := req.fields(t)["note"]; got != "making progress" {
		t.Errorf("note sent = %v, expected \"making progress\"", got)
	}
}

// An empty call writes nothing: the guard must cut BEFORE the network, not after.
//
// The case of a deadline made of BLANKS is the same empty call, written differently. It got past
// the guard (`in.Deadline != ""` is true for three spaces) only to be ignored right after by
// parseDeadline (`TrimSpace(...) == ""`): the PATCH left with ALL fields nil, the API yielded the
// task unchanged, and the agent read a SUCCESS believing it had set a deadline. The same call
// without `deadline` yielded "no change requested".
//
// MUTATION: restoring `in.Deadline != ""` in the updateTask guard makes this test fall over.
func TestUpdateTaskWithNothingToChangeSendsNoRequest(t *testing.T) {
	cases := map[string]string{
		"no field":           `{"ref":"CORE-34"}`,
		"empty deadline":     `{"ref":"CORE-34","deadline":""}`,
		"deadline of blanks": `{"ref":"CORE-34","deadline":"   "}`,
		"deadline of tabs":   `{"ref":"CORE-34","deadline":"\t\n"}`,
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			srv, rec := newRecordingServer(t, taskAPIReply)

			_, err := srv.updateTask(context.Background(), json.RawMessage(args))
			if err == nil {
				t.Fatal("a call requesting no change must be an error, not a success")
			}
			if err.Error() != "no change requested" {
				t.Errorf("message = %q, expected \"no change requested\"", err)
			}
			if got := rec.all(); len(got) != 0 {
				t.Errorf("%d requests emitted: %+v", len(got), got)
			}
		})
	}
}

// A deadline that is actually set does leave — the guard must not become too broad a filter.
func TestUpdateTaskSendsARealDeadline(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","deadline":"2027-01-02T03:04:05Z"}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}
	if got := rec.only(t).fields(t)["deadline"]; got != "2027-01-02T03:04:05Z" {
		t.Errorf("deadline sent = %v, expected 2027-01-02T03:04:05Z", got)
	}
}

// The end of a task's life fits in ONE request: status, note and archiving together.
//
// This test first froze the opposite — two requests, a PATCH then a POST /archive — because that
// was the state of the code. It fell over when archiving was folded in (FLWL-24), and that is
// exactly what was asked of it: making the change visible instead of silent.
//
// What the second request cost: a failure between the two committed the note without archiving,
// the agent read `api: internal error` and replayed — which wrote the note a SECOND time.
//
// MUTATION: pulling archiving back out as `POST .../archive` makes this test fall over on the
// count.
func TestUpdateTaskArchivesInTheSameRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","status":"done","note":"shipped","archive":true}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("request = %s %s, expected PATCH /api/task/34", req.Method, req.Path)
	}

	fields := req.fields(t)
	for key, want := range map[string]any{"status": "done", "note": "shipped", "archive": true} {
		if fields[key] != want {
			t.Errorf("%s sent = %v, expected %v: the three must travel together",
				key, fields[key], want)
		}
	}
}

// Archiving ALONE is a valid call, and stays one single request.
func TestUpdateTaskArchiveOnlyIsOneRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","archive":true}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("request = %s %s, expected PATCH /api/task/34", req.Method, req.Path)
	}
	if got := req.fields(t)["archive"]; got != true {
		t.Errorf("archive sent = %v, expected true", got)
	}
}

// The `flowlio task archive` CLI takes the same single path as the agent.
//
// MUTATION: sending it back to `POST /api/task/34/archive` makes this test fall over on the
// method, the path and the body.
func TestTaskArchiveCLIPatchesTheTask(t *testing.T) {
	api, rec := newRecordingAPI(t, taskAPIReply)

	if err := taskArchive(context.Background(), api, []string{"CORE-34"}); err != nil {
		t.Fatalf("task archive: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch {
		t.Errorf("method = %s, expected PATCH", req.Method)
	}
	if req.Path != "/api/task/34" {
		t.Errorf("path = %s, expected /api/task/34", req.Path)
	}
	if got := req.fields(t)["archive"]; got != true {
		t.Errorf("archive sent = %v, expected true", got)
	}
}

// The `flowlio task note` CLI takes the SAME write path as the agent: a PATCH carrying the note
// alone. FLWL-15 rewrote it towards another route and another method without a single test.
//
// MUTATION: sending it back to `POST /api/task/34/notes` with an empty body — its previous state —
// makes this test fall over on the method, the path and the body at once.
func TestTaskNoteCLIPatchesTheTaskWithTheNote(t *testing.T) {
	api, rec := newRecordingAPI(t, taskAPIReply)

	if err := taskNote(context.Background(), api, []string{"CORE-34", "text", "over", "several", "words"}); err != nil {
		t.Fatalf("task note: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch {
		t.Errorf("method = %s, expected PATCH", req.Method)
	}
	if req.Path != "/api/task/34" {
		t.Errorf("path = %s, expected /api/task/34", req.Path)
	}

	fields := req.fields(t)
	if fields["note"] != "text over several words" {
		t.Errorf("note sent = %v, expected the whole sentence — the following words are lost", fields["note"])
	}
	if fields["title"] != nil || fields["status"] != nil || fields["priority"] != nil {
		t.Errorf("the CLI patches something other than the note: %v", fields)
	}
}

// create_task does send what the agent wrote, optional fields included.
//
// Same class of flaw as the note: it is a write tool of which only the RETURN was checked.
func TestCreateTaskSendsEveryFieldItAccepts(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.createTask(context.Background(), json.RawMessage(
		`{"title":"t","body":"b","status":"in_progress","priority":"urgent","deadline":"2027-01-02T03:04:05Z"}`,
	)); err != nil {
		t.Fatalf("create_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPost || req.Path != "/api/task/" {
		t.Fatalf("request = %s %s, expected POST /api/task/", req.Method, req.Path)
	}

	fields := req.fields(t)
	for key, want := range map[string]any{
		"title":    "t",
		"body":     "b",
		"status":   "in_progress",
		"priority": "urgent",
		"deadline": "2027-01-02T03:04:05Z",
	} {
		if fields[key] != want {
			t.Errorf("%s sent = %v, expected %v", key, fields[key], want)
		}
	}
}

// refIssueReply and refTaskReply are the two shapes /api/ref yields, depending on what the
// reference designates. Hard-coded: this file checks what the CLI SENDS, the return only has to be
// decodable.
const refIssueReply = `{"kind":"issue","ref":"CORE-12","issue":{"ref":"CORE-12",` +
	`"title":"login outage","state":"open","role":"incoming","peer":"FRNT",` +
	`"updated_at":"2026-08-02T10:00:00Z","messages":[],"messages_total":0}}`

const refTaskReply = `{"kind":"task","ref":"CORE-34","task":{"number":34,"title":"x",` +
	`"status":"todo","priority":"normal","created_at":"2026-08-02T10:00:00Z",` +
	`"updated_at":"2026-08-02T10:00:00Z","notes":[],"notes_total":0}}`

// THE COUNT IS THE ASSERTION — it is the very statement of FLWL-16.
//
// get(ref) on an issue made TWO round trips: the task route, its 404, then the issue route. The
// cost fell exactly on INCOMING issues — the ones whose key is mine, that is to say precisely what
// check_inbox just handed the agent, hence the most-called read path of the product. The choice
// between the two is now made INSIDE the API.
//
// MUTATION: restore in get() the attempt on /api/task then the switch to /api/issue. The return
// would be identical field for field, and rec.only would go red on the count, alone.
func TestGetIssueMakesExactlyOneRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, refIssueReply)

	value, err := srv.get(context.Background(), json.RawMessage(`{"ref":"CORE-12"}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := value.(getIssueResult); !ok {
		t.Fatalf("get yields a %T, expected getIssueResult", value)
	}

	req := rec.only(t)
	if req.Method != http.MethodGet || req.Path != "/api/ref/CORE/12" {
		t.Fatalf("request = %s %s, expected GET /api/ref/CORE/12", req.Method, req.Path)
	}
}

// The same count on a task. The task branch NEVER cost two calls — this test exists so that it
// does not start to, now that both go through the same route.
func TestGetTaskMakesExactlyOneRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, refTaskReply)

	value, err := srv.get(context.Background(), json.RawMessage(`{"ref":"CORE-34"}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := value.(getTaskResult); !ok {
		t.Fatalf("get yields a %T, expected getTaskResult", value)
	}

	req := rec.only(t)
	if req.Path != "/api/ref/CORE/34" {
		t.Fatalf("path = %s, expected /api/ref/CORE/34", req.Path)
	}
}

// A BARE number leaves with the caller's key, never bare: it is that key the API compares to the
// token's project to decide whether a task is even conceivable.
//
// MUTATION: compose the path without the key (`/api/ref/34`). The route would no longer match, and
// the reference of an agent that simply writes "34" would become impossible to find.
func TestGetSendsTheCallerKeyForABareNumber(t *testing.T) {
	srv, rec := newRecordingServer(t, refTaskReply)

	if _, err := srv.get(context.Background(), json.RawMessage(`{"ref":"34"}`)); err != nil {
		t.Fatalf("get: %v", err)
	}

	if req := rec.only(t); req.Path != "/api/ref/CORE/34" {
		t.Fatalf("path = %s, expected /api/ref/CORE/34 — the caller's key must be substituted "+
			"before sending", req.Path)
	}
}
