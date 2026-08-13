package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// The launch-time MCP config carries the account token as a bearer for the ONE server
// `--allowedTools mcp__flowlio-agents` names, points it at the repo's `?repo=` surface, and is 0600
// because it holds a secret. This is what lets a woken headless Claude authenticate without OAuth.
func TestWriteHostedMCPConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	rf := credentials.RepoFile{Project: "hosted", Repo: "CORE", RepoID: "id-123", Path: "/tmp/core"}

	path, err := writeHostedMCPConfig(rf, "https://api.flowlio.me/", "flowlio_pat_secret")
	if err != nil {
		t.Fatalf("writeHostedMCPConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600 — it carries the account token", info.Mode().Perm())
	}

	var doc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("config is not valid json: %v", err)
	}

	s, ok := doc.MCPServers["flowlio-agents"]
	if !ok {
		t.Fatal("no flowlio-agents server — the woken Claude would have no inbox to read")
	}
	if s.Type != "http" {
		t.Errorf("type = %q, want http", s.Type)
	}
	if !strings.Contains(s.URL, "repo=id-123") {
		t.Errorf("url = %q, want the repo id in ?repo=", s.URL)
	}
	if strings.Contains(s.URL, "me//mcp") {
		t.Errorf("url = %q — the trailing slash in the api url was not trimmed", s.URL)
	}
	if got := s.Headers["Authorization"]; got != "Bearer flowlio_pat_secret" {
		t.Errorf("Authorization = %q, want the account token as a bearer", got)
	}
}
