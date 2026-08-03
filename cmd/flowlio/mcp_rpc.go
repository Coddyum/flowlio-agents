package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                 | Résumé                                                  | Ligne |
// |-------------------------|---------------------------------------------------------|-------|
// | rpcRequest              | Message entrant JSON-RPC 2.0                              | 41    |
// | rpcResponse             | Message sortant JSON-RPC 2.0                              | 49    |
// | rpcError                | Erreur JSON-RPC 2.0                                       | 57    |
// | mcpServer.writeResponse | Écrit une réponse sur stdout, une ligne par message        | 66    |
// | errorResponse           | Construit une réponse d'erreur JSON-RPC                   | 78    |
//
// Fin du sommaire.
// =====================================================================
//
// Le TRANSPORT JSON-RPC 2.0, séparé de la sémantique MCP qui vit dans mcp.go.
//
// Règle absolue, la même que dans mcp.go : STDOUT APPARTIENT AU PROTOCOLE. Tout message destiné
// à un humain — erreur d'encodage, trace de panic — part sur stderr. Un seul Println égaré casse
// la session MCP de l'agent, et il n'a aucun moyen de le diagnostiquer.

import (
	"encoding/json"
	"fmt"
	"os"
)

// Codes d'erreur JSON-RPC 2.0.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	// codeInternalError est réservé au panic récupéré par dispatch. Aucun chemin nominal ne le
	// produit : le voir dans un journal signale un bug du serveur, pas une faute de l'agent.
	codeInternalError = -32603
)

// rpcRequest est un message entrant. ID est absent pour une notification, à laquelle on ne
// répond jamais.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse est un message sortant. Result et Error s'excluent mutuellement.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError porte le code et le message d'une erreur de protocole.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeResponse écrit une réponse sur stdout, une ligne par message.
//
// Un échec d'écriture part sur stderr : l'agent ne lira de toute façon plus rien, et écrire
// l'erreur sur stdout corromprait le flux du protocole.
func (s *mcpServer) writeResponse(resp rpcResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: encodage de la réponse: %v\n", err)
		return
	}
	if _, err := fmt.Fprintf(s.out, "%s\n", raw); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: écriture de la réponse: %v\n", err)
	}
}

// errorResponse construit une réponse d'erreur JSON-RPC.
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
