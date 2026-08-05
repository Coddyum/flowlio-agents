package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readConfig reads the written file back, raw and decoded: both are used, one to look for a secret
// in the text, the other to check the structure.
func readConfig(t *testing.T, path string) (string, map[string]any) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", path, err, raw)
	}
	return string(raw), decoded
}

// THE GUARANTEE THAT COUNTS. The .mcp.json is meant to be committed: writing a token into it would
// amount to publishing credentials on GitHub, for every user at once.
//
// The test looks for the secret in the TEXT of the file, not in a structure: that is the only way
// to also cover a leak through a field nobody thought of.
func TestMCPConfigNeverContainsASecret(t *testing.T) {
	dir := t.TempDir()

	path, written, err := writeMCPConfig(dir, "http://localhost:42058")
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if !written {
		t.Fatal("the file was not written although the directory was empty")
	}

	raw, _ := readConfig(t, path)
	if strings.Contains(raw, "flw_") {
		t.Errorf("the file contains what looks like a token:\n%s", raw)
	}
	if !strings.Contains(raw, tokenReference) {
		t.Errorf("the file does not reference %s:\n%s", tokenReference, raw)
	}
}

// The written entry must be the one an agent knows how to launch: the command, its arguments, and
// the two environment variables.
func TestMCPConfigDeclaresARunnableServer(t *testing.T) {
	dir := t.TempDir()
	const apiURL = "http://localhost:42058"

	path, _, err := writeMCPConfig(dir, apiURL)
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if filepath.Base(path) != mcpConfigName {
		t.Errorf("written file = %s, expected %s", filepath.Base(path), mcpConfigName)
	}

	_, decoded := readConfig(t, path)
	servers, ok := decoded["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or badly shaped: %v", decoded)
	}
	entry, ok := servers[mcpServerKey].(map[string]any)
	if !ok {
		t.Fatalf("entry %q missing: %v", mcpServerKey, servers)
	}

	if entry["command"] != "flowlio" {
		t.Errorf("command = %v, expected flowlio", entry["command"])
	}
	env, ok := entry["env"].(map[string]any)
	if !ok {
		t.Fatalf("env missing: %v", entry)
	}
	if env["FLOWLIO_API_URL"] != apiURL {
		t.Errorf("FLOWLIO_API_URL = %v, expected %s", env["FLOWLIO_API_URL"], apiURL)
	}
	if env["FLOWLIO_TOKEN"] != tokenReference {
		t.Errorf("FLOWLIO_TOKEN = %v, expected %s", env["FLOWLIO_TOKEN"], tokenReference)
	}
}

// A repo often already has MCP servers declared. Overwriting them to install ours would be silent
// damage: the other entries, and the unknown top-level keys, survive.
func TestMCPConfigPreservesWhatItDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	existing := `{
  "mcpServers": {
    "github": {"command": "gh-mcp", "args": ["serve"]}
  },
  "someUnknownKey": {"kept": true}
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the existing file: %v", err)
	}

	if _, written, err := writeMCPConfig(dir, "http://localhost:42058"); err != nil || !written {
		t.Fatalf("writeMCPConfig: written=%v err=%v", written, err)
	}

	_, decoded := readConfig(t, path)
	servers := decoded["mcpServers"].(map[string]any)
	if _, found := servers["github"]; !found {
		t.Error("the pre-existing github server disappeared")
	}
	if _, found := servers[mcpServerKey]; !found {
		t.Error("our entry was not added")
	}
	if _, found := decoded["someUnknownKey"]; !found {
		t.Error("an unknown top-level key was lost")
	}
}

// An already-present flowlio entry may have been adjusted by hand — a different port, a command in
// an absolute path. Rewriting it would erase that setting without saying anything.
func TestMCPConfigLeavesAnExistingEntryAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	existing := `{"mcpServers": {"flowlio": {"command": "/opt/flowlio", "args": ["mcp"]}}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the existing file: %v", err)
	}

	_, written, err := writeMCPConfig(dir, "http://localhost:42058")
	if err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	if written {
		t.Error("an existing entry was rewritten")
	}

	raw, _ := readConfig(t, path)
	if !strings.Contains(raw, "/opt/flowlio") {
		t.Errorf("the manual setting was lost:\n%s", raw)
	}
}

// An unreadable file is not overwritten: we would rather fail and say so than destroy a file the
// user is in the middle of editing.
func TestMCPConfigRefusesToOverwriteBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcpConfigName)

	const broken = "{ this is not JSON"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("writing the broken file: %v", err)
	}

	if _, _, err := writeMCPConfig(dir, "http://localhost:42058"); err == nil {
		t.Fatal("an unreadable file was accepted")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(raw) != broken {
		t.Errorf("the unreadable file was modified:\n%s", raw)
	}
}
