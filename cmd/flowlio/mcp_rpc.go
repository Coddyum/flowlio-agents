package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                  | Ligne |
// |-------------------------|---------------------------------------------------------|-------|
// | rpcRequest              | Incoming JSON-RPC 2.0 message                             | 40    |
// | rpcResponse             | Outgoing JSON-RPC 2.0 message                             | 48    |
// | rpcError                | JSON-RPC 2.0 error                                        | 56    |
// | mcpServer.writeResponse | Writes a response to stdout, one line per message         | 65    |
// | errorResponse           | Builds a JSON-RPC error response                          | 77    |
//
// Fin du sommaire.
// =====================================================================
//
// The JSON-RPC 2.0 TRANSPORT, kept apart from the MCP semantics that live in mcp.go.
//
// Absolute rule, the same one as in mcp.go: STDOUT BELONGS TO THE PROTOCOL. Every message meant
// for a human — an encoding error, a panic trace — goes to stderr. A single stray Println breaks
// the agent's MCP session, and it has no way to diagnose that.

import (
	"encoding/json"
	"fmt"
	"os"
)

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	// codeInternalError is reserved for the panic dispatch recovers. No nominal path produces it:
	// seeing it in a log means a server bug, not a mistake by the agent.
	codeInternalError = -32603
)

// rpcRequest is an incoming message. ID is absent for a notification, which is never answered.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is an outgoing message. Result and Error are mutually exclusive.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError carries the code and the message of a protocol error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeResponse writes a response to stdout, one line per message.
//
// A write failure goes to stderr: the agent will not read anything anymore anyway, and writing
// the error to stdout would corrupt the protocol stream.
func (s *mcpServer) writeResponse(resp rpcResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: encoding the response: %v\n", err)
		return
	}
	if _, err := fmt.Fprintf(s.out, "%s\n", raw); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: writing the response: %v\n", err)
	}
}

// errorResponse builds a JSON-RPC error response.
func errorResponse(id json.RawMessage, code int, message string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}
