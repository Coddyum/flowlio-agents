package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                    | Ligne |
// |------------------------|-----------------------------------------------------------|-------|
// | mcpServer              | MCP server state: API client and the token's project       | 57    |
// | runMCP                 | Starts the MCP server on stdio                             | 81    |
// | mcpServer.serve        | Message read loop, one JSON line per message               | 116   |
// | mcpServer.dispatch     | Routes an MCP method, and survives a tool panic            | 170   |
// | mcpServer.initialize   | Answers the MCP handshake                                  | 206   |
// | mcpServer.instructions | Tells the agent where it works, before its first message   | 225   |
// | mcpServer.siblingKeys  | Resolves the other projects of the team                    | 271   |
//
// Fin du sommaire.
// =====================================================================
//
// A JSON-RPC 2.0 MCP server over stdio, written by hand: the protocol fits in a handful of
// methods, and adding an SDK would bring a dependency — and its attack surface — into a binary
// that handles tokens.
//
// Absolute rule of this file: stdout belongs to the protocol. Every message meant for a human
// goes to stderr. A single stray Println breaks the agent's MCP session.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	memoryservice "github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

const (
	// protocolVersion is the MCP protocol revision announced to the client.
	protocolVersion = "2025-06-18"
	// serverName is what the handshake announces, and it matches the key `.mcp.json` carries: an
	// agent that reads one name in its configuration and another in serverInfo has no way to tell it
	// is the same server.
	serverName    = "flowlio-agents"
	serverVersion = "0.1.0"

	// maxMessageBytes bounds an incoming message line. An agent never sends a message that large;
	// the bound keeps a malformed stream from growing the buffer without limit.
	maxMessageBytes = 1 << 20
)

// mcpServer carries the API client and the token identity, resolved once at startup.
type mcpServer struct {
	api        *client.Client
	out        io.Writer
	projectKey string
	teamSlug   string
	// siblings is the list of sibling project keys, resolved at startup. It composes the
	// initialisation instructions: without it, an agent would not know who it may address a
	// question to.
	siblings []string
	// memory is the index of what this repository remembers, resolved at startup and injected into
	// the instructions.
	//
	// THIS IS THE READ-SIDE MECHANISM OF M5, AND IT IS THE WHOLE OF IT. Asking an agent to consult
	// a memory in a tool description makes reading optional, and optional is how the reference
	// system's least-hooked register ended up empty. An agent cannot start a session without its
	// instructions, so the index costs no turn and no decision: it is simply already there.
	memory []memoryservice.IndexLine
}

// runMCP starts the MCP server on stdio.
//
// The token identity is resolved BEFORE the first request: the project key composes and validates
// the readable identifiers (CORE-34), and an invalid token must fail right away with a clear
// message rather than on every tool call.
func runMCP(ctx context.Context, _ []string) error {
	api, err := mcpClient()
	if err != nil {
		return err
	}

	var identity struct {
		Scope string `json:"scope"`
		service.Identity
	}
	if err := api.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &identity); err != nil {
		return fmt.Errorf("resolving the token: %w", err)
	}
	if identity.ProjectKey == "" {
		return errors.New("this token is not scoped to a project — the MCP server needs a project " +
			"token (flowlio token create <KEY> <name>)")
	}

	srv := &mcpServer{
		api:        api,
		out:        os.Stdout,
		projectKey: identity.ProjectKey,
		teamSlug:   identity.TeamSlug,
	}
	srv.siblings = srv.siblingKeys(ctx)
	srv.memory = srv.memoryIndex(ctx)

	return srv.serve(ctx, os.Stdin)
}

// serve reads incoming messages, one per line, and answers on stdout.
//
// A decoding error does not interrupt the session: it yields an error response and the loop goes
// on. Closing the stream at the first malformed message would cost the agent a whole session for
// one stray line.
func (s *mcpServer) serve(ctx context.Context, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxMessageBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeResponse(errorResponse(nil, codeParseError, "unreadable JSON message"))
			continue
		}

		// No ID: this is a notification (notifications/initialized for instance). The protocol
		// forbids answering it.
		if len(req.ID) == 0 {
			continue
		}
		if req.JSONRPC != "2.0" {
			s.writeResponse(errorResponse(req.ID, codeInvalidRequest, "jsonrpc must be 2.0"))
			continue
		}

		s.writeResponse(s.dispatch(ctx, req))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	return nil
}

// dispatch routes an MCP method.
//
// A tool error is NOT a protocol error: it comes back in the result with isError, so the agent
// reads it and corrects itself. The JSON-RPC codes stay reserved for protocol faults, which the
// agent cannot correct.
//
// A PANIC MUST NOT KILL THE SESSION. Without the recover below, a nil dereference in any tool
// carries the panic up to the main goroutine: the process dies, stdout closes, and the agent sees
// its MCP session vanish WITH NO JSON-RPC ANSWER to the request in flight. It waits for a message
// that will never come, on a closed pipe — the worst failure mode of the whole product, because
// the agent can neither read it, nor correct itself, nor even know what it lost.
//
// The recover is here and not in callTool: it ALSO covers initialize, tools/list and the routing
// itself. A panic in instructions() is as fatal as a panic in a tool.
//
// The trace goes to STDERR, never to stdout: stdout belongs to the protocol. It goes there whole
// — this is a product bug, not sensitive data, and without it the fault is irreproducible. What
// the agent gets is a short message: it has no use for a Go trace, and pouring one into its
// context would pollute it for nothing.
func (s *mcpServer) dispatch(ctx context.Context, req rpcRequest) (resp rpcResponse) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		fmt.Fprintf(os.Stderr, "flowlio mcp: PANIC on %s: %v\n%s\n", req.Method, r, debug.Stack())
		resp = errorResponse(req.ID, codeInternalError,
			"internal flowlio server error on "+req.Method+
				" — the session goes on, the call failed")
	}()

	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: s.initialize()}

	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: struct{}{}}

	case "tools/list":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolsListResult()}

	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			return errorResponse(req.ID, codeInvalidParams, err.Error())
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}

	default:
		return errorResponse(req.ID, codeMethodNotFound, "unknown method: "+req.Method)
	}
}

// initialize answers the handshake. The server announces tools only: no resources, no prompts, no
// sampling — the announced surface is the one actually served.
func (s *mcpServer) initialize() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"instructions": s.instructions(),
	}
}

// instructions tells the agent where it works, before its first message.
//
// This is what replaces a `whoami` tool: its content is constant over the life of the token, so
// billing it as a schema on every turn AND as a round trip on the first one would pay twice for
// something already known at startup.
func (s *mcpServer) instructions() string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the agent of project %s, in team %s.\n", s.projectKey, s.teamSlug)
	b.WriteString("A reference reads KEY-NUMBER (for example " + s.projectKey + "-34); " +
		"tasks and issues share the same numbering, so a reference names exactly one object.\n")

	if len(s.siblings) > 0 {
		fmt.Fprintf(&b, "Sibling projects, the ones you may address a question to: %s.\n",
			strings.Join(s.siblings, ", "))
	} else {
		b.WriteString("No sibling project in this team: create_issue has nobody to write to.\n")
	}

	// The framing of third-party content lives HERE and nowhere else: it is a server constant,
	// paid once per session, and it is the parameter of no tool. Nobody can therefore switch it
	// off from a call. Model detail: mcp_untrusted.go.
	b.WriteString(framingRule + "\n")

	// The memory index goes BEFORE check_inbox, and the order is the message: what this repository
	// already decided is context for reading the backlog, not a footnote to it. An agent that reads
	// its tasks first has already started forming a plan the memory may contradict.
	//
	// Titles only. The index is paid on every session; a body here would make it the memory itself,
	// and there would be nothing left to read on demand.
	if len(s.memory) > 0 {
		b.WriteString("\nWhat this repository remembers — read with recall <slug> before deciding " +
			"anything it covers:\n")
		for _, line := range s.memory {
			fmt.Fprintf(&b, "  %s (%s): %s\n", line.Slug, line.Kind, line.Title)
		}
		b.WriteString("Anything you settle or learn goes back with remember; a decision that " +
			"changes an earlier one names it in supersedes.\n")
	} else {
		b.WriteString("\nThis repository has remembered nothing yet. When you settle something or " +
			"learn what will bite again, write it with remember.\n")
	}

	b.WriteString("Start with check_inbox: it says what awaits you and what you had left in progress.")
	return b.String()
}

// siblingKeys resolves the other projects of the team.
//
// Best effort: a failure must not keep the session from starting, it only removes one sentence
// from the instructions. The error goes to stderr — never to stdout, which belongs to the
// protocol.
func (s *mcpServer) siblingKeys(ctx context.Context) []string {
	var projects []struct {
		Key string `json:"key"`
	}
	if err := s.api.Do(ctx, http.MethodGet, workspaceAPI+"/projects", nil, &projects); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: sibling project list unavailable: %v\n", err)
		return nil
	}

	keys := make([]string, 0, len(projects))
	for _, p := range projects {
		if p.Key != s.projectKey {
			keys = append(keys, p.Key)
		}
	}
	return keys
}
