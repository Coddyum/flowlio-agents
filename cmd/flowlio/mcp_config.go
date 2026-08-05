package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | serverEntry      | An MCP server declaration, in the shape agents expect            | 47    |
// | writeMCPConfig   | Writes or completes the repository .mcp.json, never a secret     | 57    |
// | flowlioEntry     | Composes the flowlio server declaration                          | 108   |
//
// Fin du sommaire.
// =====================================================================
//
// THE SECRET NEVER ENTERS THIS FILE. That is the only rule that matters here.
//
// `.mcp.json` lives at the root of the repository, and users commit it — that is the whole
// point: the entire team and every agent share the same configuration. Writing a token in it
// would publish credentials on GitHub for every user of the product. The value written is
// therefore ALWAYS the `${FLOWLIO_TOKEN}` reference, which the agent resolves from its
// environment.
//
// An existing file is COMPLETED, never replaced: a repository often already declares MCP
// servers, and overwriting them to install ours would be silent damage. A "flowlio" entry that
// is already there is left untouched — it may have been adjusted by hand.

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
	// mcpServerKey names our entry in the file.
	mcpServerKey = "flowlio"
	// tokenReference is the only admissible value for the token: a reference, not a secret.
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

// writeMCPConfig writes or completes the .mcp.json of the given directory and says what it did.
//
// Returns the path written and a boolean: false when a flowlio entry already existed and was
// kept. Every other entry of the file is preserved byte for byte.
func writeMCPConfig(dir, apiURL string) (path string, written bool, err error) {
	path = filepath.Join(dir, mcpConfigName)

	top := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &top); err != nil {
			return path, false, fmt.Errorf("%s exists and is not readable JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return path, false, fmt.Errorf("reading %s: %w", path, err)
	}

	servers := map[string]json.RawMessage{}
	if existing, found := top["mcpServers"]; found {
		if err := json.Unmarshal(existing, &servers); err != nil {
			return path, false, fmt.Errorf("%s: unreadable mcpServers: %w", path, err)
		}
	}

	// An entry that is already there may have been adjusted by hand: leave it alone.
	if _, found := servers[mcpServerKey]; found {
		return path, false, nil
	}

	entry, err := json.Marshal(flowlioEntry(apiURL))
	if err != nil {
		return path, false, fmt.Errorf("encoding the %s entry: %w", mcpServerKey, err)
	}
	servers[mcpServerKey] = entry

	encoded, err := json.Marshal(servers)
	if err != nil {
		return path, false, fmt.Errorf("encoding mcpServers: %w", err)
	}
	top["mcpServers"] = encoded

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return path, false, fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), mcpConfigPerm); err != nil {
		return path, false, fmt.Errorf("writing %s: %w", path, err)
	}
	return path, true, nil
}

// flowlioEntry composes the server declaration. The token is an environment REFERENCE: this file
// is meant to be committed, it must never carry a secret.
func flowlioEntry(apiURL string) serverEntry {
	return serverEntry{
		Command: "flowlio",
		Args:    []string{"mcp"},
		Env: map[string]string{
			"FLOWLIO_API_URL": apiURL,
			"FLOWLIO_TOKEN":   tokenReference,
		},
	}
}
