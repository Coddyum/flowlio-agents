package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | serverEntry      | An MCP server declaration, in the shape agents expect            | 66    |
// | readMCPFile      | Decodes the file into its top level and its server map           | 77    |
// | writeMCPFile     | Writes both halves back, everything it does not own untouched    | 101   |
// | writeMCPConfig   | Writes or completes the repository .mcp.json, never a secret     | 122   |
// | flowlioEntry     | Composes the flowlio-agents server declaration                   | 152   |
// | mcpLegacyEntry   | Says whether a pre-rename "flowlio" entry of ours is still there  | 167   |
// | removeMCPEntry   | Drops one entry, leaving the rest of the file byte for byte      | 187   |
//
// Fin du sommaire.
// =====================================================================
//
// THE SECRET NEVER ENTERS THIS FILE. That is the only rule that matters here.
//
// `.mcp.json` lives at the root of the repository, and users commit it — that is the whole
// point: the entire team and every agent share the same configuration. Writing a token in it
// would publish credentials on GitHub for every user of the product.
//
// It no longer carries a REFERENCE to a secret either. `${FLOWLIO_TOKEN}` was one variable name
// for every repository on a machine, so the second repository set up took a 401; and the address
// written alongside it froze whichever API the repo happened to be initialised against. The entry
// now names a project and a repo, and `internal/pkg/credentials` resolves the pair into an address
// and a token that are host-local.
//
// An existing file is COMPLETED, never replaced: a repository often already declares MCP
// servers, and overwriting them to install ours would be silent damage. A "flowlio-agents" entry
// that is already there is left untouched — it may have been adjusted by hand.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// mcpConfigName is the name agents recognise (Claude Code, Codex, OpenCode).
	mcpConfigName = ".mcp.json"
	// mcpServerKey names our entry in the file. It matches what the hosted product writes, so one
	// workflow prompt can name a single server and serve both deployments.
	mcpServerKey = "flowlio-agents"
	// mcpLegacyServerKey is what we called the same entry before that rename. Repositories set up by
	// an older binary still carry it, and `connect` has to say so rather than leave two entries
	// racing for the same board.
	mcpLegacyServerKey = "flowlio"
	// envProjectVar and envRepoVar are the two names the entry carries, and the two the MCP server
	// resolves its credentials from. Declared once: a rename on one side only would leave every
	// already-written .mcp.json pointing at nothing.
	envProjectVar = "FLOWLIO_PROJECT"
	envRepoVar    = "FLOWLIO_REPO"
	// tokenReference is what the entry used to carry. Named here only so a test can look for it and
	// prove it is gone: the file references no secret at all now.
	tokenReference = "${FLOWLIO_TOKEN}"

	mcpConfigPerm = 0o644
)

// serverEntry is an MCP server declaration. The other entries of the file are never decoded into
// this shape: they stay raw JSON, so they can be written back byte for byte.
type serverEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// readMCPFile decodes the file at path into its top level and its server map, both empty when the
// file does not exist yet.
//
// The top level keeps every key as raw JSON — including ones we have never heard of — because it is
// written back whole, and a repository's `.mcp.json` is not ours to normalise.
func readMCPFile(path string) (top, servers map[string]json.RawMessage, err error) {
	top, servers = map[string]json.RawMessage{}, map[string]json.RawMessage{}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, nil, fmt.Errorf("%s exists and is not readable JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		return top, servers, nil
	default:
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if existing, found := top["mcpServers"]; found {
		if err := json.Unmarshal(existing, &servers); err != nil {
			return nil, nil, fmt.Errorf("%s: unreadable mcpServers: %w", path, err)
		}
	}
	return top, servers, nil
}

// writeMCPFile writes both halves back to path.
func writeMCPFile(path string, top, servers map[string]json.RawMessage) error {
	encoded, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encoding mcpServers: %w", err)
	}
	top["mcpServers"] = encoded

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), mcpConfigPerm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeMCPConfig writes or completes the .mcp.json of the given directory and says what it did.
//
// Returns the path written and a boolean: false when a flowlio-agents entry already existed and was
// kept. Every other entry of the file is preserved byte for byte.
func writeMCPConfig(dir, project, repo string) (path string, written bool, err error) {
	path = filepath.Join(dir, mcpConfigName)

	top, servers, err := readMCPFile(path)
	if err != nil {
		return path, false, err
	}

	// An entry that is already there may have been adjusted by hand: leave it alone.
	if _, found := servers[mcpServerKey]; found {
		return path, false, nil
	}

	entry, err := json.Marshal(flowlioEntry(project, repo))
	if err != nil {
		return path, false, fmt.Errorf("encoding the %s entry: %w", mcpServerKey, err)
	}
	servers[mcpServerKey] = entry

	if err := writeMCPFile(path, top, servers); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// flowlioEntry composes the server declaration. It carries two NAMES and nothing else: this file is
// meant to be committed, so neither the secret nor the address of a particular host belongs in it.
//
// This is the one place the transport is decided. The day the engine serves MCP over HTTP, the
// entry becomes a url/headers pair and every caller above is unchanged.
func flowlioEntry(project, repo string) serverEntry {
	return serverEntry{
		Command: "flowlio",
		Args:    []string{"mcp"},
		Env: map[string]string{
			envProjectVar: project,
			envRepoVar:    repo,
		},
	}
}

// mcpLegacyEntry says whether the file still carries the entry we wrote under our old name.
//
// The command is checked, not just the key: "flowlio" is a plausible name for something else
// entirely, and renaming a stranger's entry would be worse than leaving ours behind.
func mcpLegacyEntry(dir string) (bool, error) {
	_, servers, err := readMCPFile(filepath.Join(dir, mcpConfigName))
	if err != nil {
		return false, err
	}

	raw, found := servers[mcpLegacyServerKey]
	if !found {
		return false, nil
	}
	var entry serverEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return false, nil
	}
	return entry.Command == "flowlio", nil
}

// removeMCPEntry drops one entry from the file and says whether there was one to drop. Everything
// else in the file survives — that is what makes `flowlio disconnect` safe to run in a repository
// that declares other servers.
func removeMCPEntry(dir, key string) (removed bool, err error) {
	path := filepath.Join(dir, mcpConfigName)

	top, servers, err := readMCPFile(path)
	if err != nil {
		return false, err
	}
	if _, found := servers[key]; !found {
		return false, nil
	}
	delete(servers, key)

	if err := writeMCPFile(path, top, servers); err != nil {
		return false, err
	}
	return true, nil
}
