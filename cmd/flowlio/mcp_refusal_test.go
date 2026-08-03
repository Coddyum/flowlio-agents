package main

// T1, second volet — CE QUE LA SURFACE MCP REND D'UN REFUS.
//
// `docs/DESIGN-TRUST.md` § Le refus indiscernable, canal 2 : un `create_issue` refusé arrive à
// l'agent sous la forme `not found` (9 octets), `isError: true`, AUCUN autre champ.
//
// CE QUE CE FICHIER GARDE, ET RIEN DE PLUS. Il garde le RENDU : que l'emballage MCP n'ajoute pas
// de canal que l'API n'a pas ouvert (un statut recopié, un champ de diagnostic, un préfixe). Il ne
// garde PAS la forme du refus côté serveur — ça, c'est `internal/feature/issue/module_integration_test.go`,
// qui monte la vraie API sur la vraie base.
//
// Les deux tiennent ensemble par le second test de ce fichier : le texte rendu à l'agent est une
// fonction FIDÈLE de ce que l'API a répondu. Un handler qui distinguerait le refus de confiance
// (mutation M3) serait donc restitué tel quel à l'agent — la couche MCP ne le masque pas, et ne
// peut pas le masquer sans faire virer ce test au rouge.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// newServerAnswering monte un serveur MCP dont l'API répond toujours le même couple statut/corps.
//
// Écrit ici plutôt que dérivé de newRoutedServer : ce qui est sous test est précisément le couple
// (statut, corps) que l'API rend, donc le test doit le poser lui-même, pas hériter du repli d'un
// autre harnais.
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

// callCreateIssue joue l'outil create_issue par le chemin de production — callTool, pas
// l'implémentation — pour que l'emballage d'erreur soit celui que l'agent reçoit.
func callCreateIssue(t *testing.T, s *mcpServer, toProject string) map[string]any {
	t.Helper()

	raw := json.RawMessage(`{"name":"create_issue","arguments":{` +
		`"to_project":"` + toProject + `","title":"contrat modifié ?","body":"le endpoint ne répond plus"}}`)

	result, err := s.callTool(context.Background(), raw)
	if err != nil {
		t.Fatalf("callTool: erreur JSON-RPC %v — une erreur d'outil doit revenir dans le résultat", err)
	}
	return result
}

// textOf extrait le texte rendu à l'agent, en vérifiant au passage que le contenu n'a pas d'autre
// entrée ni d'autre champ que ceux du contrat.
func textOf(t *testing.T, result map[string]any) string {
	t.Helper()

	content, ok := result["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content mal typé: %T", result["content"])
	}
	if len(content) != 1 {
		t.Fatalf("content = %d entrées, attendu 1 : une entrée de plus est un canal de plus", len(content))
	}
	if len(content[0]) != 2 {
		t.Fatalf("entrée de content = %v, attendu exactement type et text", content[0])
	}
	if content[0]["type"] != "text" {
		t.Fatalf("type = %v, attendu \"text\"", content[0]["type"])
	}

	text, ok := content[0]["text"].(string)
	if !ok {
		t.Fatalf("text mal typé: %T", content[0]["text"])
	}
	return text
}

// TestRefusedCreateIssueRendersNotFoundAndNothingElse vérifie le canal 2 sur le refus canonique.
func TestRefusedCreateIssueRendersNotFoundAndNothingElse(t *testing.T) {
	s := newServerAnswering(t, http.StatusNotFound, `{"error":"not found"}`)

	result := callCreateIssue(t, s, "OPS")

	// Exactement deux clés. Un champ de plus — un statut, un code, un diagnostic — serait
	// précisément le canal que le refus indiscernable existe pour fermer.
	if len(result) != 2 {
		t.Errorf("résultat = %v, attendu exactement content et isError", result)
	}
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("isError = %v, attendu true : un refus muet passerait pour un succès", result["isError"])
	}

	text := textOf(t, result)
	if text != "not found" {
		t.Errorf("texte = %q, attendu %q", text, "not found")
	}
	if len(text) != 9 {
		t.Errorf("texte = %d octets, attendu 9 — la longueur est elle-même un canal", len(text))
	}
}

// TestMCPTextIsAFaithfulFunctionOfTheAPIResponse est le test qui relie les deux volets.
//
// Il ne dit pas ce que l'API DOIT répondre — c'est le rôle du test d'intégration côté feature. Il
// dit que la couche MCP ne peut pas rendre `not found` à un agent quand l'API a répondu autre
// chose. Sans lui, on pourrait faire passer le volet MCP en écrivant `not found` en dur dans
// errText, et la garantie ne tiendrait plus qu'à la lecture du code.
func TestMCPTextIsAFaithfulFunctionOfTheAPIResponse(t *testing.T) {
	s := newServerAnswering(t, http.StatusForbidden, `{"error":"forbidden"}`)

	result := callCreateIssue(t, s, "OPS")

	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("isError = %v, attendu true", result["isError"])
	}
	if text := textOf(t, result); text != "forbidden" {
		t.Errorf("texte = %q, attendu %q : la couche MCP masque ce que l'API a répondu, "+
			"donc elle masquerait aussi un refus de confiance distinguable", text, "forbidden")
	}
}
