package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | rpcRequest         | Message entrant JSON-RPC 2.0                                | 66    |
// | rpcResponse        | Message sortant JSON-RPC 2.0                                | 74    |
// | rpcError           | Erreur JSON-RPC 2.0                                         | 82    |
// | mcpServer          | État du serveur MCP : client API et projet du token          | 88    |
// | runMCP             | Lance le serveur MCP sur stdio                               | 104   |
// | mcpServer.serve    | Boucle de lecture des messages, une ligne JSON par message    | 138   |
// | mcpServer.dispatch | Route une méthode MCP vers son implémentation                | 178   |
// | mcpServer.initialize | Répond à la poignée de main MCP                            | 203   |
// | mcpServer.instructions | Dit à l'agent où il travaille, avant son premier message | 222   |
// | mcpServer.siblingKeys | Résout les autres projets de la team                      | 244   |
// | writeResponse      | Écrit une réponse sur stdout, une ligne par message           | 266   |
// | errorResponse      | Construit une réponse d'erreur JSON-RPC                       | 278   |
//
// Fin du sommaire.
// =====================================================================
//
// Serveur MCP en JSON-RPC 2.0 sur stdio, écrit à la main : le protocole tient en une poignée de
// méthodes, et l'ajout d'un SDK ferait entrer une dépendance — et sa surface d'attaque — dans un
// binaire qui manipule des tokens.
//
// Règle absolue de ce fichier : stdout appartient au protocole. Tout message destiné à un humain
// part sur stderr. Un seul Println égaré casse la session MCP de l'agent.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-ia/internal/pkg/client"
)

const (
	// protocolVersion est la révision du protocole MCP annoncée au client.
	protocolVersion = "2025-06-18"
	serverName      = "flowlio"
	serverVersion   = "0.1.0"

	// maxMessageBytes borne une ligne de message entrant. Un agent n'envoie jamais un message de
	// cette taille ; la borne évite qu'un flux malformé fasse grossir le tampon sans limite.
	maxMessageBytes = 1 << 20
)

// Codes d'erreur JSON-RPC 2.0.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
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

// mcpServer porte le client API et l'identité du token, résolue une seule fois au démarrage.
type mcpServer struct {
	api        *client.Client
	out        io.Writer
	projectKey string
	teamSlug   string
	// siblings est la liste des clés de projets frères, résolue au démarrage. Elle sert à
	// composer les instructions d'initialisation : sans elle, un agent ne saurait pas à qui
	// il peut adresser une question.
	siblings []string
}

// runMCP lance le serveur MCP sur stdio.
//
// L'identité du token est résolue AVANT la première requête : la clé du projet sert à composer
// et à valider les identifiants lisibles (CORE-34), et un token invalide doit échouer tout de
// suite avec un message clair plutôt qu'à chaque appel d'outil.
func runMCP(ctx context.Context, _ []string) error {
	api, err := newClient()
	if err != nil {
		return err
	}

	var identity struct {
		Scope string `json:"scope"`
		service.Identity
	}
	if err := api.Do(ctx, http.MethodGet, workspaceAPI+"/whoami", nil, &identity); err != nil {
		return fmt.Errorf("résolution du token: %w", err)
	}
	if identity.ProjectKey == "" {
		return errors.New("ce token n'est pas scopé à un projet — le serveur MCP a besoin d'un " +
			"token de projet (flowlio token create <KEY> <nom>)")
	}

	srv := &mcpServer{
		api:        api,
		out:        os.Stdout,
		projectKey: identity.ProjectKey,
		teamSlug:   identity.TeamSlug,
	}
	srv.siblings = srv.siblingKeys(ctx)

	return srv.serve(ctx, os.Stdin)
}

// serve lit les messages entrants, un par ligne, et répond sur stdout.
//
// Une erreur de décodage n'interrompt pas la session : elle donne une réponse d'erreur et la
// boucle continue. Fermer le flux au premier message malformé ferait perdre à l'agent une
// session entière pour une ligne parasite.
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
			s.writeResponse(errorResponse(nil, codeParseError, "message JSON illisible"))
			continue
		}

		// Pas d'ID : c'est une notification (notifications/initialized par exemple). Le protocole
		// interdit d'y répondre.
		if len(req.ID) == 0 {
			continue
		}
		if req.JSONRPC != "2.0" {
			s.writeResponse(errorResponse(req.ID, codeInvalidRequest, "jsonrpc doit valoir 2.0"))
			continue
		}

		s.writeResponse(s.dispatch(ctx, req))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lecture de stdin: %w", err)
	}
	return nil
}

// dispatch route une méthode MCP.
//
// Une erreur d'outil n'est PAS une erreur de protocole : elle revient dans le résultat avec
// isError, pour que l'agent la lise et se corrige. Les codes JSON-RPC restent réservés aux
// défauts de protocole, que l'agent ne peut pas corriger.
func (s *mcpServer) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
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
		return errorResponse(req.ID, codeMethodNotFound, "méthode inconnue: "+req.Method)
	}
}

// initialize répond à la poignée de main. Le serveur n'annonce que les outils : ni ressources,
// ni prompts, ni échantillonnage — la surface annoncée est celle qui est réellement servie.
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

// instructions dit à l'agent où il travaille, avant son premier message.
//
// C'est ce qui remplace un outil `whoami` : son contenu est constant sur la vie du token, donc
// le facturer en schéma à chaque tour ET en aller-retour au premier serait payer deux fois pour
// une information qu'on connaît déjà au démarrage.
func (s *mcpServer) instructions() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tu es l'agent du projet %s, dans la team %s.\n", s.projectKey, s.teamSlug)
	b.WriteString("Une référence se lit CLE-NUMERO (par exemple " + s.projectKey + "-34) ; " +
		"tâches et issues partagent la même numérotation, donc une référence désigne un seul objet.\n")

	if len(s.siblings) > 0 {
		fmt.Fprintf(&b, "Projets frères, à qui tu peux adresser une question : %s.\n",
			strings.Join(s.siblings, ", "))
	} else {
		b.WriteString("Aucun projet frère dans cette team : create_issue n'a personne à qui écrire.\n")
	}

	b.WriteString("Commence par check_inbox : il dit ce qui t'attend et ce que tu avais laissé en cours.")
	return b.String()
}

// siblingKeys résout les autres projets de la team.
//
// Best effort : un échec ne doit pas empêcher la session de démarrer, il retire seulement une
// phrase des instructions. L'erreur part sur stderr — jamais sur stdout, qui appartient au
// protocole.
func (s *mcpServer) siblingKeys(ctx context.Context) []string {
	var projects []struct {
		Key string `json:"key"`
	}
	if err := s.api.Do(ctx, http.MethodGet, workspaceAPI+"/projects", nil, &projects); err != nil {
		fmt.Fprintf(os.Stderr, "flowlio mcp: liste des projets frères indisponible: %v\n", err)
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
