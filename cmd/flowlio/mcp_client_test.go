package main

import (
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// clearMCPEnv takes the four variables out of the ambient environment: a developer who has exported
// FLOWLIO_TOKEN in their shell would otherwise run a different test than CI does.
func clearMCPEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"FLOWLIO_API_URL", "FLOWLIO_TOKEN", envProjectVar, envRepoVar} {
		t.Setenv(name, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// THE ORDER IS THE CONTRACT. An explicit export has to beat everything, because every `.mcp.json`
// written before the rename carries one — inverting these two sources would silently move a session
// onto another board.
func TestMCPClientPrefersTheExplicitEnvironment(t *testing.T) {
	clearMCPEnv(t)
	t.Setenv("FLOWLIO_API_URL", "http://exported:1111")
	t.Setenv("FLOWLIO_TOKEN", "flw_exported")
	t.Setenv(envProjectVar, "acme")
	t.Setenv(envRepoVar, "API")

	if _, err := credentials.SaveRepo(credentials.RepoFile{
		APIURL: "http://from-the-file:2222", Project: "acme", Repo: "API", Token: "flw_file",
	}); err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}

	c, err := mcpClient()
	if err != nil {
		t.Fatalf("mcpClient: %v", err)
	}
	if c.BaseURL() != "http://exported:1111" {
		t.Errorf("client points at %s, expected the exported address", c.BaseURL())
	}
}

// The pair of names beats the admin credentials: a repository's agent runs under its own project
// token, and falling through to the admin one would hand it a scope nobody asked for.
func TestMCPClientResolvesTheRepoNamesBeforeTheAdminCredentials(t *testing.T) {
	clearMCPEnv(t)
	t.Setenv(envProjectVar, "acme")
	t.Setenv(envRepoVar, "API")

	if _, err := credentials.Save(credentials.File{APIURL: "http://admin:3333", Token: "flw_admin"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := credentials.SaveRepo(credentials.RepoFile{
		APIURL: "http://from-the-file:2222", Project: "acme", Repo: "API", Token: "flw_file",
	}); err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}

	c, err := mcpClient()
	if err != nil {
		t.Fatalf("mcpClient: %v", err)
	}
	if c.BaseURL() != "http://from-the-file:2222" {
		t.Errorf("client points at %s, expected the repository's own address", c.BaseURL())
	}
}

// The names travel in a committed file, the credentials do not. A teammate who clones the repo has
// the first and not the second, and the message they get has to be the command that fixes it —
// they cannot be expected to know a configuration directory exists.
func TestMCPClientNamesConnectWhenTheHostHasNoCredentials(t *testing.T) {
	clearMCPEnv(t)
	t.Setenv(envProjectVar, "acme")
	t.Setenv(envRepoVar, "api")

	_, err := mcpClient()
	if err == nil {
		t.Fatal("a session started with no credentials on this host")
	}
	if !strings.Contains(err.Error(), "flowlio connect API") {
		t.Errorf("the error does not name the command that fixes it: %v", err)
	}
}

// Only one half of the pair set is not a repository session at all: it falls through to the admin
// path rather than looking up a file under a name it does not have.
func TestMCPClientIgnoresAHalfSetPair(t *testing.T) {
	clearMCPEnv(t)
	t.Setenv(envProjectVar, "acme")

	if _, err := credentials.Save(credentials.File{APIURL: "http://admin:3333", Token: "flw_admin"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c, err := mcpClient()
	if err != nil {
		t.Fatalf("mcpClient: %v", err)
	}
	if c.BaseURL() != "http://admin:3333" {
		t.Errorf("client points at %s, expected the admin address", c.BaseURL())
	}
}
