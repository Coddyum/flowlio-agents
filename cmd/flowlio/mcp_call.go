package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | callParams         | Corps d'un appel tools/call                                  | 32    |
// | mcpServer.callTool | Exécute un outil et emballe son résultat                     | 42    |
// | mcpServer.invoke   | Route vers l'implémentation de l'outil demandé               | 65    |
// | textResult         | Emballe un résultat d'outil pour le client MCP               | 92    |
// | errText            | Emballe une erreur d'outil, lisible par l'agent              | 103   |
// | parseDeadline      | Lit une échéance RFC 3339, absente si la chaîne est vide      | 124   |
//
// Fin du sommaire.
// =====================================================================
//
// Plomberie de tools/call. Les implémentations des six outils sont dans mcp_task_tools.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/pkg/client"
)

// callParams est le corps d'un appel tools/call.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callTool exécute un outil et emballe son résultat.
//
// Une erreur d'outil revient dans le résultat avec isError, pas en erreur JSON-RPC : l'agent doit
// pouvoir la lire et se corriger. Le message d'erreur de l'API est repris tel quel, parce que
// c'est lui qui dit à l'agent ce qui n'allait pas.
func (s *mcpServer) callTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var params callParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, errors.New("paramètres d'appel illisibles")
	}
	if params.Name == "" {
		return nil, errors.New("nom d'outil manquant")
	}

	// Un outil sans argument peut arriver sans champ arguments : un objet vide évite à chaque
	// implémentation de traiter ce cas.
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage("{}")
	}

	value, err := s.invoke(ctx, params.Name, params.Arguments)
	if err != nil {
		return errText(err), nil
	}
	return textResult(value), nil
}

// invoke route vers l'implémentation de l'outil demandé.
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
	case "add_task_note":
		return s.addNote(ctx, args)
	case "create_issue":
		return s.createIssue(ctx, args)
	case "list_issues":
		return s.listIssues(ctx, args)
	case "answer_issue":
		return s.answerIssue(ctx, args)
	case "check_inbox":
		return s.checkInbox(ctx, args)
	default:
		return nil, fmt.Errorf("outil inconnu: %s", name)
	}
}

// textResult emballe un résultat d'outil. Le contenu est du JSON : un agent le reparse sans
// ambiguïté, là où un rendu textuel demanderait de deviner un format.
func textResult(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return errText(fmt.Errorf("encodage du résultat: %w", err))
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(raw)}},
	}
}

// errText emballe une erreur d'outil de façon que l'agent la lise et se corrige.
func errText(err error) map[string]any {
	message := err.Error()

	// Une erreur d'API porte déjà le message du serveur ; le préfixer une seconde fois ne dirait
	// rien de plus à l'agent.
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

// parseDeadline lit une échéance RFC 3339. Une chaîne vide vaut « pas d'échéance fournie » et
// n'est donc pas une erreur : le champ est optionnel dans tous les outils qui l'acceptent.
func parseDeadline(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("échéance %q illisible (format attendu : 2026-09-01T12:00:00Z)", value)
	}
	return &parsed, nil
}
