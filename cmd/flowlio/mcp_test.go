package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// newTestServer builds an MCP server with no API client: the protocol methods (initialize,
// tools/list, ping) do not need one, and these tests must run without any infrastructure.
func newTestServer(out *bytes.Buffer) *mcpServer {
	return &mcpServer{out: out, projectKey: "CORE", teamSlug: "omiros"}
}

// exchange pushes messages through the server loop and returns the decoded responses.
func exchange(t *testing.T, messages ...string) []rpcResponse {
	t.Helper()

	var out bytes.Buffer
	srv := newTestServer(&out)
	if err := srv.serve(context.Background(), strings.NewReader(strings.Join(messages, "\n")+"\n")); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var responses []rpcResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unreadable response %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestInitializeHandshake(t *testing.T) {
	responses := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(responses) != 1 {
		t.Fatalf("%d responses, expected 1", len(responses))
	}

	result, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("result of type %T, expected an object", responses[0].Result)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, expected %s", result["protocolVersion"], protocolVersion)
	}

	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities missing from the initialize response")
	}
	if _, declared := capabilities["tools"]; !declared {
		t.Error("the server must advertise the tools capability")
	}
	// The server serves neither resources nor prompts: advertising them would lie to the client.
	for _, absent := range []string{"resources", "prompts", "sampling"} {
		if _, found := capabilities[absent]; found {
			t.Errorf("capability %q advertised although it is not served", absent)
		}
	}
}

// A notification has no ID: answering one is a JSON-RPC 2.0 violation that some MCP clients
// treat as a session error.
func TestNotificationGetsNoResponse(t *testing.T) {
	responses := exchange(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
	)
	if len(responses) != 1 {
		t.Fatalf("%d responses, expected 1 (the notification must produce none)", len(responses))
	}
	if string(responses[0].ID) != "1" {
		t.Errorf("the only response must be the ping's, ID = %s", responses[0].ID)
	}
}

// An unreadable line must not close the session: an agent would lose its whole working context
// over one stray line.
func TestMalformedMessageDoesNotKillSession(t *testing.T) {
	responses := exchange(t,
		`this is not JSON`,
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`,
	)
	if len(responses) != 2 {
		t.Fatalf("%d responses, expected 2", len(responses))
	}
	if responses[0].Error == nil || responses[0].Error.Code != codeParseError {
		t.Errorf("first response = %+v, expected a parse error", responses[0].Error)
	}
	if responses[1].Error != nil {
		t.Errorf("the session must go on after an unreadable line: %+v", responses[1].Error)
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected int
	}{
		{
			"unknown method",
			`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
			codeMethodNotFound,
		},
		{
			"wrong protocol version",
			`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
			codeInvalidRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responses := exchange(t, tc.message)
			if len(responses) != 1 {
				t.Fatalf("%d responses, expected 1", len(responses))
			}
			if responses[0].Error == nil || responses[0].Error.Code != tc.expected {
				t.Errorf("error = %+v, expected code %d", responses[0].Error, tc.expected)
			}
		})
	}
}

// The MCP surface is a budget: every tool is re-injected into the agent's context on every turn.
// This test fails if somebody adds one without thinking, and the order is checked because it is
// the one in which an agent discovers the product.
func TestToolSurfaceIsSmallAndWellFormed(t *testing.T) {
	expected := []string{
		"list_tasks", "get", "create_task", "update_task", "block_task", "unblock_task",
		"create_issue", "list_issues", "answer_issue", "check_inbox",
	}

	defs := tools()
	if len(defs) != len(expected) {
		t.Fatalf("%d tools exposed, expected %d — the MCP surface is a budget, not a wish list",
			len(defs), len(expected))
	}

	seen := make(map[string]bool, len(defs))
	for i, def := range defs {
		if def.Name != expected[i] {
			t.Errorf("tool %d = %q, expected %q", i, def.Name, expected[i])
		}
		if seen[def.Name] {
			t.Errorf("tool %q declared twice", def.Name)
		}
		seen[def.Name] = true

		if def.Description == "" {
			t.Errorf("tool %q has no description: an agent would have to guess what it does", def.Name)
		}
		if def.InputSchema["type"] != "object" {
			t.Errorf("tool %q: schema of type %v, expected object", def.Name, def.InputSchema["type"])
		}
		if _, ok := def.InputSchema["properties"].(map[string]any); !ok {
			t.Errorf("tool %q: properties missing from the schema", def.Name)
		}

		// No tool must accept a project as a parameter: the project comes from the token, and a
		// parameter would be a surface where the scope could be bypassed.
		properties, _ := def.InputSchema["properties"].(map[string]any)
		// `to_project` is the only project parameter tolerated: it designates the RECIPIENT of a
		// question, not a read scope. No tool can choose the project it READS.
		forbidden := []string{"project", "project_id", "team", "team_id"}
		if def.Name == "create_issue" {
			forbidden = []string{"project", "project_id", "team", "team_id", "from_project"}
		}
		for _, forbidden := range forbidden {
			if _, found := properties[forbidden]; found {
				t.Errorf("tool %q accepts %q as a parameter: the scope comes from the token, never from the call",
					def.Name, forbidden)
			}
		}
	}
}

// add_task_note was folded into update_task: this test keeps that removal. The `note` field
// carries the capability, without the schema of one more tool paid for on every turn.
func TestNoteIsAFieldOfUpdateTaskNotATool(t *testing.T) {
	for _, def := range tools() {
		if def.Name == "add_task_note" {
			t.Fatal("add_task_note is back: the note is a field of update_task")
		}
	}

	for _, def := range tools() {
		if def.Name != "update_task" {
			continue
		}
		properties, _ := def.InputSchema["properties"].(map[string]any)
		note, found := properties["note"]
		if !found {
			t.Fatal("update_task does not accept a note: the capability was lost, not folded in")
		}
		if schema, _ := note.(map[string]any); schema["type"] != "string" {
			t.Errorf("note of type %v, expected string", schema["type"])
		}
		return
	}
	t.Fatal("update_task missing from the surface")
}

// newAPIServer mounts a fake API always answering the same payload, and an MCP server that talks
// to it. No database: what is checked here is the shape of the return, not persistence.
//
// The double goes through newRecordingServer and throws the recorder away. That detour is not
// free: the previous version ignored the request received (`func(w, _ *http.Request)`), and that
// line is what let three mutations cross the whole suite green — a tool could omit a field without
// anything seeing it. There is no longer, in this package, a way to mount a fake API deaf to what
// is sent to it; the assertions on what is sent live in mcp_request_test.go.
func newAPIServer(t *testing.T, reply string) *mcpServer {
	t.Helper()

	srv, _ := newRecordingServer(t, reply)
	return srv
}

// Every write yields {ref, <object>} and nothing else.
//
// Before, an agent had to guess where to read the reference depending on the tool: under "key" for
// a task, inside the object for an issue. One single shape saves it from getting it wrong, and the
// cost of a rename grows with the number of callers — hence this test, which freezes the shape.
func TestWriteToolsShareOneReturnShape(t *testing.T) {
	const taskReply = `{"number":34,"title":"x","status":"todo","priority":"normal",` +
		`"created_at":"2026-08-02T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}`
	const issueReply = `{"ref":"FRNT-12","title":"x","state":"open","role":"outgoing",` +
		`"peer":"FRNT","updated_at":"2026-08-02T10:00:00Z"}`

	tests := []struct {
		name    string
		reply   string
		call    func(*mcpServer) (any, error)
		wantRef string
		wantKey string
	}{
		{
			"create_task", taskReply,
			func(s *mcpServer) (any, error) {
				return s.createTask(context.Background(), json.RawMessage(`{"title":"x"}`))
			},
			"CORE-34", "task",
		},
		{
			"update_task", taskReply,
			func(s *mcpServer) (any, error) {
				return s.updateTask(context.Background(),
					json.RawMessage(`{"ref":"CORE-34","status":"done","note":"done"}`))
			},
			"CORE-34", "task",
		},
		{
			"create_issue", issueReply,
			func(s *mcpServer) (any, error) {
				return s.createIssue(context.Background(),
					json.RawMessage(`{"to_project":"FRNT","title":"x","body":"y"}`))
			},
			"FRNT-12", "issue",
		},
		{
			"answer_issue", issueReply,
			func(s *mcpServer) (any, error) {
				return s.answerIssue(context.Background(),
					json.RawMessage(`{"ref":"FRNT-12","body":"y"}`))
			},
			"FRNT-12", "issue",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value, err := tc.call(newAPIServer(t, tc.reply))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			result, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%s yields a %T, expected the {ref, object} envelope", tc.name, value)
			}
			if result["ref"] != tc.wantRef {
				t.Errorf("ref = %v, expected %s", result["ref"], tc.wantRef)
			}
			if _, found := result[tc.wantKey]; !found {
				t.Errorf("object missing under key %q: %+v", tc.wantKey, result)
			}
			if len(result) != 2 {
				t.Errorf("%d fields in the envelope, expected exactly 2 (ref + %s): %+v",
					len(result), tc.wantKey, result)
			}
		})
	}
}

// The task listing carries the same envelope as the writes: the reference is always read under
// "ref", never under another name depending on the tool.
func TestListTasksCarriesTheSameEnvelope(t *testing.T) {
	srv := newAPIServer(t, `[{"number":7,"title":"x","status":"todo","priority":"normal",`+
		`"created_at":"2026-08-02T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}]`)

	value, err := srv.listTasks(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("listTasks: %v", err)
	}

	rows, ok := value.([]map[string]any)
	if !ok {
		t.Fatalf("listTasks yields a %T, expected a list of envelopes", value)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows, expected 1", len(rows))
	}
	if rows[0]["ref"] != "CORE-7" {
		t.Errorf("ref = %v, expected CORE-7", rows[0]["ref"])
	}
	if _, legacy := rows[0]["key"]; legacy {
		t.Error(`the row still carries "key": the reference is read under "ref"`)
	}
}

// The advertised schema must be valid JSON, otherwise the MCP client rejects the whole session.
func TestToolsListIsSerializable(t *testing.T) {
	responses := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("tools/list failed: %+v", responses)
	}

	result, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("result of type %T", responses[0].Result)
	}
	listed, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools of type %T, expected an array", result["tools"])
	}
	if len(listed) != len(tools()) {
		t.Errorf("%d tools serialised, expected %d", len(listed), len(tools()))
	}
}

func TestNumberFromKey(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})

	valid := map[string]int64{
		"CORE-34": 34,
		"core-34": 34,
		"Core-1":  1,
		"34":      34,
		"  7  ":   7,
	}
	for key, expected := range valid {
		t.Run("valid "+key, func(t *testing.T) {
			number, err := srv.numberFromKey(key)
			if err != nil {
				t.Fatalf("numberFromKey(%q): %v", key, err)
			}
			if number != expected {
				t.Errorf("numberFromKey(%q) = %d, expected %d", key, number, expected)
			}
		})
	}

	invalid := []string{"", "   ", "CORE-", "CORE-abc", "-12", "0", "CORE-0", "34.5", "3 4"}
	for _, key := range invalid {
		t.Run("invalid "+key, func(t *testing.T) {
			if _, err := srv.numberFromKey(key); err == nil {
				t.Errorf("numberFromKey(%q) accepted, expected an error", key)
			}
		})
	}
}

// Another project's key must be refused WITH an explanation: an agent receiving a 404 would
// conclude the task does not exist, when it does and there is simply no access to it.
func TestKeyOfAnotherProjectIsRefusedExplicitly(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})

	_, err := srv.numberFromKey("FRNT-34")
	if err == nil {
		t.Fatal("another project's key was accepted")
	}
	if !strings.Contains(err.Error(), "FRNT") || !strings.Contains(err.Error(), "CORE") {
		t.Errorf("message = %q, it must name the requested project and the token's", err)
	}
}

// splitRef must accept a sibling project's key: an issue belongs to its recipient, which is not
// always the caller. That is the difference with a task reference.
func TestSplitRefAcceptsSiblingProjects(t *testing.T) {
	tests := []struct {
		ref        string
		wantKey    string
		wantNumber int64
	}{
		{"CORE-34", "CORE", 34},
		{"frnt-7", "FRNT", 7},
		{"12", "CORE", 12},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			key, number, err := splitRef(tc.ref, "CORE")
			if err != nil {
				t.Fatalf("splitRef(%q): %v", tc.ref, err)
			}
			if key != tc.wantKey || number != tc.wantNumber {
				t.Errorf("splitRef(%q) = (%s, %d), expected (%s, %d)",
					tc.ref, key, number, tc.wantKey, tc.wantNumber)
			}
		})
	}

	for _, bad := range []string{"", "  ", "CORE-", "CORE-0", "-3", "abc"} {
		if _, _, err := splitRef(bad, "CORE"); err == nil {
			t.Errorf("splitRef(%q) accepted, expected an error", bad)
		}
	}
}

// The instructions replace the whoami tool: they must tell the agent where it works and who it
// can address, without it having to call anything.
func TestInstructionsCarryTheIdentity(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})
	srv.siblings = []string{"WEB", "API"}

	got := srv.instructions()
	for _, expected := range []string{"CORE", "omiros", "WEB", "API", "check_inbox"} {
		if !strings.Contains(got, expected) {
			t.Errorf("the instructions do not mention %q:\n%s", expected, got)
		}
	}

	// With no sibling project, create_issue has nobody to write to: saying so saves a round trip.
	srv.siblings = nil
	if !strings.Contains(srv.instructions(), "No sibling project") {
		t.Errorf("with no sibling project, the instructions must say so:\n%s", srv.instructions())
	}
}

func TestParseDeadline(t *testing.T) {
	absent, err := parseDeadline("")
	if err != nil || absent != nil {
		t.Errorf("empty deadline: (%v, %v), expected (nil, nil)", absent, err)
	}

	parsed, err := parseDeadline("2026-09-01T12:00:00Z")
	if err != nil {
		t.Fatalf("parseDeadline: %v", err)
	}
	if parsed == nil || parsed.Year() != 2026 || parsed.Month() != 9 {
		t.Errorf("deadline = %v, expected the 1st of September 2026", parsed)
	}

	for _, bad := range []string{"tomorrow", "2026-09-01", "01/09/2026"} {
		if _, err := parseDeadline(bad); err == nil {
			t.Errorf("parseDeadline(%q) accepted, expected an error", bad)
		}
	}
}

// A tool error must come back in the result with isError, never as a JSON-RPC error: the agent
// must be able to read it and correct itself.
func TestUnknownToolIsAToolErrorNotAProtocolError(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})

	result, err := srv.callTool(context.Background(),
		json.RawMessage(`{"name":"delete_everything","arguments":{}}`))
	if err != nil {
		t.Fatalf("callTool returned a protocol error: %v", err)
	}
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("result = %+v, expected isError", result)
	}
}

// A PANIC IS NOT THE END OF A SESSION.
//
// Without the recover in dispatch, a nil dereference in any tool climbs all the way to the main
// goroutine: the process dies, stdout closes, and the agent sees its session disappear WITH NO
// JSON-RPC RESPONSE to the request in flight. It waits on a closed pipe for a message that will
// never come — it can neither read it, nor correct itself from it, nor know what it lost.
//
// The panic is REAL, not simulated: newTestServer leaves `api` nil, so any tool calling the API
// dereferences nil. That is exactly the production failure mode.
//
// MUTATION: removing the defer/recover from dispatch makes this test fall over — the whole package
// panics instead of yielding a response.
func TestAPanicInAToolDoesNotKillTheSession(t *testing.T) {
	stderr, restore := captureStderr(t)
	defer restore()

	responses := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_inbox","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	)

	if len(responses) != 2 {
		t.Fatalf("%d responses, expected 2: the session stopped at the panic", len(responses))
	}

	if responses[0].Error == nil {
		t.Fatalf("the panicking call yielded a result: %+v", responses[0])
	}
	if responses[0].Error.Code != codeInternalError {
		t.Errorf("code = %d, expected %d (internal error)", responses[0].Error.Code, codeInternalError)
	}
	// The agent receives a short, actionable message, not a Go trace.
	if strings.Contains(responses[0].Error.Message, "goroutine") {
		t.Errorf("the Go trace goes to the agent and pollutes its context: %q", responses[0].Error.Message)
	}
	if !strings.Contains(responses[0].Error.Message, "the session goes on") {
		t.Errorf("the message does not tell the agent the session holds: %q", responses[0].Error.Message)
	}

	// The NEXT request is served: that is what "the session goes on" means.
	if responses[1].Error != nil {
		t.Errorf("the ping after the panic failed: %+v", responses[1].Error)
	}

	// STDOUT BELONGS TO THE PROTOCOL. The trace must go to stderr, and only there — a single stray
	// line on stdout breaks the client's JSON-RPC decoding.
	trace := stderr()
	if !strings.Contains(trace, "PANIC") || !strings.Contains(trace, "goroutine") {
		t.Errorf("the trace was not logged to stderr:\n%s", trace)
	}
}

// captureStderr diverts os.Stderr and yields a function that reads its content.
//
// The pipe is drained by a goroutine: without it, a trace longer than the pipe buffer would block
// the write, hence the test itself.
func captureStderr(t *testing.T) (read func() string, restore func()) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	var content string
	var once sync.Once
	collect := func() {
		once.Do(func() {
			os.Stderr = saved
			_ = w.Close()
			content = <-done
		})
	}
	return func() string { collect(); return content }, collect
}
