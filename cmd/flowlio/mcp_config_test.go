package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readConfig relit le fichier écrit, brut et décodé : les deux servent, l'un pour chercher un
// secret dans le texte, l'autre pour vérifier la structure.
func readConfig(t *testing.T, path string) (string, map[string]any) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lecture de %s : %v", path, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s n'est pas un JSON valide : %v\n%s", path, err, raw)
	}
	return string(raw), decoded
}

// LA GARANTIE QUI COMPTE. Le .mcp.json est fait pour être commité : y écrire un token
// reviendrait à publier des identifiants sur GitHub à l'échelle de tous les utilisateurs.
//
// Le test cherche le secret dans le TEXTE du fichier, pas dans une structure : c'est la seule
// façon de couvrir aussi une fuite par un champ auquel personne n'a pensé.
func TestMCPConfigNeverContainsASecret(t *testing.T) {
	dir := t.TempDir()

	path, written, err := writeMCPConfig(dir, "http://localhost:42058")
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if !written {
		t.Fatal("le fichier n'a pas été écrit alors que le répertoire était vide")
	}

	raw, _ := readConfig(t, path)
	if strings.Contains(raw, "flw_") {
		t.Errorf("le fichier contient ce qui ressemble à un token :\n%s", raw)
	}
	if !strings.Contains(raw, tokenReference) {
		t.Errorf("le fichier ne référence pas %s :\n%s", tokenReference, raw)
	}
}

// L'entrée écrite doit être celle qu'un agent sait lancer : la commande, ses arguments, et les
// deux variables d'environnement.
func TestMCPConfigDeclaresARunnableServer(t *testing.T) {
	dir := t.TempDir()
	const apiURL = "http://localhost:42058"

	path, _, err := writeMCPConfig(dir, apiURL)
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if filepath.Base(path) != mcpConfigName {
		t.Errorf("fichier écrit = %s, attendu %s", filepath.Base(path), mcpConfigName)
	}

	_, decoded := readConfig(t, path)
	servers, ok := decoded["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers absent ou de mauvaise forme : %v", decoded)
	}
	entry, ok := servers[mcpServerKey].(map[string]any)
	if !ok {
		t.Fatalf("entrée %q absente : %v", mcpServerKey, servers)
	}

	if entry["command"] != "flowlio" {
		t.Errorf("command = %v, attendu flowlio", entry["command"])
	}
	env, ok := entry["env"].(map[string]any)
	if !ok {
		t.Fatalf("env absent : %v", entry)
	}
	if env["FLOWLIO_API_URL"] != apiURL {
		t.Errorf("FLOWLIO_API_URL = %v, attendu %s", env["FLOWLIO_API_URL"], apiURL)
	}
	if env["FLOWLIO_TOKEN"] != tokenReference {
		t.Errorf("FLOWLIO_TOKEN = %v, attendu %s", env["FLOWLIO_TOKEN"], tokenReference)
	}
}

// Un dépôt a souvent déjà des serveurs MCP déclarés. Les écraser pour installer le nôtre serait
// un dégât silencieux : les autres entrées, et les clés de premier niveau inconnues, survivent.
func TestMCPConfigPreservesWhatItDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	existing := `{
  "mcpServers": {
    "github": {"command": "gh-mcp", "args": ["serve"]}
  },
  "uneCléInconnue": {"gardée": true}
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("écriture du fichier existant : %v", err)
	}

	if _, written, err := writeMCPConfig(dir, "http://localhost:42058"); err != nil || !written {
		t.Fatalf("writeMCPConfig: written=%v err=%v", written, err)
	}

	_, decoded := readConfig(t, path)
	servers := decoded["mcpServers"].(map[string]any)
	if _, found := servers["github"]; !found {
		t.Error("le serveur github préexistant a disparu")
	}
	if _, found := servers[mcpServerKey]; !found {
		t.Error("notre entrée n'a pas été ajoutée")
	}
	if _, found := decoded["uneCléInconnue"]; !found {
		t.Error("une clé de premier niveau inconnue a été perdue")
	}
}

// Une entrée flowlio déjà présente a pu être ajustée à la main — un port différent, une commande
// dans un chemin absolu. La réécrire effacerait ce réglage sans rien dire.
func TestMCPConfigLeavesAnExistingEntryAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	existing := `{"mcpServers": {"flowlio": {"command": "/opt/flowlio", "args": ["mcp"]}}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("écriture du fichier existant : %v", err)
	}

	_, written, err := writeMCPConfig(dir, "http://localhost:42058")
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if written {
		t.Error("une entrée existante a été réécrite")
	}

	raw, _ := readConfig(t, path)
	if !strings.Contains(raw, "/opt/flowlio") {
		t.Errorf("le réglage manuel a été perdu :\n%s", raw)
	}
}

// Un fichier illisible n'est pas écrasé : on préfère échouer et le dire plutôt que détruire un
// fichier que l'utilisateur est en train d'éditer.
func TestMCPConfigRefusesToOverwriteBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	const broken = "{ ceci n'est pas du JSON"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("écriture du fichier cassé : %v", err)
	}

	if _, _, err := writeMCPConfig(dir, "http://localhost:42058"); err == nil {
		t.Fatal("un fichier illisible a été accepté")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	if string(raw) != broken {
		t.Errorf("le fichier illisible a été modifié :\n%s", raw)
	}
}
