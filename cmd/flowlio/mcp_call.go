package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | callParams         | Body of a tools/call call                                    | 35    |
// | mcpServer.callTool | Runs a tool and wraps its result                             | 45    |
// | mcpServer.invoke   | Routes to the implementation of the requested tool           | 68    |
// | writeResult        | Wraps a write return in the {ref, object} shape              | 105   |
// | textResult         | Wraps a tool result for the MCP client                       | 120   |
// | errText            | Wraps a tool error so the agent can read it                  | 136   |
// | parseDeadline      | Reads an RFC 3339 deadline, absent when the string is empty  | 157   |
//
// Fin du sommaire.
// =====================================================================
//
// The tools/call plumbing. The implementations of the ten tools live in mcp_task_tools.go and
// mcp_issue_tools.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// callParams is the body of a tools/call call.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callTool runs a tool and wraps its result.
//
// A tool error comes back in the result with isError, not as a JSON-RPC error: the agent must be
// able to read it and correct itself. The API error message is passed through as is, because it
// is the one that tells the agent what went wrong.
func (s *mcpServer) callTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var params callParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, errors.New("unreadable call parameters")
	}
	if params.Name == "" {
		return nil, errors.New("missing tool name")
	}

	// A tool without arguments may arrive with no arguments field: an empty object spares every
	// implementation from handling that case.
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage("{}")
	}

	value, err := s.invoke(ctx, params.Name, params.Arguments)
	if err != nil {
		return errText(err), nil
	}
	return textResult(value), nil
}

// invoke routes to the implementation of the requested tool.
func (s *mcpServer) invoke(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "list_tasks":
		return s.listTasks(ctx, args)
	case "get":
		return s.get(ctx, args)
	case "create_task":
		return s.createTask(ctx, args)
	case "update_task":
		return s.updateTask(ctx, args)
	case "block_task":
		return s.blockTask(ctx, args)
	case "unblock_task":
		return s.unblockTask(ctx, args)
	case "create_issue":
		return s.createIssue(ctx, args)
	case "list_issues":
		return s.listIssues(ctx, args)
	case "answer_issue":
		return s.answerIssue(ctx, args)
	case "check_inbox":
		return s.checkInbox(ctx, args)
	case "remember":
		return s.remember(ctx, args)
	case "recall":
		return s.recall(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// writeResult wraps the return of a write, in the ONLY shape the server produces:
// {"ref": "CORE-34", "<kind>": {…}}.
//
// That is the whole point of this function: without it, every tool composed its own envelope, and
// an agent had to guess where to read the reference depending on which one it had just called —
// under "key" for a task, inside the object for an issue. One shape, one place.
func writeResult(kind, ref string, value any) map[string]any {
	return map[string]any{"ref": ref, kind: value}
}

// textResult wraps a tool result. The content is JSON: an agent reparses it without ambiguity,
// where a textual rendering would ask it to guess a format.
//
// HTML escaping is DISABLED, and that is not cosmetic. By default, encoding/json replaces `<`,
// `>` and `&` with their six-character unicode escapes, to make the JSON safe to paste inside a
// script tag — a worry this binary does not have, since its output goes to stdout in a JSON-RPC
// stream. With escaping on, the marking of external content reached the agent fully escaped: a
// marking that is only readable after a second decoding is not a marking. See mcp_untrusted.go.
//
// The text field is re-encoded by writeResponse anyway, and that is where transport escaping
// happens — the MCP client undoes it before handing back to the agent.
func textResult(value any) map[string]any {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return errText(fmt.Errorf("encoding the result: %w", err))
	}

	return map[string]any{
		// Encode appends a trailing newline: dropping it avoids paying a useless byte in the
		// agent's context on every tool call.
		"content": []map[string]any{{"type": "text", "text": strings.TrimRight(buf.String(), "\n")}},
	}
}

// errText wraps a tool error so the agent reads it and corrects itself.
func errText(err error) map[string]any {
	message := err.Error()

	// An API error already carries the server's message; prefixing it a second time would tell
	// the agent nothing more.
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		message = apiErr.Message
		if message == "" {
			message = http.StatusText(apiErr.Status)
		}
	}

	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": message}},
		"isError": true,
	}
}

// parseDeadline reads an RFC 3339 deadline. An empty string means "no deadline supplied" and is
// therefore not an error: the field is optional in every tool that accepts it.
func parseDeadline(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("unreadable deadline %q (expected format: 2026-09-01T12:00:00Z)", value)
	}
	return &parsed, nil
}
