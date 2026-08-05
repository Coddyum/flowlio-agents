package main

// T1, second half — WHAT THE MCP SURFACE YIELDS OF A REFUSAL.
//
// `docs/DESIGN-TRUST.md` § Le refus indiscernable, channel 2: a refused `create_issue` reaches the
// agent in the form `not found` (9 bytes), `isError: true`, NO other field.
//
// WHAT THIS FILE GUARDS, AND NOTHING MORE. It guards the RENDERING: that the MCP wrapping adds no
// channel the API did not open (a copied status, a diagnostic field, a prefix). It does NOT guard
// the shape of the refusal on the server side — that is
// `internal/feature/issue/module_integration_test.go`, which mounts the real API on the real
// database.
//
// The two hold together through the second test of this file: the text yielded to the agent is a
// FAITHFUL function of what the API answered. A handler that told the trust refusal apart
// (mutation M3) would therefore be restored as such to the agent — the MCP layer does not mask it,
// and cannot mask it without turning this test red.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// newServerAnswering mounts an MCP server whose API always answers the same status/body pair.
//
// Written here rather than derived from newRoutedServer: what is under test is precisely the
// (status, body) pair the API yields, so the test must lay it down itself, not inherit the
// fallback of another harness.
func newServerAnswering(t *testing.T, status int, body string) *mcpServer {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	return &mcpServer{
		out:        &strings.Builder{},
		api:        client.New(ts.URL, "flw_test"),
		projectKey: "CORE",
		teamSlug:   "omiros",
	}
}

// callCreateIssue plays the create_issue tool through the production path — callTool, not the
// implementation — so that the error wrapping is the one the agent receives.
func callCreateIssue(t *testing.T, s *mcpServer, toProject string) map[string]any {
	t.Helper()

	raw := json.RawMessage(`{"name":"create_issue","arguments":{` +
		`"to_project":"` + toProject + `","title":"contract changed?","body":"the endpoint no longer answers"}}`)

	result, err := s.callTool(context.Background(), raw)
	if err != nil {
		t.Fatalf("callTool: JSON-RPC error %v — a tool error must come back in the result", err)
	}
	return result
}

// textOf extracts the text yielded to the agent, checking along the way that the content has no
// other entry and no other field than the contract's.
func textOf(t *testing.T, result map[string]any) string {
	t.Helper()

	content, ok := result["content"].([]map[string]any)
	if !ok {
		t.Fatalf("badly typed content: %T", result["content"])
	}
	if len(content) != 1 {
		t.Fatalf("content = %d entries, expected 1: one more entry is one more channel", len(content))
	}
	if len(content[0]) != 2 {
		t.Fatalf("content entry = %v, expected exactly type and text", content[0])
	}
	if content[0]["type"] != "text" {
		t.Fatalf("type = %v, expected \"text\"", content[0]["type"])
	}

	text, ok := content[0]["text"].(string)
	if !ok {
		t.Fatalf("badly typed text: %T", content[0]["text"])
	}
	return text
}

// TestRefusedCreateIssueRendersNotFoundAndNothingElse checks channel 2 on the canonical refusal.
func TestRefusedCreateIssueRendersNotFoundAndNothingElse(t *testing.T) {
	s := newServerAnswering(t, http.StatusNotFound, `{"error":"not found"}`)

	result := callCreateIssue(t, s, "OPS")

	// Exactly two keys. One field more — a status, a code, a diagnostic — would be precisely the
	// channel the indistinguishable refusal exists to close.
	if len(result) != 2 {
		t.Errorf("result = %v, expected exactly content and isError", result)
	}
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("isError = %v, expected true: a silent refusal would pass for a success", result["isError"])
	}

	text := textOf(t, result)
	if text != "not found" {
		t.Errorf("text = %q, expected %q", text, "not found")
	}
	if len(text) != 9 {
		t.Errorf("text = %d bytes, expected 9 — the length is itself a channel", len(text))
	}
}

// TestMCPTextIsAFaithfulFunctionOfTheAPIResponse is the test that ties the two halves together.
//
// It does not say what the API MUST answer — that is the job of the integration test on the
// feature side. It says the MCP layer cannot yield `not found` to an agent when the API answered
// something else. Without it, the MCP half could be passed by hard-coding `not found` in errText,
// and the guarantee would rest on reading the code alone.
func TestMCPTextIsAFaithfulFunctionOfTheAPIResponse(t *testing.T) {
	s := newServerAnswering(t, http.StatusForbidden, `{"error":"forbidden"}`)

	result := callCreateIssue(t, s, "OPS")

	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("isError = %v, expected true", result["isError"])
	}
	if text := textOf(t, result); text != "forbidden" {
		t.Errorf("text = %q, expected %q: the MCP layer masks what the API answered, so it would "+
			"also mask a distinguishable trust refusal", text, "forbidden")
	}
}
