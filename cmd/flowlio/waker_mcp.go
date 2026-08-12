package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                       | Ligne |
// |----------------------|--------------------------------------------------------------|-------|
// | hostedMCPConfigPath  | Path of a hosted repo's launch-time MCP config, beside its record | 34 |
// | writeHostedMCPConfig | Writes the config a woken Claude loads to authenticate headless   | 48 |
//
// Fin du sommaire.
// =====================================================================
//
// HOW A WOKEN CLAUDE AUTHENTICATES IN HOSTED (DESIGN-WAKE §6). A hosted repo's committed `.mcp.json`
// points Claude at the remote engine surface and leaves auth to an OAuth flow. The waker launches
// `claude -p`, which is non-interactive and cannot run that flow — so the woken agent connects to
// nothing and never reaches its inbox. This file writes a SECOND, host-local MCP config that carries
// the account token in an Authorization header, and the launch loads it with `--strict-mcp-config`
// so Claude uses THIS server and ignores the repo's OAuth one. Interactive sessions in the same repo
// are untouched: they never read this file.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// hostedMCPConfigPath yields the launch-time MCP config path for a hosted repo, beside its credential
// file so the two share a lifetime — removing the repo removes both.
func hostedMCPConfigPath(rf credentials.RepoFile) (string, error) {
	credPath, err := credentials.RepoRecordPath(rf)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(credPath, ".json") + ".mcp.json", nil
}

// writeHostedMCPConfig writes the MCP config a waker-launched Claude loads with
// `--mcp-config <path> --strict-mcp-config`, and returns its path.
//
// It holds the account token in an Authorization header, so it is 0600 like every credential in this
// directory, host-local, and never committed. It is rewritten on every launch, so a rotated account
// token or a moved engine address takes effect on the next wake with nothing to clean up.
func writeHostedMCPConfig(rf credentials.RepoFile, apiURL, token string) (string, error) {
	path, err := hostedMCPConfigPath(rf)
	if err != nil {
		return "", err
	}

	type httpServer struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	doc := map[string]any{
		"mcpServers": map[string]any{
			mcpServerKey: httpServer{
				Type:    "http",
				URL:     strings.TrimRight(apiURL, "/") + "/mcp?repo=" + url.QueryEscape(rf.RepoID),
				Headers: map[string]string{"Authorization": "Bearer " + token},
			},
		},
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding hosted MCP config for %s: %w", rf.Repo, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}
