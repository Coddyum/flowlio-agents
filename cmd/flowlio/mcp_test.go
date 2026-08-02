package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newTestServer construit un serveur MCP sans client API : les méthodes de protocole
// (initialize, tools/list, ping) n'en ont pas besoin, et ces tests doivent tourner sans
// infrastructure.
func newTestServer(out *bytes.Buffer) *mcpServer {
	return &mcpServer{out: out, projectKey: "CORE", teamSlug: "omiros"}
}

// exchange fait passer des messages dans la boucle du serveur et renvoie les réponses décodées.
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
			t.Fatalf("réponse illisible %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestInitializeHandshake(t *testing.T) {
	responses := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(responses) != 1 {
		t.Fatalf("%d réponses, attendu 1", len(responses))
	}

	result, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("résultat de type %T, attendu un objet", responses[0].Result)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, attendu %s", result["protocolVersion"], protocolVersion)
	}

	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("capabilities absent de la réponse d'initialize")
	}
	if _, declared := capabilities["tools"]; !declared {
		t.Error("le serveur doit annoncer la capacité tools")
	}
	// Le serveur ne sert ni ressources ni prompts : les annoncer serait mentir au client.
	for _, absent := range []string{"resources", "prompts", "sampling"} {
		if _, found := capabilities[absent]; found {
			t.Errorf("capacité %q annoncée alors qu'elle n'est pas servie", absent)
		}
	}
}

// Une notification n'a pas d'ID : y répondre est une violation de JSON-RPC 2.0 que certains
// clients MCP traitent comme une erreur de session.
func TestNotificationGetsNoResponse(t *testing.T) {
	responses := exchange(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
	)
	if len(responses) != 1 {
		t.Fatalf("%d réponses, attendu 1 (la notification ne doit pas en produire)", len(responses))
	}
	if string(responses[0].ID) != "1" {
		t.Errorf("la seule réponse doit être celle du ping, ID = %s", responses[0].ID)
	}
}

// Une ligne illisible ne doit pas fermer la session : un agent perdrait tout son contexte de
// travail pour une ligne parasite.
func TestMalformedMessageDoesNotKillSession(t *testing.T) {
	responses := exchange(t,
		`ceci n'est pas du JSON`,
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`,
	)
	if len(responses) != 2 {
		t.Fatalf("%d réponses, attendu 2", len(responses))
	}
	if responses[0].Error == nil || responses[0].Error.Code != codeParseError {
		t.Errorf("première réponse = %+v, attendu une erreur de parsing", responses[0].Error)
	}
	if responses[1].Error != nil {
		t.Errorf("la session doit continuer après une ligne illisible: %+v", responses[1].Error)
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected int
	}{
		{
			"méthode inconnue",
			`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
			codeMethodNotFound,
		},
		{
			"version de protocole erronée",
			`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
			codeInvalidRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responses := exchange(t, tc.message)
			if len(responses) != 1 {
				t.Fatalf("%d réponses, attendu 1", len(responses))
			}
			if responses[0].Error == nil || responses[0].Error.Code != tc.expected {
				t.Errorf("erreur = %+v, attendu le code %d", responses[0].Error, tc.expected)
			}
		})
	}
}

// La surface MCP est un budget : chaque outil est réinjecté dans le contexte de l'agent à chaque
// tour. Ce test échoue si quelqu'un en ajoute un sans y penser, et l'ordre est vérifié parce
// qu'il est celui dans lequel un agent découvre le produit.
func TestToolSurfaceIsSmallAndWellFormed(t *testing.T) {
	expected := []string{
		"list_tasks", "get", "create_task", "update_task", "add_task_note",
		"create_issue", "list_issues", "answer_issue", "check_inbox",
	}

	defs := tools()
	if len(defs) != len(expected) {
		t.Fatalf("%d outils exposés, attendu %d — la surface MCP est un budget, pas une liste de souhaits",
			len(defs), len(expected))
	}

	seen := make(map[string]bool, len(defs))
	for i, def := range defs {
		if def.Name != expected[i] {
			t.Errorf("outil %d = %q, attendu %q", i, def.Name, expected[i])
		}
		if seen[def.Name] {
			t.Errorf("outil %q déclaré deux fois", def.Name)
		}
		seen[def.Name] = true

		if def.Description == "" {
			t.Errorf("outil %q sans description : un agent devrait deviner ce qu'il fait", def.Name)
		}
		if def.InputSchema["type"] != "object" {
			t.Errorf("outil %q: schéma de type %v, attendu object", def.Name, def.InputSchema["type"])
		}
		if _, ok := def.InputSchema["properties"].(map[string]any); !ok {
			t.Errorf("outil %q: propriétés absentes du schéma", def.Name)
		}

		// Aucun outil ne doit accepter un projet en paramètre : le projet vient du token, et un
		// paramètre serait une surface où le scope pourrait être contourné.
		properties, _ := def.InputSchema["properties"].(map[string]any)
		// `to_project` est le seul paramètre de projet toléré : il désigne le DESTINATAIRE d'une
		// question, pas un scope de lecture. Aucun outil ne peut choisir le projet qu'il LIT.
		forbidden := []string{"project", "project_id", "team", "team_id"}
		if def.Name == "create_issue" {
			forbidden = []string{"project", "project_id", "team", "team_id", "from_project"}
		}
		for _, forbidden := range forbidden {
			if _, found := properties[forbidden]; found {
				t.Errorf("outil %q accepte %q en paramètre : le scope vient du token, jamais de l'appel",
					def.Name, forbidden)
			}
		}
	}
}

// Le schéma annoncé doit être du JSON valide, sinon le client MCP rejette la session entière.
func TestToolsListIsSerializable(t *testing.T) {
	responses := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("tools/list a échoué: %+v", responses)
	}

	result, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("résultat de type %T", responses[0].Result)
	}
	listed, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools de type %T, attendu un tableau", result["tools"])
	}
	if len(listed) != len(tools()) {
		t.Errorf("%d outils sérialisés, attendu %d", len(listed), len(tools()))
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
		t.Run("valide "+key, func(t *testing.T) {
			number, err := srv.numberFromKey(key)
			if err != nil {
				t.Fatalf("numberFromKey(%q): %v", key, err)
			}
			if number != expected {
				t.Errorf("numberFromKey(%q) = %d, attendu %d", key, number, expected)
			}
		})
	}

	invalid := []string{"", "   ", "CORE-", "CORE-abc", "-12", "0", "CORE-0", "34.5", "3 4"}
	for _, key := range invalid {
		t.Run("invalide "+key, func(t *testing.T) {
			if _, err := srv.numberFromKey(key); err == nil {
				t.Errorf("numberFromKey(%q) accepté, attendu une erreur", key)
			}
		})
	}
}

// La clé d'un autre projet doit être refusée AVEC une explication : un agent qui reçoit un 404
// conclurait que la tâche n'existe pas, alors qu'elle existe et qu'il n'y a simplement pas accès.
func TestKeyOfAnotherProjectIsRefusedExplicitly(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})

	_, err := srv.numberFromKey("FRNT-34")
	if err == nil {
		t.Fatal("la clé d'un autre projet a été acceptée")
	}
	if !strings.Contains(err.Error(), "FRNT") || !strings.Contains(err.Error(), "CORE") {
		t.Errorf("message = %q, il doit nommer le projet demandé et celui du token", err)
	}
}

// splitRef doit accepter la clé d'un projet frère : une issue appartient à son destinataire,
// qui n'est pas toujours l'appelant. C'est la différence avec une référence de tâche.
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
				t.Errorf("splitRef(%q) = (%s, %d), attendu (%s, %d)",
					tc.ref, key, number, tc.wantKey, tc.wantNumber)
			}
		})
	}

	for _, bad := range []string{"", "  ", "CORE-", "CORE-0", "-3", "abc"} {
		if _, _, err := splitRef(bad, "CORE"); err == nil {
			t.Errorf("splitRef(%q) accepté, attendu une erreur", bad)
		}
	}
}

// Les instructions remplacent l'outil whoami : elles doivent dire à l'agent où il travaille et
// à qui il peut s'adresser, sans qu'il ait à appeler quoi que ce soit.
func TestInstructionsCarryTheIdentity(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})
	srv.siblings = []string{"WEB", "API"}

	got := srv.instructions()
	for _, expected := range []string{"CORE", "omiros", "WEB", "API", "check_inbox"} {
		if !strings.Contains(got, expected) {
			t.Errorf("les instructions ne mentionnent pas %q:\n%s", expected, got)
		}
	}

	// Sans projet frère, create_issue n'a personne à qui écrire : le dire évite un aller-retour.
	srv.siblings = nil
	if !strings.Contains(srv.instructions(), "Aucun projet frère") {
		t.Errorf("sans projet frère, les instructions doivent le dire:\n%s", srv.instructions())
	}
}

func TestParseDeadline(t *testing.T) {
	absent, err := parseDeadline("")
	if err != nil || absent != nil {
		t.Errorf("échéance vide: (%v, %v), attendu (nil, nil)", absent, err)
	}

	parsed, err := parseDeadline("2026-09-01T12:00:00Z")
	if err != nil {
		t.Fatalf("parseDeadline: %v", err)
	}
	if parsed == nil || parsed.Year() != 2026 || parsed.Month() != 9 {
		t.Errorf("échéance = %v, attendu le 1er septembre 2026", parsed)
	}

	for _, bad := range []string{"demain", "2026-09-01", "01/09/2026"} {
		if _, err := parseDeadline(bad); err == nil {
			t.Errorf("parseDeadline(%q) accepté, attendu une erreur", bad)
		}
	}
}

// Une erreur d'outil doit revenir dans le résultat avec isError, jamais en erreur JSON-RPC :
// l'agent doit pouvoir la lire et se corriger.
func TestUnknownToolIsAToolErrorNotAProtocolError(t *testing.T) {
	srv := newTestServer(&bytes.Buffer{})

	result, err := srv.callTool(context.Background(),
		json.RawMessage(`{"name":"delete_everything","arguments":{}}`))
	if err != nil {
		t.Fatalf("callTool a renvoyé une erreur de protocole: %v", err)
	}
	if isError, _ := result["isError"].(bool); !isError {
		t.Errorf("résultat = %+v, attendu isError", result)
	}
}
